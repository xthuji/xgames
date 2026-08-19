package session

import (
	"errors"
	"fmt"
	"time"

	"xgames/internal/games/ddz/msg"
	"xgames/internal/games/ddz/rule"
	"xgames/internal/platform/protocol"
)

// Snapshot GameSession 全量序列化结构（崩溃恢复用，见技术设计 6.1 / R-4）。
// 手牌用 CardInfo 列表（suit+rank 唯一确定一张牌），恢复时反向构造 Actor 状态机。
type Snapshot struct {
	RoomCode string `json:"room_code"`
	Phase    string `json:"phase"` // bidding / doubling / playing

	Players     []SnapshotPlayer    `json:"players"`
	BottomCards []protocol.CardInfo `json:"bottom_cards"`

	// 叫分 / 加倍
	CurrentBidder int `json:"current_bidder"`
	HighBid       int `json:"high_bid"`
	HighBidder    int `json:"high_bidder"`
	BidActed      int `json:"bid_acted"`
	DoubleActed   int `json:"double_acted"`
	BidMultiplier int `json:"bid_multiplier"`
	RedealCount   int `json:"redeal_count"`

	// 倍数累计
	BombCount     int `json:"bomb_count"`
	LandlordPlays int `json:"landlord_plays"`
	FarmerPlays   int `json:"farmer_plays"`

	// 出牌
	CurrentPlayer     int                 `json:"current_player"`
	LastPlayedCards   []protocol.CardInfo `json:"last_played_cards"`
	LastPlayerIdx     int                 `json:"last_player_idx"`
	ConsecutivePasses int                 `json:"consecutive_passes"`

	// 计时器状态
	TimerKind      string `json:"timer_kind"` // none / bid / play
	TimerRemaining int64  `json:"timer_remaining_ms"`
	TurnPaused     bool   `json:"turn_paused"`
}

// SnapshotPlayer 快照中的玩家
type SnapshotPlayer struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	Seat       int                 `json:"seat"`
	IsBot      bool                `json:"is_bot"`
	IsLandlord bool                `json:"is_landlord"`
	IsOffline  bool                `json:"is_offline"`
	Afk        bool                `json:"afk,omitempty"`
	Hand       []protocol.CardInfo `json:"hand"`
}

// snapshot 导出当前全量状态（仅 Actor 内调用）
func (gs *Session) snapshot() *Snapshot {
	phase := "bidding"
	switch gs.phase {
	case PhaseDoubling:
		phase = "doubling"
	case PhasePlaying:
		phase = "playing"
	case PhaseEnded, PhaseInit:
		// Init/Ended 不做快照恢复
		phase = ""
	}

	players := make([]SnapshotPlayer, len(gs.players))
	for i, p := range gs.players {
		players[i] = SnapshotPlayer{
			ID:         p.ID,
			Name:       p.Name,
			Seat:       p.Seat,
			IsBot:      p.IsBot,
			IsLandlord: p.IsLandlord,
			IsOffline:  p.IsOffline,
			Afk:        p.Afk,
			Hand:       msg.CardsToInfos(p.Hand),
		}
	}

	timerKind := "none"
	switch gs.timerKind {
	case timerBid:
		timerKind = "bid"
	case timerPlay:
		timerKind = "play"
	case timerAutoPass:
		timerKind = "auto_pass"
	}

	var remaining int64
	switch {
	case gs.turnPaused:
		remaining = gs.pausedRest.Milliseconds()
	case gs.timer != nil:
		remaining = gs.timeLeft().Milliseconds()
	}

	return &Snapshot{
		RoomCode:          gs.roomCode,
		Phase:             phase,
		Players:           players,
		BottomCards:       msg.CardsToInfos(gs.bottomCards),
		CurrentBidder:     gs.currentBidder,
		HighBid:           gs.highBid,
		HighBidder:        gs.highBidder,
		BidActed:          gs.bidActed,
		DoubleActed:       gs.doubleActed,
		BidMultiplier:     gs.bidMultiplier,
		RedealCount:       gs.redealCount,
		BombCount:         gs.bombCount,
		LandlordPlays:     gs.landlordPlays,
		FarmerPlays:       gs.farmerPlays,
		CurrentPlayer:     gs.currentPlayer,
		LastPlayedCards:   msg.CardsToInfos(gs.lastPlayedCards),
		LastPlayerIdx:     gs.lastPlayerIdx,
		ConsecutivePasses: gs.consecutivePasses,
		TimerKind:         timerKind,
		TimerRemaining:    remaining,
		TurnPaused:        gs.turnPaused,
	}
}

// RestoreFromSnapshot 从快照反向构造 Session（未启动）。
// Run 启动后按快照阶段恢复回合通知与计时器。
func RestoreFromSnapshot(snap *Snapshot, bc Broadcaster, sink ResultSink, to Timeouts) (*Session, error) {
	if snap == nil || snap.Phase == "" {
		return nil, errors.New("快照为空或不可恢复")
	}
	if len(snap.Players) != 3 {
		return nil, fmt.Errorf("快照玩家数非法: %d", len(snap.Players))
	}

	players := make([]*Player, len(snap.Players))
	for i, sp := range snap.Players {
		players[i] = &Player{
			ID:         sp.ID,
			Name:       sp.Name,
			Seat:       sp.Seat,
			IsBot:      sp.IsBot,
			IsLandlord: sp.IsLandlord,
			IsOffline:  sp.IsOffline,
			Afk:        sp.Afk,
			Hand:       msg.InfosToCards(sp.Hand),
		}
	}

	gs := New(snap.RoomCode, players, bc, sink, to)
	gs.restored = snap

	// 还原上家牌型
	if len(snap.LastPlayedCards) > 0 {
		hand, err := rule.ParseHand(msg.InfosToCards(snap.LastPlayedCards))
		if err != nil {
			return nil, fmt.Errorf("快照上家牌型非法: %w", err)
		}
		gs.restoredLastHand = hand
	}
	return gs, nil
}

// applySnapshot 在 Run 启动时应用快照（仅 Actor 内调用）
func (gs *Session) applySnapshot(snap *Snapshot) {
	// 恢复局按恢复时刻重新计时：endGame 生成复盘报告以 gameStartTime 非零为前提，
	// 且报告时长从恢复时刻起算（恢复前的决策日志不随快照持久化）
	gs.gameStartTime = time.Now()
	gs.bottomCards = msg.InfosToCards(snap.BottomCards)
	gs.currentBidder = snap.CurrentBidder
	gs.highBid = snap.HighBid
	gs.highBidder = snap.HighBidder
	gs.bidActed = snap.BidActed
	gs.doubleActed = snap.DoubleActed
	gs.bidMultiplier = snap.BidMultiplier
	gs.redealCount = snap.RedealCount
	gs.bombCount = snap.BombCount
	gs.landlordPlays = snap.LandlordPlays
	gs.farmerPlays = snap.FarmerPlays
	gs.currentPlayer = snap.CurrentPlayer
	gs.lastPlayedCards = msg.InfosToCards(snap.LastPlayedCards)
	gs.lastPlayedHand = gs.restoredLastHand
	gs.lastPlayerIdx = snap.LastPlayerIdx
	gs.consecutivePasses = snap.ConsecutivePasses

	switch snap.Phase {
	case "bidding":
		gs.phase = PhaseBidding
		gs.notifyBidTurn()
	case "doubling":
		gs.phase = PhaseDoubling
		gs.notifyDoubleTurn()
	case "playing":
		gs.phase = PhasePlaying
		gs.notifyPlayTurn()
	}

	// 恢复时以快照剩余时间覆盖新启动的全时长计时器
	if snap.TimerRemaining > 0 && gs.timer != nil {
		rest := time.Duration(snap.TimerRemaining) * time.Millisecond
		gs.timerStartedAt = timerNow()
		gs.turnTotal = rest
		stopTimer(gs.timer)
		gs.timer = waitAfter(rest)
	}
}
