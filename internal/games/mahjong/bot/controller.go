package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"xgames/internal/games/mahjong/msg"
	"xgames/internal/games/mahjong/rule"
	"xgames/internal/platform/protocol"
)

// Controller 麻将机器人控制器
type Controller struct {
	engine        DecisionEngine
	logger        *slog.Logger
	replayEnabled bool // 是否启用复盘记录

	mu    sync.Mutex
	games map[string]*gameTrack

	delay func() time.Duration
}

// NewController 创建机器人控制器
func NewController(engine DecisionEngine, logger *slog.Logger) *Controller {
	return &Controller{
		engine:        engine,
		logger:        logger,
		games:         make(map[string]*gameTrack),
		delay:         nil,  // 未覆盖时按性格动态延迟（见 thinkDelayFor）
		replayEnabled: true, // 默认启用复盘
	}
}

// EnableReplay 启用复盘记录功能
func (c *Controller) EnableReplay() {
	c.replayEnabled = true
}

// Engine 暴露决策引擎
func (c *Controller) Engine() DecisionEngine { return c.engine }

// SetDelay 覆盖决策延迟（测试用）：注入后优先生效，跳过性格动态延迟，
// 保证测试加速与确定性；生产环境不调用
func (c *Controller) SetDelay(fn func() time.Duration) { c.delay = fn }

// defaultThinkDelay 生产环境默认思考延迟
func defaultThinkDelay() time.Duration {
	// 机器人出牌需等待最少 3.5 秒，模拟真人思考节奏，避免玩家出完牌后机器人瞬间连出
	// 同时确保前端有足够时间完成语音播报（约3-4秒），保持画音同步
	return time.Duration(3500+rand.IntN(500)) * time.Millisecond
}

// thinkDelayFor 机器人回合思考延迟：
// SetDelay 注入的固定延迟优先生效（测试加速/确定性）；
// 否则按性格动态延迟（其上限受会话回合超时 afkActDelay=3s 约束，见 personality.CalculateThinkDelay）
func (c *Controller) thinkDelayFor(p *BotPersonality, hand []int, turn int, isTenpai bool) time.Duration {
	if c.delay == nil && p != nil {
		return p.CalculateThinkDelay(hand, turn, isTenpai)
	}
	return c.actionDelay()
}

// actionDelay 碰/胡/杠询问的决策延迟（询问超时为 10s，无超时竞争）
func (c *Controller) actionDelay() time.Duration {
	if c.delay != nil {
		return c.delay()
	}
	return defaultThinkDelay()
}

type gameTrack struct {
	bots     map[string]string // botID → 名称
	seatOf   map[string]int    // playerID → 座位
	hands    map[string][]int  // playerID → 手牌（镜像）
	melds    map[string][]rule.Meld
	discards []int          // 全局牌河（所有人弃牌，记牌器数据源）
	turnOf   map[string]int // playerID → 已弃牌数（≈巡数）

	// 各玩家的舍牌序列（用于对手读牌）
	playerDiscards map[string][]int

	// 对手读牌器（集成 OpponentReader 模块）
	opponentReader *OpponentReader

	// P1: 复盘适配器
	replayAdapter *MahjongReplayAdapter

	// Phase 4: 机器人性格系统
	botPersonalities map[string]*BotPersonality // botID → 性格
	lastGameResults  map[string]bool            // botID → 上局是否获胜

	// 难度配置：来自房间选项 options["difficulty"]（easy/normal/hard，空回退 normal）
	difficulty DifficultyConfig
}

// OnBroadcast 对局广播消息
func (c *Controller) OnBroadcast(roomCode string, m protocol.Message, resp BotResponder) {
	switch m.Type {
	case protocol.MsgGameStart:
		p, err := protocol.ParsePayload[protocol.GameStartPayload](m)
		if err != nil {
			return
		}
		c.onGameStart(roomCode, p)
	case msg.MsgMjTurn:
		p, err := protocol.ParsePayload[msg.MjTurnPayload](m)
		if err != nil {
			return
		}
		c.onTurn(roomCode, p, resp)
	case msg.MsgMjDiscarded:
		p, err := protocol.ParsePayload[msg.MjDiscardedPayload](m)
		if err != nil {
			return
		}
		c.onDiscarded(roomCode, p)
	case msg.MsgMjPongMade:
		p, err := protocol.ParsePayload[msg.MjPongMadePayload](m)
		if err != nil {
			return
		}
		c.onPongMade(roomCode, p)
	case msg.MsgMjKongMade:
		p, err := protocol.ParsePayload[msg.MjKongMadePayload](m)
		if err != nil {
			return
		}
		c.onKongMade(roomCode, p)
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

// OnTargeted 定向消息。
// 麻将手牌是隐藏信息，无法从广播重建，镜像必须靠定向消息维护：
// MsgGameState 携带初始手牌，MsgMjDraw 按接收者归属摸牌。
func (c *Controller) OnTargeted(roomCode, playerID string, m protocol.Message, resp BotResponder) {
	switch m.Type {
	case protocol.MsgGameState:
		p, err := protocol.ParsePayload[protocol.GameStatePayload](m)
		if err != nil || !p.Available {
			return
		}
		var hs struct {
			Hand []int `json:"hand"`
		}
		if json.Unmarshal(p.State, &hs) != nil || len(hs.Hand) == 0 {
			return
		}
		c.withTrack(roomCode, func(gt *gameTrack) {
			gt.hands[playerID] = hs.Hand
		})
	case msg.MsgMjDraw:
		p, err := protocol.ParsePayload[msg.MjDrawPayload](m)
		if err != nil {
			return
		}
		c.withTrack(roomCode, func(gt *gameTrack) {
			gt.hands[playerID] = append(gt.hands[playerID], p.Tile)
		})
	case msg.MsgMjActionAvail:
		p, err := protocol.ParsePayload[msg.MjActionAvailPayload](m)
		if err != nil {
			return
		}
		c.onActionAvail(roomCode, playerID, p, resp)
	}
}

func (c *Controller) onGameStart(roomCode string, p protocol.GameStartPayload) {
	// 难度链路：房间选项 options["difficulty"] → 难度配置（空/未知回退 normal）
	difficulty := DifficultyFor(p.Options["difficulty"])

	gt := &gameTrack{
		bots:             make(map[string]string),
		seatOf:           make(map[string]int),
		hands:            make(map[string][]int),
		melds:            make(map[string][]rule.Meld),
		turnOf:           make(map[string]int),
		playerDiscards:   make(map[string][]int),
		opponentReader:   NewOpponentReader(), // 初始化对手读牌器
		botPersonalities: make(map[string]*BotPersonality),
		lastGameResults:  make(map[string]bool),
		difficulty:       difficulty,
	}

	// P1: 初始化复盘适配器（携带真实难度档位）
	if c.replayEnabled {
		gt.replayAdapter = NewMahjongReplayAdapter(roomCode, difficulty.Name)
	}

	for _, pl := range p.Players {
		gt.seatOf[pl.ID] = pl.Seat
		gt.hands[pl.ID] = nil
		if pl.IsBot {
			gt.bots[pl.ID] = pl.Name
			// Phase 4: 为每个机器人分配独特性格
			gt.botPersonalities[pl.ID] = createBotPersonality(pl.Name)
		}
		// 初始化玩家状态
		gt.opponentReader.PlayerStates[pl.ID] = &PlayerState{
			DiscardSequence: make([]int, 0),
			IsRiichi:        false,
			Pongs:           make([]int, 0),
			TurnNumber:      0,
		}
	}
	c.mu.Lock()
	c.games[roomCode] = gt
	c.mu.Unlock()
}

// createBotPersonality 根据机器人名称生成性格（确定性，便于复现）
func createBotPersonality(name string) *BotPersonality {
	// 使用名称哈希生成确定性性格
	hash := 0
	for _, ch := range name {
		hash = hash*31 + int(ch)
	}

	// 基于哈希生成性格参数（范围 0.3-0.8，避免极端值）
	rng := rand.New(rand.NewPCG(uint64(hash), uint64(hash>>16)))
	aggression := 0.3 + rng.Float64()*0.5
	conservatism := 0.3 + rng.Float64()*0.5
	patience := 0.3 + rng.Float64()*0.5

	return NewBotPersonality(aggression, conservatism, patience)
}

func (c *Controller) onTurn(roomCode string, p msg.MjTurnPayload, resp BotResponder) {
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
	hand := append([]int(nil), gt.hands[p.PlayerID]...)
	melds := gt.melds[p.PlayerID]
	discards := append([]int(nil), gt.discards...)
	turn := gt.turnOf[p.PlayerID]
	difficulty := gt.difficulty

	// 构建对手模型（用于防守判断）；低难度不启用对手读牌
	var opponents []OpponentModel
	if difficulty.ReadOpponent {
		opponents = c.buildOpponentModels(gt, p.PlayerID)
	}

	// P1: 保存复盘适配器引用
	replayAdapter := gt.replayAdapter
	c.mu.Unlock()

	if len(hand) == 0 {
		// 镜像缺失（极端时序）：不代打，交给会话回合超时兜底，避免空手牌 panic
		c.logger.Warn("机器人手牌镜像为空，跳过代打", "room", roomCode, "bot", name)
		return
	}

	// Phase 4: 计算动态思考延迟
	personality := gt.botPersonalities[p.PlayerID]
	isTenpai := rule.IsTenpaiWithMelds(hand, len(melds))
	delay := c.thinkDelayFor(personality, hand, turn, isTenpai)

	time.AfterFunc(delay, func() {
		meldInfos := make([]MeldInfo, len(melds))
		for i, m := range melds {
			meldInfos[i] = MeldInfo{Tile: m.Tile, Type: int(m.Type)}
		}

		// 记牌器视图：全局牌河 + 自己面子 + 自己手牌，供成组概率分析与防守判断
		ownMeldTiles := make([]int, 0, len(meldInfos)*4)
		for _, m := range meldInfos {
			n := 3
			if m.Type != int(rule.MeldPong) { // 非碰（杠类）面子占 4 张
				n = 4
			}
			for k := 0; k < n; k++ {
				ownMeldTiles = append(ownMeldTiles, m.Tile)
			}
		}
		seen := rule.VisibleCounts([][]int{discards}, [][]int{ownMeldTiles}, hand)

		tile := c.engine.DecideDiscardWithErrors(DiscardContext{
			Hand:           hand,
			Melds:          meldInfos,
			DiscardedTiles: discards,
			MyDiscards:     gt.playerDiscards[p.PlayerID], // 自己打出的牌（现物兜底用）
			TurnNumber:     turn,
			Seen:           seen,
			OpponentModels: opponents,
			Difficulty:     difficulty,
		})

		// P1: 记录决策日志
		if replayAdapter != nil {
			// 简化：假设向听数和进张数从引擎内部获取（这里使用默认值）
			moveInfo := MahjongMoveInfo{
				Tile:         tile,
				ActionType:   "discard",
				ShantenAfter: -1, // TODO: 从引擎获取
				Ukeire:       0,  // TODO: 从引擎获取
				DangerLevel:  calculateDangerLevel(tile, discards, opponents),
				IsTing:       false, // TODO: 从引擎获取
			}

			reasoning := generateMahjongReasoning(moveInfo, "", opponents, turn+1)

			// 创建简化的候选列表（只包含选中的牌）
			candidates := []MahjongCandidate{{
				Tile:         tile,
				ActionType:   "discard",
				ShantenAfter: moveInfo.ShantenAfter,
				Ukeire:       moveInfo.Ukeire,
				DangerLevel:  moveInfo.DangerLevel,
				Score:        calculateMahjongScore(moveInfo),
			}}

			replayAdapter.RecordDecision(
				turn+1,
				name,
				hand,
				meldInfos,
				moveInfo,
				candidates,
				reasoning,
				opponents,
			)
		}

		action := protocol.NewMessage(msg.MsgMjDiscard, msg.MjDiscardPayload{Tile: tile})
		if err := resp.SubmitBotAction(p.PlayerID, action); err != nil {
			c.logger.Warn("机器人出牌失败", "room", roomCode, "bot", name, "err", err)
		}
	})
}

func (c *Controller) onDiscarded(roomCode string, p msg.MjDiscardedPayload) {
	c.withTrack(roomCode, func(gt *gameTrack) {
		hand := gt.hands[p.PlayerID]
		for i, t := range hand {
			if t == p.Tile {
				gt.hands[p.PlayerID] = append(hand[:i], hand[i+1:]...)
				break
			}
		}
		// 牌河与巡数跟踪（记牌器数据源）
		gt.discards = append(gt.discards, p.Tile)
		gt.turnOf[p.PlayerID]++
		// 记录各玩家的舍牌序列（用于对手读牌）
		gt.playerDiscards[p.PlayerID] = append(gt.playerDiscards[p.PlayerID], p.Tile)

		// 更新 OpponentReader
		gt.opponentReader.UpdateDiscard(p.PlayerID, p.Tile)
	})
}

func (c *Controller) onActionAvail(roomCode, playerID string, p msg.MjActionAvailPayload, resp BotResponder) {
	c.mu.Lock()
	gt := c.games[roomCode]
	if gt == nil {
		c.mu.Unlock()
		return
	}
	botName, isBot := gt.bots[playerID]
	difficulty := gt.difficulty
	if !isBot {
		c.mu.Unlock()
		return
	}
	hand := append([]int(nil), gt.hands[playerID]...)
	meldCount := len(gt.melds[playerID])
	c.mu.Unlock()

	time.AfterFunc(c.actionDelay(), func() {
		pong, win := c.engine.DecideAction(context.Background(), botName, hand, p.Tile, meldCount, difficulty)

		if win && p.CanWin {
			action := protocol.NewMessage(msg.MsgMjWin, msg.MjWinPayload{IsSelfDraw: false})
			_ = resp.SubmitBotAction(playerID, action)
			return
		}
		if pong && p.CanPong {
			action := protocol.NewMessage(msg.MsgMjPong, msg.MjPongPayload{Tile: p.Tile})
			_ = resp.SubmitBotAction(playerID, action)
			return
		}
		action := protocol.NewMessage(msg.MsgMjPass, msg.MjPassPayload{})
		_ = resp.SubmitBotAction(playerID, action)
	})
}

func (c *Controller) onPongMade(roomCode string, p msg.MjPongMadePayload) {
	c.withTrack(roomCode, func(gt *gameTrack) {
		// 碰消耗碰牌者 2 张手牌
		c.removeTilesFromTrackHand(gt, p.PlayerID, p.Tile, 2)
		if _, isBot := gt.bots[p.PlayerID]; isBot {
			gt.melds[p.PlayerID] = append(gt.melds[p.PlayerID], rule.Meld{
				Type: rule.MeldPong, Tile: p.Tile,
			})
		}
	})
}

func (c *Controller) onKongMade(roomCode string, p msg.MjKongMadePayload) {
	c.withTrack(roomCode, func(gt *gameTrack) {
		// 暗杠消耗 4 张，明杠 3 张，补杠 1 张
		remove := 3
		if p.IsAnKong {
			remove = 4
		} else if p.IsAddKong {
			remove = 1
		}
		c.removeTilesFromTrackHand(gt, p.PlayerID, p.Tile, remove)
		if _, isBot := gt.bots[p.PlayerID]; isBot {
			if p.IsAddKong {
				// 补杠：将已碰面子升级为杠
				for i := range gt.melds[p.PlayerID] {
					if gt.melds[p.PlayerID][i].Type == rule.MeldPong && gt.melds[p.PlayerID][i].Tile == p.Tile {
						gt.melds[p.PlayerID][i].Type = rule.MeldKong
						break
					}
				}
			} else {
				meldType := rule.MeldKong
				if p.IsAnKong {
					meldType = rule.MeldAnKong
				}
				gt.melds[p.PlayerID] = append(gt.melds[p.PlayerID], rule.Meld{
					Type: meldType, Tile: p.Tile,
				})
			}
		}
	})
}

// removeTilesFromTrackHand 从手牌镜像中移除指定数量的牌
func (c *Controller) removeTilesFromTrackHand(gt *gameTrack, playerID string, tile int, n int) {
	for k := 0; k < n; k++ {
		hand := gt.hands[playerID]
		for i, t := range hand {
			if t == tile {
				gt.hands[playerID] = append(hand[:i], hand[i+1:]...)
				break
			}
		}
	}
}

func (c *Controller) withTrack(roomCode string, fn func(gt *gameTrack)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gt := c.games[roomCode]; gt != nil {
		fn(gt)
	}
}

// buildOpponentModels 构建对手模型（用于防守判断，集成 OpponentReader）
func (c *Controller) buildOpponentModels(gt *gameTrack, selfID string) []OpponentModel {
	// 疑似听牌推断阈值随难度缩放（困难档更早警惕对手听牌）
	inferDiscards, inferTurn := gt.difficulty.TenpaiInferGates()
	models := make([]OpponentModel, 0, len(gt.playerDiscards))
	for playerID, discards := range gt.playerDiscards {
		if playerID == selfID {
			continue // 跳过自己
		}

		// 使用 OpponentReader 的状态进行更精确的判断
		state, hasState := gt.opponentReader.PlayerStates[playerID]

		var isTenpai bool
		if hasState && state != nil {
			// 基于 OpponentReader 的巡数和舍牌序列判断
			isTenpai = len(state.DiscardSequence) > inferDiscards && state.TurnNumber > inferTurn
		} else {
			// fallback：使用原始逻辑
			isTenpai = len(discards) > inferDiscards && gt.turnOf[playerID] > inferTurn
		}

		models = append(models, OpponentModel{
			PlayerID:        playerID,
			IsTenpai:        isTenpai,
			IsRiichi:        false, // 推倒胡无立直，保留字段供其他麻将变体使用
			DiscardSequence: append([]int(nil), discards...),
		})
	}
	return models
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

// calculateDangerLevel 计算打出的牌的危险度（简化版）
func calculateDangerLevel(tile int, discards []int, opponents []OpponentModel) int {
	// 简化逻辑：根据是否有人听牌和牌的可见数量估算危险度
	if len(opponents) == 0 {
		return 0
	}

	// 统计该牌已出现的次数
	visibleCount := 0
	for _, d := range discards {
		if d == tile {
			visibleCount++
		}
	}

	// 如果有人听牌且该牌未现物，危险度高
	tenpaiCount := 0
	for _, opp := range opponents {
		if opp.IsTenpai {
			tenpaiCount++
		}
	}

	if tenpaiCount == 0 {
		return 20 // 无人听牌，低危险
	}

	if visibleCount >= 3 {
		return 10 // 已现3张以上，相对安全
	}

	// 有人听牌且未现物，高危险
	return 70 + tenpaiCount*10
}

// generateMahjongReasoning 生成麻将决策推理说明
func generateMahjongReasoning(move MahjongMoveInfo, baseReasoning string, opponents []OpponentModel, round int) string {
	var sb strings.Builder

	// 基础推理
	if baseReasoning != "" {
		sb.WriteString(baseReasoning)
		sb.WriteString("\n")
	}

	// 向听数分析
	if move.ShantenAfter == 0 {
		sb.WriteString("- 已听牌状态\n")
		if len(move.TingWaits) > 0 {
			waits := make([]string, len(move.TingWaits))
			for i, w := range move.TingWaits {
				waits[i] = tileToString(w)
			}
			sb.WriteString(fmt.Sprintf("- 听牌: %s\n", strings.Join(waits, ", ")))
		}
	} else if move.ShantenAfter > 0 {
		sb.WriteString(fmt.Sprintf("- 向听数: %d，还需 %d 步听牌\n", move.ShantenAfter, move.ShantenAfter))
	}

	// 进张分析
	if move.Ukeire > 0 {
		sb.WriteString(fmt.Sprintf("- 有效进张: %d 种\n", move.Ukeire))
	}

	// 危险度分析
	if move.DangerLevel > 70 {
		sb.WriteString(fmt.Sprintf("- 高危险度 (%d/100)，需谨慎\n", move.DangerLevel))
	} else if move.DangerLevel < 30 {
		sb.WriteString(fmt.Sprintf("- 低危险度 (%d/100)，相对安全\n", move.DangerLevel))
	}

	// 对手分析
	tenpaiCount := 0
	for _, opp := range opponents {
		if opp.IsTenpai {
			tenpaiCount++
		}
	}
	if tenpaiCount > 0 {
		sb.WriteString(fmt.Sprintf("- %d 家疑似听牌，注意防守\n", tenpaiCount))
	}

	// 巡数分析
	if round <= 6 {
		sb.WriteString("- 早期阶段，优先牌效\n")
	} else if round <= 12 {
		sb.WriteString("- 中期阶段，攻守平衡\n")
	} else {
		sb.WriteString("- 晚巡阶段，注重防守\n")
	}

	return sb.String()
}

func tileToString(tile int) string {
	// 麻将牌映射（0-33）
	suits := []string{"万", "筒", "条"}
	honors := []string{"东", "南", "西", "北", "白", "发", "中"}

	if tile < 27 {
		// 数牌：0-8万，9-17筒，18-26条
		suit := tile / 9
		number := tile%9 + 1
		return fmt.Sprintf("%d%s", number, suits[suit])
	} else {
		// 字牌：27-33
		return honors[tile-27]
	}
}

func calculateMahjongScore(move MahjongMoveInfo) float64 {
	// 综合评分：向听数越低越好，进张越多越好，危险度越低越好
	score := float64(-move.ShantenAfter*1000 + move.Ukeire*10 - move.DangerLevel)
	if move.IsTing {
		score += 500 // 听牌加分
	}
	return score
}
