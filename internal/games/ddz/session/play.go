package session

import (
	"strings"
	"time"

	"xgames/internal/games/bot/replay"
	"xgames/internal/games/ddz/bot"
	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/msg"
	"xgames/internal/games/ddz/rule"
	"xgames/internal/platform/protocol"
)

// handlePlayCards 处理出牌（仅 Actor 内调用）
func (gs *Session) handlePlayCards(playerID string, cardInfos []protocol.CardInfo) error {
	if gs.phase != PhasePlaying {
		return ErrGameNotStart
	}
	current := gs.players[gs.currentPlayer]
	if current.ID != playerID {
		return ErrNotYourTurn
	}

	cards := msg.InfosToCards(cardInfos)

	// 校验牌在手
	if !cardsInHand(current.Hand, cards) {
		return ErrInvalidCards
	}

	// 解析牌型
	handToPlay, err := rule.ParseHand(cards)
	if err != nil {
		return ErrInvalidCards
	}

	// 校验能否压过上家（新一轮除外）
	if !gs.isMustPlay() && !rule.CanBeat(handToPlay, gs.lastPlayedHand) {
		return ErrCannotBeat
	}

	// 全部校验通过才停计时器
	gs.stopTurnTimer()

	// 复盘：记录真人玩家出牌决策快照（bot 与托管代打不记录）
	gs.recordUserDecision(current, cards, false)

	// 更新牌局状态
	gs.lastPlayedHand = handToPlay
	gs.lastPlayedCards = cards
	gs.lastPlayerIdx = gs.currentPlayer
	gs.consecutivePasses = 0
	gs.beatStreaks[gs.currentPlayer] = 0

	// 炸弹 / 王炸翻倍
	if handToPlay.Type == rule.Bomb || handToPlay.Type == rule.Rocket {
		gs.bombCount++
	}
	if current.IsLandlord {
		gs.landlordPlays++
	} else {
		gs.farmerPlays++
	}

	// 出牌记录（挂机托管决策上下文：记牌器数据源与最近两手）
	gs.oncePlayed[gs.currentPlayer] = true
	gs.recentPlays[1] = gs.recentPlays[0]
	gs.recentPlays[0] = bot.PlayRecord{
		Played:     handToPlay,
		PlayerName: current.Name,
		IsLandlord: current.IsLandlord,
	}
	for _, cc := range cards {
		gs.playedRankCounts[cc.Rank]++
	}

	// 从手牌移除
	current.Hand = card.RemoveCards(current.Hand, cards)

	// 出牌展示从大到小排序
	sorted := make([]card.Card, len(cards))
	copy(sorted, cards)
	sortHandDesc(sorted)

	gs.broadcast(msg.MsgCardPlayed, msg.CardPlayedPayload{
		PlayerID:   playerID,
		PlayerName: current.Name,
		Cards:      msg.CardsToInfos(sorted),
		CardsLeft:  len(current.Hand),
		HandType:   handToPlay.Type.String(),
	})

	// 手牌清空 → 结算
	if len(current.Hand) == 0 {
		gs.endGame(current)
		return nil
	}

	gs.currentPlayer = (gs.currentPlayer + 1) % 3
	gs.notifyPlayTurn()
	return nil
}

// handlePass 处理不出（仅 Actor 内调用）
func (gs *Session) handlePass(playerID string) error {
	if gs.phase != PhasePlaying {
		return ErrGameNotStart
	}
	current := gs.players[gs.currentPlayer]
	if current.ID != playerID {
		return ErrNotYourTurn
	}
	if gs.isMustPlay() {
		return ErrMustPlay
	}

	gs.stopTurnTimer()
	gs.consecutivePasses++
	gs.beatStreaks[gs.currentPlayer]++ // 连续让牌计数（拆牌重计划触发依据）

	// 复盘：记录真人玩家过牌决策快照
	gs.recordUserDecision(current, nil, true)

	gs.broadcast(msg.MsgPlayerPass, msg.PlayerPassPayload{
		PlayerID:   playerID,
		PlayerName: current.Name,
	})

	// 连续两人不出 → 清空上家开新轮
	if gs.consecutivePasses >= 2 {
		gs.lastPlayedHand = rule.ParsedHand{}
		gs.lastPlayedCards = nil
		gs.lastPlayerIdx = (gs.currentPlayer + 1) % 3
		gs.consecutivePasses = 0
	}

	gs.currentPlayer = (gs.currentPlayer + 1) % 3
	gs.notifyPlayTurn()
	return nil
}

// notifyPlayTurn 通知当前玩家出牌并启动计时
func (gs *Session) notifyPlayTurn() {
	player := gs.players[gs.currentPlayer]
	mustPlay := gs.isMustPlay()

	canBeat := mustPlay // 新一轮肯定能出
	if !mustPlay {
		canBeat = rule.CanBeatWithHand(player.Hand, gs.lastPlayedHand)
	}

	turnSecs := int(gs.turnDelayFor(gs.currentPlayer).Seconds())
	gs.broadcast(msg.MsgPlayTurn, msg.PlayTurnPayload{
		PlayerID: player.ID,
		Timeout:  turnSecs,
		MustPlay: mustPlay,
		CanBeat:  canBeat,
	})

	// 无牌可出自动过牌：给人类用户 2s 观察上家出牌后再自动过（独立计时类型，不误判为超时转挂机）
	if !mustPlay && !canBeat && !player.IsBot {
		gs.logger.Info("无牌可出，自动过牌", "player", player.Name, "room", gs.roomCode)
		gs.startTurnTimer(timerAutoPass, 2*time.Second)
		return
	}

	gs.startTurnTimer(timerPlay, gs.turnDelayFor(gs.currentPlayer))
}

// handleTurnTimeout 回合超时：
//   - 叫分/加倍超时：先转挂机再自动不叫/不加倍
//   - 出牌超时：先转挂机再由机器人策略托管（真人仅挂机后才被代打）
//   - 无牌可出的自动过牌：直接过牌，与挂机无关
func (gs *Session) handleTurnTimeout() {
	switch gs.timerKind {
	case timerBid:
		gs.timerKind = timerNone
		gs.enterAfkOnTimeout(gs.currentBidder)
		switch gs.phase {
		case PhaseBidding:
			_ = gs.handleBid(gs.players[gs.currentBidder].ID, 0)
		case PhaseDoubling:
			_ = gs.handleDouble(gs.players[gs.currentBidder].ID, false)
		}
	case timerPlay:
		gs.timerKind = timerNone
		gs.enterAfkOnTimeout(gs.currentPlayer)
		gs.autoplayFor(gs.currentPlayer)
	case timerAutoPass:
		gs.timerKind = timerNone
		gs.autoplayFor(gs.currentPlayer)
	}
}

// recordUserDecision 记录真人玩家的一次出牌/过牌决策快照（仅 Actor 内调用）。
// Round 为该玩家本局的第 N 次决策，报告以玩家视角呈现。
func (gs *Session) recordUserDecision(p *Player, chosen []card.Card, isPass bool) {
	if gs.playerLogger == nil || p.IsBot || p.Afk {
		return
	}
	idx := gs.playerIdx(p.ID)
	up := (idx + 2) % 3
	down := (idx + 1) % 3

	mustPlay := gs.isMustPlay()
	snap := bot.UserMoveSnapshot{
		Hand:             append([]card.Card(nil), p.Hand...),
		Chosen:           chosen,
		LastPlayed:       gs.lastPlayedHand,
		RecentPlays:      gs.recentPlays,
		MustPlay:         mustPlay,
		CanBeat:          mustPlay || rule.CanBeatWithHand(p.Hand, gs.lastPlayedHand),
		IsLandlord:       p.IsLandlord,
		UpIsLandlord:     gs.players[up].IsLandlord,
		DownIsLandlord:   gs.players[down].IsLandlord,
		PlayerCounts:     [2]int{len(gs.players[up].Hand), len(gs.players[down].Hand)},
		PlayedRankCounts: gs.copyPlayedRankCounts(),
		PlayedBombs:      gs.bombCount,
		HasPlayed:        gs.oncePlayed[idx],
		UnbeatenStreak:   gs.beatStreaks[idx],
	}

	gs.playerLogger.Record(replay.PlayerDecision{
		Round:      len(gs.playerLogger.DecisionsByPlayer(p.ID)) + 1,
		PlayerID:   p.ID,
		PlayerName: p.Name,
		ChosenDesc: describeDdzCards(chosen),
		IsPass:     isPass,
		Timestamp:  time.Now().Unix(),
		Snapshot:   snap,
	})
}

// copyPlayedRankCounts 拷贝全场已出牌点数计数
func (gs *Session) copyPlayedRankCounts() map[card.Rank]int {
	m := make(map[card.Rank]int, len(gs.playedRankCounts))
	for r, n := range gs.playedRankCounts {
		m[r] = n
	}
	return m
}

// describeDdzCards 牌面描述（点数直接拼接，如 "33"、"34567"）
func describeDdzCards(cards []card.Card) string {
	if len(cards) == 0 {
		return ""
	}
	parts := make([]string, len(cards))
	for i, c := range cards {
		parts[i] = c.Rank.String()
	}
	return strings.Join(parts, "")
}

// cardsInHand 验证所出牌全部在手
func cardsInHand(hand, cards []card.Card) bool {
	handCopy := make([]card.Card, len(hand))
	copy(handCopy, hand)

	for _, c := range cards {
		found := false
		for i, h := range handCopy {
			if h.Suit == c.Suit && h.Rank == c.Rank {
				handCopy = append(handCopy[:i], handCopy[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
