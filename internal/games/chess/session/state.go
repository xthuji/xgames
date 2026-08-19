package session

import (
	"errors"
	"fmt"
	"time"

	"xgames/internal/games/chess/msg"
	"xgames/internal/games/chess/rule"
	"xgames/internal/platform/protocol"
)

const afkActDelay = 1 * time.Second

func (gs *Session) SetAfk(playerID string, afk bool) error {
	reply := make(chan error, 1)
	if !gs.call(afkEvent{playerID: playerID, afk: afk, reply: reply}) {
		return ErrSessionDown
	}
	return <-reply
}

func (gs *Session) handleAfk(playerID string, afk bool) error {
	if gs.phase == PhaseInit || gs.phase == PhaseEnded {
		return ErrGameNotStart
	}
	idx := gs.playerIdx(playerID)
	if idx == -1 {
		return ErrNotYourTurn
	}
	player := gs.players[idx]
	if player.IsBot || player.Afk == afk {
		return nil
	}

	player.Afk = afk
	gs.broadcastAfk(player, afk)
	gs.logger.Info("挂机状态变更", "room", gs.roomCode, "player", player.Name, "afk", afk)

	if gs.isCurrentTurnPlayer(idx) && gs.timer != nil {
		if afk {
			gs.startTurnTimer(afkActDelay)
		} else {
			gs.startTurnTimer(gs.timeouts.Turn)
		}
	}
	return nil
}

func (gs *Session) turnDelayFor(idx int) time.Duration {
	if gs.players[idx].Afk {
		return afkActDelay
	}
	return gs.timeouts.Turn
}

func (gs *Session) broadcastAfk(player *Player, afk bool) {
	gs.broadcast(msg.MsgCcAfkChanged, msg.CcAfkChangedPayload{
		PlayerID: player.ID,
		Afk:      afk,
	})
}

func (gs *Session) enterAfkOnTimeout(idx int) {
	p := gs.players[idx]
	if p.IsBot || p.Afk || p.IsOffline {
		return
	}
	p.Afk = true
	gs.logger.Info("回合超时，自动转为挂机", "room", gs.roomCode, "player", p.Name)
	gs.broadcastAfk(p, true)
}

func (gs *Session) exitAfkOnAction(playerID string) {
	idx := gs.playerIdx(playerID)
	if idx == -1 {
		return
	}
	p := gs.players[idx]
	if p.IsBot || !p.Afk {
		return
	}
	p.Afk = false
	gs.logger.Info("主动行动，取消挂机", "room", gs.roomCode, "player", p.Name)
	gs.broadcastAfk(p, false)
}

// --- 离线 ---

func (gs *Session) handleOffline(playerID string) {
	idx := gs.playerIdx(playerID)
	if idx == -1 {
		return
	}
	gs.players[idx].IsOffline = true

	if !gs.isCurrentTurnPlayer(idx) || gs.timer == nil {
		return
	}

	gs.pausedRest = gs.timeLeft()
	stopTimer(gs.timer)
	gs.timer = nil
	gs.turnPaused = true

	stopTimer(gs.offlineTimer)
	gs.offlineIdx = idx
	gs.offlineTimer = waitAfter(gs.timeouts.OfflineWait)

	gs.logger.Info("玩家离线，暂停计时", "room", gs.roomCode,
		"player", gs.players[idx].Name, "remaining", gs.pausedRest)
}

func (gs *Session) handleOnline(playerID string) {
	idx := gs.playerIdx(playerID)
	if idx == -1 {
		return
	}
	gs.players[idx].IsOffline = false

	if p := gs.players[idx]; !p.IsBot && p.Afk {
		p.Afk = false
		gs.broadcastAfk(p, false)
	}

	if gs.offlineTimer != nil && gs.offlineIdx == idx {
		stopTimer(gs.offlineTimer)
		gs.offlineTimer = nil
	}

	if !gs.turnPaused || !gs.isCurrentTurnPlayer(idx) {
		return
	}

	rest := gs.pausedRest
	gs.turnPaused = false
	gs.pausedRest = 0
	if rest <= 0 {
		gs.handleTurnTimeout()
		return
	}

	gs.timerStartedAt = timerNow()
	gs.turnTotal = rest
	stopTimer(gs.timer)
	gs.timer = waitAfter(rest)
	gs.logger.Info("玩家重连，恢复计时", "room", gs.roomCode,
		"player", gs.players[idx].Name, "remaining", rest)
}

func (gs *Session) handleOfflineTimeout() {
	idx := gs.offlineIdx
	if idx < 0 || idx >= len(gs.players) {
		return
	}
	player := gs.players[idx]
	if !player.IsOffline || !gs.isCurrentTurnPlayer(idx) {
		return
	}

	gs.logger.Info("玩家离线超时，自动托管", "room", gs.roomCode, "player", player.Name)

	if !player.Afk {
		player.Afk = true
		gs.broadcastAfk(player, true)
	}

	gs.autoplayFor(idx)
}

// --- 状态 DTO ---

func (gs *Session) gameStateFor(playerID string) *msg.CcGameStateDTO {
	phase := "playing"
	if gs.phase == PhaseEnded {
		phase = "ended"
	}

	players := make([]protocol.PlayerInfo, len(gs.players))
	for i, p := range gs.players {
		players[i] = protocol.PlayerInfo{
			ID:     p.ID,
			Name:   p.Name,
			Seat:   p.Seat,
			Ready:  true,
			Online: !p.IsOffline,
			IsBot:  p.IsBot,
			Afk:    p.Afk,
		}
	}

	currentTurn := ""
	if gs.phase == PhasePlaying {
		currentTurn = gs.players[gs.currentPlayer].ID
	}

	myCamp := 0
	if idx := gs.playerIdx(playerID); idx >= 0 {
		myCamp = rule.CampOfSeat(gs.players[idx].Seat)
	}

	redCaptures := append([]int(nil), gs.redCaptures...)
	blackCaptures := append([]int(nil), gs.blackCaptures...)
	if redCaptures == nil {
		redCaptures = []int{}
	}
	if blackCaptures == nil {
		blackCaptures = []int{}
	}

	return &msg.CcGameStateDTO{
		Phase:         phase,
		Players:       players,
		Board:         append([]int(nil), gs.board...),
		CurrentTurn:   currentTurn,
		MyCamp:        myCamp,
		LastFromRow:   gs.lastFromRow,
		LastFromCol:   gs.lastFromCol,
		LastToRow:     gs.lastToRow,
		LastToCol:     gs.lastToCol,
		MoveNumber:    gs.moveNumber,
		RedCaptures:   redCaptures,
		BlackCaptures: blackCaptures,
	}
}

// --- 快照 ---

type Snapshot struct {
	RoomCode string `json:"room_code"`
	Phase    string `json:"phase"`

	Players []SnapshotPlayer `json:"players"`

	Board         []int `json:"board"`
	CurrentPlayer int   `json:"current_player"`
	MoveNumber    int   `json:"move_number"`
	LastFromRow   int   `json:"last_from_row"`
	LastFromCol   int   `json:"last_from_col"`
	LastToRow     int   `json:"last_to_row"`
	LastToCol     int   `json:"last_to_col"`

	RedCaptures   []int `json:"red_captures"`
	BlackCaptures []int `json:"black_captures"`

	// 和棋判定状态（omitempty：老快照恢复后从当前局面重新计数）
	PosHistory    []uint64 `json:"pos_history,omitempty"` // 局面哈希序列（含执子方）
	HalfmoveClock int      `json:"halfmove_clock,omitempty"` // 连续未吃子 ply 数

	TimerRemaining int64 `json:"timer_remaining_ms"`
	TurnPaused     bool  `json:"turn_paused"`
}

type SnapshotPlayer struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Seat      int    `json:"seat"`
	IsBot     bool   `json:"is_bot"`
	IsOffline bool   `json:"is_offline"`
	Afk       bool   `json:"afk,omitempty"`
}

func (gs *Session) snapshot() *Snapshot {
	phase := "playing"
	if gs.phase != PhasePlaying {
		phase = ""
	}

	players := make([]SnapshotPlayer, len(gs.players))
	for i, p := range gs.players {
		players[i] = SnapshotPlayer{
			ID:        p.ID,
			Name:      p.Name,
			Seat:      p.Seat,
			IsBot:     p.IsBot,
			IsOffline: p.IsOffline,
			Afk:       p.Afk,
		}
	}

	var remaining int64
	switch {
	case gs.turnPaused:
		remaining = gs.pausedRest.Milliseconds()
	case gs.timer != nil:
		remaining = gs.timeLeft().Milliseconds()
	}

	return &Snapshot{
		RoomCode:       gs.roomCode,
		Phase:          phase,
		Players:        players,
		Board:          append([]int(nil), gs.board...),
		CurrentPlayer:  gs.currentPlayer,
		MoveNumber:     gs.moveNumber,
		LastFromRow:    gs.lastFromRow,
		LastFromCol:    gs.lastFromCol,
		LastToRow:      gs.lastToRow,
		LastToCol:      gs.lastToCol,
		RedCaptures:    append([]int(nil), gs.redCaptures...),
		BlackCaptures:  append([]int(nil), gs.blackCaptures...),
		PosHistory:     append([]uint64(nil), gs.posHistory...),
		HalfmoveClock:  gs.halfmoveClock,
		TimerRemaining: remaining,
		TurnPaused:     gs.turnPaused,
	}
}

func RestoreFromSnapshot(snap *Snapshot, bc Broadcaster, sink ResultSink, to Timeouts) (*Session, error) {
	if snap == nil || snap.Phase == "" {
		return nil, errors.New("快照为空或不可恢复")
	}
	if len(snap.Players) != 2 {
		return nil, fmt.Errorf("快照玩家数非法: %d", len(snap.Players))
	}
	if len(snap.Board) != rule.Cols*rule.Rows {
		return nil, fmt.Errorf("快照棋盘大小非法: %d", len(snap.Board))
	}

	players := make([]*Player, len(snap.Players))
	for i, sp := range snap.Players {
		players[i] = &Player{
			ID:        sp.ID,
			Name:      sp.Name,
			Seat:      sp.Seat,
			IsBot:     sp.IsBot,
			IsOffline: sp.IsOffline,
			Afk:       sp.Afk,
		}
	}

	gs := New(snap.RoomCode, players, bc, sink, to)
	gs.restored = snap
	return gs, nil
}

func (gs *Session) applySnapshot(snap *Snapshot) {
	// 恢复局按恢复时刻重新计时：endGame 生成复盘报告以 gameStartTime 非零为前提
	gs.gameStartTime = time.Now()
	gs.phase = PhasePlaying
	gs.board = append([]int(nil), snap.Board...)
	gs.currentPlayer = snap.CurrentPlayer
	gs.moveNumber = snap.MoveNumber
	gs.lastFromRow, gs.lastFromCol = snap.LastFromRow, snap.LastFromCol
	gs.lastToRow, gs.lastToCol = snap.LastToRow, snap.LastToCol
	gs.redCaptures = append([]int(nil), snap.RedCaptures...)
	gs.blackCaptures = append([]int(nil), snap.BlackCaptures...)
	restoreDrawState(gs, snap)

	gs.notifyTurn()

	if snap.TimerRemaining > 0 && gs.timer != nil {
		rest := time.Duration(snap.TimerRemaining) * time.Millisecond
		gs.timerStartedAt = timerNow()
		gs.turnTotal = rest
		stopTimer(gs.timer)
		gs.timer = waitAfter(rest)
	}
}

// restoreDrawState 重建和棋判定状态（重复计数 + 自然限着计数）。
// 老快照无 PosHistory 时从当前局面重新开始计数（判和阈值不受影响，仅历史归零）。
func restoreDrawState(gs *Session, snap *Snapshot) {
	gs.halfmoveClock = snap.HalfmoveClock
	gs.posCount = make(map[uint64]int)
	gs.posHistory = nil
	if len(snap.PosHistory) > 0 {
		for _, h := range snap.PosHistory {
			gs.posHistory = append(gs.posHistory, h)
			gs.posCount[h]++
		}
		return
	}
	// 无历史：以当前局面为起点计入重复计数
	gs.recordPosition(rule.CampOfSeat(gs.players[gs.currentPlayer].Seat))
}
