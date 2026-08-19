package bot

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/msg"
	"xgames/internal/games/ddz/rule"
	"xgames/internal/platform/protocol"
)

// Controller 机器人控制器：消息驱动（技术设计 8.5）。
//
// Room 实现 session.Broadcaster 时把对局消息转发给本控制器；控制器跟踪每局
// 状态（机器人手牌、出牌历史、各家剩余牌数），在机器人回合延迟 1~3s 后调用
// DecisionEngine 决策，并通过 Responder（即 GameSession）执行动作。
type Controller struct {
	engine         DecisionEngine
	logger         *slog.Logger
	replayEnabled  bool // 是否启用复盘记录

	mu    sync.Mutex
	games map[string]*gameTrack

	delay func() time.Duration // 拟人思考延迟（测试可覆盖）
}

// NewController 创建机器人控制器
func NewController(engine DecisionEngine, logger *slog.Logger) *Controller {
	return &Controller{
		engine:        engine,
		logger:        logger,
		games:         make(map[string]*gameTrack),
		delay:         thinkDelay,
		replayEnabled: true, // 默认启用复盘
	}
}

// EnableReplay 启用/禁用复盘记录
func (c *Controller) EnableReplay(enabled bool) { c.replayEnabled = enabled }

// Engine 暴露决策引擎（供真人挂机托管复用同一套规则引擎）
func (c *Controller) Engine() DecisionEngine { return c.engine }

// SetDelay 覆盖决策延迟（测试用）
func (c *Controller) SetDelay(fn func() time.Duration) { c.delay = fn }

// thinkDelay 默认拟人延迟：随机 1~3s
func thinkDelay() time.Duration {
	return time.Duration(1000+rand.IntN(2000)) * time.Millisecond
}

// gameTrack 单局跟踪状态（Controller.mu 保护）
type gameTrack struct {
	bots       map[string]string // botID → 名称
	seatOf     map[string]int    // playerID → 座位
	playerAt   [3]string         // 座位 → playerID
	landlordID string
	bottom     []card.Card
	hands      map[string][]card.Card     // 机器人手牌（真人不跟踪）
	oncePlayed map[string]bool            // 机器人本局是否已出过牌（未开张时放弃让牌配合）
	counts     map[string]int             // playerID → 剩余牌数
	recent     [2]PlayRecord                        // [0]=最近一手
	actionSeq  [][]card.Rank                        // 完整出牌序列，nil=pass（记牌器数据来源）
	passes     map[string]rule.ParsedHand           // playerID → 当前一轮内其过牌时未压过的牌（过牌推理素材）
	cumPasses  map[string]map[rule.HandType]card.Rank // playerID → {牌型: 最高KeyRank} 累积过牌声明（跨轮次持久化）
	stuck      map[string]int                       // botID → 连续让牌/无法管住对手的次数（拆牌重计划触发依据）
	passStreak int                        // 连续过牌数（达 2 后下一个出牌即开启新一轮）
	bombNum    int                        // 累计炸弹数（含火箭）
	difficulty string                     // easy/normal/hard：决定人格化参数和记牌视图完整度
	personality BotPersonality            // P0: 当前难度对应的人格化参数
	replayAdapter *DDZReplayAdapter       // P1: 复盘适配器
	round        int                      // P1: 当前轮次
}

// --- Room 转发入口 ---

// OnBroadcast 对局广播消息（game_start/bid_turn/play_turn/card_played/...）
func (c *Controller) OnBroadcast(roomCode string, m protocol.Message, resp BotResponder) {
	switch m.Type {
	case protocol.MsgGameStart:
		p, err := protocol.ParsePayload[protocol.GameStartPayload](m)
		if err != nil {
			return
		}
		c.onGameStart(roomCode, p)
	case msg.MsgLandlord:
		p, err := protocol.ParsePayload[msg.LandlordPayload](m)
		if err != nil {
			return
		}
		c.onLandlord(roomCode, p)
	case msg.MsgBidTurn:
		p, err := protocol.ParsePayload[msg.BidTurnPayload](m)
		if err != nil {
			return
		}
		c.onBidTurn(roomCode, p, resp)
	case msg.MsgPlayTurn:
		p, err := protocol.ParsePayload[msg.PlayTurnPayload](m)
		if err != nil {
			return
		}
		c.onPlayTurn(roomCode, p, resp)
	case msg.MsgCardPlayed:
		p, err := protocol.ParsePayload[msg.CardPlayedPayload](m)
		if err != nil {
			return
		}
		c.onCardPlayed(roomCode, p)
	case msg.MsgPlayerPass:
		p, err := protocol.ParsePayload[msg.PlayerPassPayload](m)
		if err != nil {
			return
		}
		c.onPlayerPass(roomCode, p)
	case protocol.MsgGameOver:
		p, err := protocol.ParsePayload[protocol.GameOverPayload](m)
		if err == nil {
			c.onGameOver(roomCode, p)
		} else {
			c.mu.Lock()
			delete(c.games, roomCode)
			c.mu.Unlock()
		}
	}
}

// OnTargeted 定向发给机器人的消息（deal_cards）
func (c *Controller) OnTargeted(roomCode, playerID string, m protocol.Message, _ BotResponder) {
	if m.Type != msg.MsgDealCards {
		return
	}
	p, err := protocol.ParsePayload[msg.DealCardsPayload](m)
	if err != nil {
		return
	}
	c.withTrack(roomCode, func(gt *gameTrack) {
		if _, ok := gt.bots[playerID]; ok {
			gt.hands[playerID] = msg.InfosToCards(p.Cards)
		}
	})
}

// --- 消息处理 ---

func (c *Controller) onGameStart(roomCode string, p protocol.GameStartPayload) {
	difficulty := p.Options["difficulty"]
	if difficulty == "" {
		difficulty = "normal" // 默认普通难度
	}
	
	personality, ok := DefaultPersonalities[difficulty]
	if !ok {
		personality = DefaultPersonalities["normal"] // 未知难度回退到 normal
	}
	
	gt := &gameTrack{
		bots:       make(map[string]string),
		seatOf:     make(map[string]int),
		hands:      make(map[string][]card.Card),
		oncePlayed: make(map[string]bool),
		counts:     make(map[string]int),
		passes:     make(map[string]rule.ParsedHand),
		cumPasses:  make(map[string]map[rule.HandType]card.Rank),
		stuck:      make(map[string]int),
		difficulty:  difficulty,
		personality: personality, // P0: 初始化人格化参数
		round:       0,           // P1: 初始化轮次
	}
	
	// P1: 初始化复盘适配器
	if c.replayEnabled {
		gt.replayAdapter = NewDDZReplayAdapter(roomCode)
	}
	
	for _, pl := range p.Players {
		if pl.Seat < 0 || pl.Seat > 2 {
			continue
		}
		gt.seatOf[pl.ID] = pl.Seat
		gt.playerAt[pl.Seat] = pl.ID
		gt.counts[pl.ID] = 17
		if pl.IsBot {
			gt.bots[pl.ID] = pl.Name
		}
	}
	c.mu.Lock()
	c.games[roomCode] = gt
	c.mu.Unlock()
}

func (c *Controller) onLandlord(roomCode string, p msg.LandlordPayload) {
	c.withTrack(roomCode, func(gt *gameTrack) {
		gt.landlordID = p.PlayerID
		gt.bottom = msg.InfosToCards(p.BottomCards)
		gt.counts[p.PlayerID] += 3
		if _, ok := gt.bots[p.PlayerID]; ok {
			gt.hands[p.PlayerID] = append(gt.hands[p.PlayerID], gt.bottom...)
		}
	})
}

func (c *Controller) onBidTurn(roomCode string, p msg.BidTurnPayload, resp BotResponder) {
	c.mu.Lock()
	gt := c.games[roomCode]
	if gt == nil {
		c.mu.Unlock()
		return
	}
	name, isBot := gt.bots[p.PlayerID]
	if !isBot {
		c.mu.Unlock()
		return
	}
	hand := append([]card.Card(nil), gt.hands[p.PlayerID]...)
	phase := p.Phase
	highBid := p.HighBid
	c.mu.Unlock()

	go func() {
		time.Sleep(c.delay())
		var action protocol.Message
		if phase == "double" {
			double := c.engine.DecideDouble(context.Background(), name, hand)
			action = protocol.NewMessage(msg.MsgDouble, msg.DoublePayload{Double: double})
		} else {
			score := c.engine.DecideBidScore(context.Background(), name, hand, highBid)
			action = protocol.NewMessage(msg.MsgBid, msg.BidPayload{Score: score})
		}
		if err := resp.SubmitBotAction(p.PlayerID, action); err != nil {
			c.logger.Warn("机器人叫分/加倍失败", "room", roomCode, "bot", name, "err", err)
		}
	}()
}

func (c *Controller) onPlayTurn(roomCode string, p msg.PlayTurnPayload, resp BotResponder) {
	c.mu.Lock()
	gt := c.games[roomCode]
	if gt == nil {
		c.mu.Unlock()
		return
	}
	name, isBot := gt.bots[p.PlayerID]
	if !isBot {
		c.mu.Unlock()
		return
	}
	gctx := c.buildContextLocked(gt, p.PlayerID, p.MustPlay, p.CanBeat)
	
	// P1: 轮次递增
	gt.round++
	round := gt.round
	
	// P1: 保存replay adapter引用（在锁外使用）
	var replayAdapter *DDZReplayAdapter
	if c.replayEnabled && gt.replayAdapter != nil {
		replayAdapter = gt.replayAdapter
	}
	
	c.mu.Unlock()

	go func() {
		time.Sleep(c.delay())
		
		// P1: 记录决策前的手牌快照
		handSnapshot := append([]card.Card(nil), gctx.Hand...)
		
		cards := c.engine.DecidePlay(context.Background(), name, gctx)
		
		// P1: 记录决策日志
		if replayAdapter != nil {
			isLandlord := gctx.IsLandlord
			reasoning := generateDecisionReasoning(gctx, cards)
			
			// 创建简化的候选列表（基于决策结果推断）
			var candidates []rule.Hint
			if cards != nil {
				ph, _ := rule.ParseHand(cards)
				if !ph.IsEmpty() {
					candidates = []rule.Hint{{Cards: cards, Score: 100}}
				}
			}
			
			replayAdapter.RecordDecision(
				round,
				name,
				isLandlord,
				handSnapshot,
				gctx,
				candidates,
				cards,
				reasoning,
			)
		}
		
		// 跟踪连续让牌：多次无法管住对手时，后续决策触发拆牌重计划夺取牌权
		c.withTrack(roomCode, func(gt *gameTrack) {
			if cards == nil {
				gt.stuck[p.PlayerID]++
			} else {
				delete(gt.stuck, p.PlayerID)
			}
		})
		var action protocol.Message
		if cards == nil {
			action = protocol.NewMessage(msg.MsgPass, nil)
		} else {
			action = protocol.NewMessage(msg.MsgPlayCards, msg.PlayCardsPayload{Cards: msg.CardsToInfos(cards)})
		}
		if err := resp.SubmitBotAction(p.PlayerID, action); err != nil {
			c.logger.Warn("机器人出牌失败", "room", roomCode, "bot", name, "err", err)
		}
	}()
}

// generateDecisionReasoning 生成决策推理说明
func generateDecisionReasoning(ctx GameContext, chosen []card.Card) string {
	var sb strings.Builder
	
	if ctx.MustPlay {
		sb.WriteString("- 领出回合，需要主动出击\n")
	} else {
		sb.WriteString("- 跟牌回合，根据上家出牌调整策略\n")
	}
	
	if ctx.IsLandlord {
		sb.WriteString("- 作为地主，需要快速跑牌\n")
	} else {
		if ctx.UpIsLandlord {
			sb.WriteString("- 作为地主下家（跑牌位），优先送走小牌\n")
		} else if ctx.DownIsLandlord {
			sb.WriteString("- 作为地主上家（顶牌位），需要压制地主\n")
		}
	}
	
	if ctx.PlayerCounts[0] <= 3 || ctx.PlayerCounts[1] <= 3 {
		sb.WriteString("- 进入残局阶段，需谨慎决策\n")
	}
	
	if ctx.UnbeatenStreak >= 2 {
		sb.WriteString(fmt.Sprintf("- 已连续让牌 %d 次，考虑拆牌夺权\n", ctx.UnbeatenStreak))
	}
	
	return sb.String()
}

func (c *Controller) onCardPlayed(roomCode string, p msg.CardPlayedPayload) {
	played := msg.InfosToCards(p.Cards)
	ranks := make([]card.Rank, len(played))
	for i, cc := range played {
		ranks[i] = cc.Rank
	}

	c.withTrack(roomCode, func(gt *gameTrack) {
		// 手牌与剩余数
		if _, ok := gt.bots[p.PlayerID]; ok {
			gt.hands[p.PlayerID] = removeCards(gt.hands[p.PlayerID], played)
			gt.oncePlayed[p.PlayerID] = true
		}
		gt.counts[p.PlayerID] = p.CardsLeft

		// 出牌历史（[0]=最近）
		ph, _ := rule.ParseHand(played)
		gt.recent[1] = gt.recent[0]
		gt.recent[0] = PlayRecord{
			Played:     ph,
			PlayerName: p.PlayerName,
			IsLandlord: p.PlayerID == gt.landlordID,
		}
		if ph.Type == rule.Bomb || ph.Type == rule.Rocket {
			gt.bombNum++
		}

		// 出牌序列（记牌器数据源）
		gt.actionSeq = append(gt.actionSeq, ranks)
		// 过牌推理失效：连续两过后该玩家出牌即开启新一轮，全部过牌记录作废；
		// 单个玩家一旦再次出牌，其旧的过牌记录也不再可靠。
		if gt.passStreak >= 2 {
			gt.passes = make(map[string]rule.ParsedHand)
		}
		delete(gt.passes, p.PlayerID)
		delete(gt.cumPasses, p.PlayerID) // 出牌后清除该玩家的累积过牌声明
		gt.passStreak = 0
	})
}

// onPlayerPass 玩家过牌：记录其未能压过的参照牌（当前最近一手），供手牌预估使用
func (c *Controller) onPlayerPass(roomCode string, p msg.PlayerPassPayload) {
	c.withTrack(roomCode, func(gt *gameTrack) {
		gt.actionSeq = append(gt.actionSeq, nil)
		gt.passStreak++
		if !gt.recent[0].Played.IsEmpty() {
			target := gt.recent[0].Played
			// 单轮过牌信息（开新轮时清空）
			gt.passes[p.PlayerID] = target
			// 累积过牌信息（跨轮次持久化，保留该牌型的历史最高 KeyRank）
			if gt.cumPasses[p.PlayerID] == nil {
				gt.cumPasses[p.PlayerID] = make(map[rule.HandType]card.Rank)
			}
			existing := gt.cumPasses[p.PlayerID][target.Type]
			if target.KeyRank > existing {
				gt.cumPasses[p.PlayerID][target.Type] = target.KeyRank
			}
		}
	})
}

// onGameOver 对局结束，清理本局跟踪状态。
// 复盘报告改由 session endGame 以真人玩家为中心生成（见 replay.SaveUserReport）。
func (c *Controller) onGameOver(roomCode string, _ protocol.GameOverPayload) {
	c.mu.Lock()
	if gt := c.games[roomCode]; gt != nil {
		delete(c.games, roomCode)
	}
	c.mu.Unlock()
}

// buildContextLocked 构造决策上下文（调用方持 c.mu）
func (c *Controller) buildContextLocked(gt *gameTrack, playerID string, mustPlay, canBeat bool) GameContext {
	seat := gt.seatOf[playerID]
	upID := gt.playerAt[(seat+2)%3]   // 上家
	downID := gt.playerAt[(seat+1)%3] // 下家

	// 记牌器：全副牌减去已出牌（含自己手牌）。难度只决定记牌完整度：
	// hard 跟踪全部点数；normal 仅跟踪关键牌（A/2/大小王）；视图中缺失的点数 = 未跟踪（未知）。
	full := gt.difficulty == "hard"
	remaining := make(map[card.Rank]int)
	for r := card.Rank3; r <= card.Rank2; r++ {
		if full || isKeyRank(r) {
			remaining[r] = 4
		}
	}
	remaining[card.RankBlackJoker] = 1
	remaining[card.RankRedJoker] = 1
	for _, move := range gt.actionSeq {
		for _, r := range move {
			if _, ok := remaining[r]; ok {
				remaining[r]--
			}
		}
	}

	return GameContext{
		IsLandlord:     playerID == gt.landlordID,
		Hand:           append([]card.Card(nil), gt.hands[playerID]...),
		BottomCards:    append([]card.Card(nil), gt.bottom...),
		RecentPlays:    gt.recent,
		MustPlay:       mustPlay,
		CanBeat:        canBeat,
		PlayerCounts:   [2]int{gt.counts[upID], gt.counts[downID]},
		RemainingCards: remaining,
		PlayedBombs:    gt.bombNum,
		UpIsLandlord:   upID == gt.landlordID,
		DownIsLandlord: downID == gt.landlordID,
		HasPlayed:      gt.oncePlayed[playerID],
		PassInfos:      [2]PassInfo{passInfoOf(gt, upID), passInfoOf(gt, downID)},
		UnbeatenStreak: gt.stuck[playerID],
		handCountCache: newHandCountCache(), // 每回合新建缓存，复用 MinHandCount 计算结果
		Personality:    gt.personality,       // P0: 注入人格化参数
	}
}

// passInfoOf 构造指定玩家的过牌推理信息（调用方持 c.mu）
// 优先返回单轮过牌信息，无单轮信息时返回累积过牌声明
func passInfoOf(gt *gameTrack, playerID string) PassInfo {
	if t, ok := gt.passes[playerID]; ok {
		return PassInfo{Valid: true, Target: t, CumulativePass: gt.cumPasses[playerID]}
	}
	// 无单轮信息时，返回累积过牌声明（可能为空 map）
	cum := gt.cumPasses[playerID]
	if cum != nil && len(cum) > 0 {
		return PassInfo{Valid: false, CumulativePass: cum} // Valid=false 表示无单轮参照牌
	}
	return PassInfo{}
}

// withTrack 在锁内修改指定房间的跟踪状态
func (c *Controller) withTrack(roomCode string, fn func(gt *gameTrack)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gt := c.games[roomCode]; gt != nil {
		fn(gt)
	}
}

// removeCards 从手牌中移除已出的牌
func removeCards(hand, played []card.Card) []card.Card {
	out := append([]card.Card(nil), hand...)
	for _, p := range played {
		for i, cc := range out {
			if cc == p {
				out = append(out[:i], out[i+1:]...)
				break
			}
		}
	}
	return out
}
