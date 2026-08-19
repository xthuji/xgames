package session

import (
	"xgames/internal/games/ddz/msg"
	"xgames/internal/games/ddz/rule"
	"xgames/internal/platform/protocol"
)

// gameStateFor 生成玩家视角的对局状态 DTO（重连一次性还原牌桌）
func (gs *Session) gameStateFor(playerID string) *msg.GameStateDTO {
	phase := "bidding"
	switch gs.phase {
	case PhaseDoubling:
		phase = "doubling"
	case PhasePlaying:
		phase = "playing"
	}

	players := make([]protocol.PlayerInfo, len(gs.players))
	for i, p := range gs.players {
		players[i] = protocol.PlayerInfo{
			ID:         p.ID,
			Name:       p.Name,
			Seat:       p.Seat,
			Ready:      true,
			IsLandlord: p.IsLandlord,
			CardsCount: len(p.Hand),
			Online:     !p.IsOffline,
			IsBot:      p.IsBot,
			Afk:        p.Afk,
		}
	}

	idx := gs.playerIdx(playerID)
	var hand []protocol.CardInfo
	if idx >= 0 {
		hand = msg.CardsToInfos(gs.players[idx].Hand)
	}

	// 底牌：地主确定后才展示
	bottom := make([]protocol.CardInfo, 3)
	if gs.phase == PhaseDoubling || gs.phase == PhasePlaying {
		bottom = msg.CardsToInfos(gs.bottomCards)
	}

	// 当前回合玩家
	currentTurn := ""
	switch gs.phase {
	case PhaseBidding, PhaseDoubling:
		currentTurn = gs.players[gs.currentBidder].ID
	case PhasePlaying:
		currentTurn = gs.players[gs.currentPlayer].ID
	}

	mustPlay := gs.isMustPlay()
	canBeat := mustPlay
	if gs.phase == PhasePlaying && !mustPlay && idx >= 0 {
		canBeat = rule.CanBeatWithHand(gs.players[idx].Hand, gs.lastPlayedHand)
	}

	// 计算当前倍数：底倍 * 2^炸弹数
	multiplier := gs.bidMultiplier * (1 << gs.bombCount)

	return &msg.GameStateDTO{
		Phase:        phase,
		Players:      players,
		Hand:         hand,
		BottomCards:  bottom,
		CurrentTurn:  currentTurn,
		LastPlayed:   msg.CardsToInfos(gs.lastPlayedCards),
		LastPlayerID: gs.players[gs.lastPlayerIdx].ID,
		MustPlay:     mustPlay,
		CanBeat:      canBeat,
		Multiplier:   multiplier,
	}
}
