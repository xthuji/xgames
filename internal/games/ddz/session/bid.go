package session

import (
	"cmp"
	"math/rand/v2"
	"slices"

	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/msg"
)

// bid_turn / bid_result 的阶段标识
const (
	bidPhaseCall   = "call"   // 叫分阶段
	bidPhaseDouble = "double" // 加倍阶段
)

// handleBid 处理叫分（仅 Actor 内调用）
//
// 叫分流程：随机首叫，按序每人一次选择「不叫 / 1分 / 2分 / 3分」，
// 所叫分数必须严格大于当前最高分；有人叫 3 分立即定地主；
// 一圈表态后分最高者当地主（底倍 = 所叫分数）；全不叫则流局重发。
func (gs *Session) handleBid(playerID string, score int) error {
	if gs.phase != PhaseBidding {
		return ErrGameNotStart
	}
	if gs.players[gs.currentBidder].ID != playerID {
		return ErrNotYourTurn
	}
	if score < 0 || score > 3 || (score > 0 && score <= gs.highBid) {
		return ErrInvalidBid
	}

	gs.stopTurnTimer()
	player := gs.players[gs.currentBidder]

	if score > 0 {
		gs.highBid = score
		gs.highBidder = gs.currentBidder
	}
	gs.bidActed++

	gs.broadcastBidResult(player, bidPhaseCall, score, false)

	switch {
	case score == 3:
		// 叫 3 分立即定地主
		gs.bidMultiplier = gs.highBid
		gs.assignLandlord(gs.highBidder)
	case gs.bidActed >= 3 && gs.highBidder != -1:
		// 一圈表态完毕，最高分者当地主
		gs.bidMultiplier = gs.highBid
		gs.assignLandlord(gs.highBidder)
	case gs.bidActed >= 3:
		// 一圈无人叫分 → 流局重发
		gs.redeal()
	default:
		gs.currentBidder = (gs.currentBidder + 1) % 3
		gs.notifyBidTurn()
	}
	return nil
}

// handleDouble 处理加倍（仅 Actor 内调用）
//
// 地主确定后，两名农民按座序依次选择「加倍 / 不加倍」，每次加倍倍数 ×2；
// 两人表态完毕后进入出牌阶段（地主先出）。
func (gs *Session) handleDouble(playerID string, double bool) error {
	if gs.phase != PhaseDoubling {
		return ErrGameNotStart
	}
	if gs.players[gs.currentBidder].ID != playerID {
		return ErrNotYourTurn
	}

	gs.stopTurnTimer()
	player := gs.players[gs.currentBidder]

	if double {
		gs.bidMultiplier *= 2
	}
	gs.doubleActed++

	gs.broadcastBidResult(player, bidPhaseDouble, 0, double)

	// 两名农民均已表态 → 进入出牌阶段，地主先出
	if gs.doubleActed >= 2 {
		gs.startPlaying(gs.landlordIdx())
		return nil
	}

	gs.currentBidder = (gs.currentBidder + 1) % 3
	gs.notifyDoubleTurn()
	return nil
}

// assignLandlord 定地主：底牌归地主，进入加倍阶段
func (gs *Session) assignLandlord(idx int) {
	gs.grantBottom(idx)

	gs.phase = PhaseDoubling
	gs.doubleActed = 0
	gs.currentBidder = (idx + 1) % 3 // 从地主下家开始加倍
	gs.notifyDoubleTurn()
}

// forceLandlord 连续流局后强制指定地主：底倍为 1，跳过加倍直接出牌
func (gs *Session) forceLandlord(idx int) {
	gs.grantBottom(idx)
	gs.startPlaying(idx)
}

// grantBottom 底牌归地主并广播（调用方负责后续阶段流转）
func (gs *Session) grantBottom(idx int) {
	landlord := gs.players[idx]
	landlord.IsLandlord = true

	landlord.Hand = append(landlord.Hand, gs.bottomCards...)
	sortHandDesc(landlord.Hand)

	gs.broadcast(msg.MsgLandlord, msg.LandlordPayload{
		PlayerID:    landlord.ID,
		PlayerName:  landlord.Name,
		BottomCards: msg.CardsToInfos(gs.bottomCards),
		Multiplier:  gs.bidMultiplier,
	})

	// 给地主单独推送更新后的手牌
	gs.sendTo(landlord.ID, msg.MsgDealCards, msg.DealCardsPayload{
		Cards:       msg.CardsToInfos(landlord.Hand),
		BottomCards: msg.CardsToInfos(gs.bottomCards),
	})
}

// startPlaying 进入出牌阶段（idx 先出）
func (gs *Session) startPlaying(idx int) {
	gs.phase = PhasePlaying
	gs.currentPlayer = idx
	gs.lastPlayerIdx = idx
	gs.notifyPlayTurn()
}

// broadcastBidResult 广播叫分 / 加倍结果
func (gs *Session) broadcastBidResult(player *Player, phase string, score int, double bool) {
	mult := gs.bidMultiplier
	if phase == bidPhaseCall {
		// 叫分阶段底倍未定，展示当前最高叫分
		mult = max(gs.highBid, 1)
	}
	gs.broadcast(msg.MsgBidResult, msg.BidResultPayload{
		PlayerID:   player.ID,
		PlayerName: player.Name,
		Phase:      phase,
		Score:      score,
		Double:     double,
		Multiplier: mult,
	})
}

// notifyBidTurn 通知当前玩家叫分并启动计时
func (gs *Session) notifyBidTurn() {
	player := gs.players[gs.currentBidder]
	gs.broadcast(msg.MsgBidTurn, msg.BidTurnPayload{
		PlayerID:   player.ID,
		Timeout:    int(gs.turnDelayFor(gs.currentBidder).Seconds()),
		Phase:      bidPhaseCall,
		HighBid:    gs.highBid,
		Multiplier: max(gs.highBid, 1),
	})
	gs.startTurnTimer(timerBid, gs.turnDelayFor(gs.currentBidder))
}

// notifyDoubleTurn 通知当前农民选择加倍并启动计时
func (gs *Session) notifyDoubleTurn() {
	player := gs.players[gs.currentBidder]
	gs.broadcast(msg.MsgBidTurn, msg.BidTurnPayload{
		PlayerID:   player.ID,
		Timeout:    int(gs.turnDelayFor(gs.currentBidder).Seconds()),
		Phase:      bidPhaseDouble,
		Multiplier: gs.bidMultiplier,
	})
	gs.startTurnTimer(timerBid, gs.turnDelayFor(gs.currentBidder))
}

// redeal 流局重发；连续 maxRedeals 次后随机强制指定地主
func (gs *Session) redeal() {
	gs.redealCount++

	if gs.redealCount >= maxRedeals {
		gs.logger.Info("连续流局，强制指定地主", "room", gs.roomCode, "redeal_count", gs.redealCount)
		gs.dealNewRound()
		gs.bidMultiplier = 1
		gs.forceLandlord(rand.IntN(3))
		return
	}

	gs.logger.Info("无人叫分，流局重发", "room", gs.roomCode, "redeal_count", gs.redealCount)
	gs.startBiddingRound()
}

// sortHandDesc 手牌从大到小排序
func sortHandDesc(hand []card.Card) {
	slices.SortFunc(hand, func(a, b card.Card) int {
		return cmp.Compare(b.Rank, a.Rank)
	})
}
