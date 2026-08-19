package session

import (
	"errors"
	"fmt"
	"time"

	"xgames/internal/games/gomoku/msg"
	"xgames/internal/games/gomoku/rule"
	"xgames/internal/platform/protocol"
)

// 挂机（AFK）与离线托管、状态 DTO、快照。

// afkActDelay 挂机后决策前的反应延迟（拟人，也留出取消挂机的窗口）
const afkActDelay = 1 * time.Second

// SetAfk 挂机 / 取消挂机（消息入口调用）
func (gs *Session) SetAfk(playerID string, afk bool) error {
	reply := make(chan error, 1)
	if !gs.call(afkEvent{playerID: playerID, afk: afk, reply: reply}) {
		return ErrSessionDown
	}
	return <-reply
}

// handleAfk 处理挂机切换（仅 Actor 内调用）
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
		return nil // 机器人本就自动决策，无挂机概念；重复请求幂等
	}

	player.Afk = afk
	gs.broadcastAfk(player, afk)
	gs.logger.Info("挂机状态变更", "room", gs.roomCode, "player", player.Name, "afk", afk)

	// 挂机状态变化时恰为其回合 → 重置计时（挂机加速，取消挂机恢复完整时长）
	if gs.isCurrentTurnPlayer(idx) && gs.timer != nil {
		if afk {
			gs.startTurnTimer(afkActDelay)
		} else {
			gs.startTurnTimer(gs.timeouts.Turn)
		}
	}
	return nil
}

// turnDelayFor 当前决策玩家的回合计时时长：挂机时快速行动，否则完整时长
func (gs *Session) turnDelayFor(idx int) time.Duration {
	if gs.players[idx].Afk {
		return afkActDelay
	}
	return gs.timeouts.Turn
}

// broadcastAfk 广播挂机状态变更
func (gs *Session) broadcastAfk(player *Player, afk bool) {
	gs.broadcast(msg.MsgGkAfkChanged, msg.GkAfkChangedPayload{
		PlayerID: player.ID,
		Afk:      afk,
	})
}

// enterAfkOnTimeout 在线玩家回合超时：自动转为挂机（之后才允许机器人策略接管）
func (gs *Session) enterAfkOnTimeout(idx int) {
	p := gs.players[idx]
	if p.IsBot || p.Afk || p.IsOffline {
		return
	}
	p.Afk = true
	gs.logger.Info("回合超时，自动转为挂机", "room", gs.roomCode, "player", p.Name)
	gs.broadcastAfk(p, true)
}

// exitAfkOnAction 真人主动行动 → 自动取消挂机。
// 注意：挂机托管走 gs.handleMove 直调（不经事件队列），不会被此逻辑误取消。
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

// --- 离线暂停 / 恢复 ---

// handleOffline 玩家掉线：若是其回合则暂停计时，启动离线等待计时器
func (gs *Session) handleOffline(playerID string) {
	idx := gs.playerIdx(playerID)
	if idx == -1 {
		return
	}
	gs.players[idx].IsOffline = true

	if !gs.isCurrentTurnPlayer(idx) || gs.timer == nil {
		return
	}

	// 暂停回合计时，记录剩余时间
	gs.pausedRest = gs.timeLeft()
	stopTimer(gs.timer)
	gs.timer = nil
	gs.turnPaused = true

	// 取消上一个离线计时器（避免多玩家同时离线时旧计时器泄漏并误触发）
	stopTimer(gs.offlineTimer)
	gs.offlineTimer = nil
	gs.offlineIdx = idx
	gs.offlineTimer = waitAfter(gs.timeouts.OfflineWait)

	gs.logger.Info("玩家离线，暂停计时", "room", gs.roomCode,
		"player", gs.players[idx].Name, "remaining", gs.pausedRest)
}

// handleOnline 玩家重连：取消离线等待，恢复其回合计时，并自动退出挂机
func (gs *Session) handleOnline(playerID string) {
	idx := gs.playerIdx(playerID)
	if idx == -1 {
		return
	}
	gs.players[idx].IsOffline = false

	// 重连即回归手动控制：离线期间的托管（挂机）自动取消
	if p := gs.players[idx]; !p.IsBot && p.Afk {
		p.Afk = false
		gs.broadcastAfk(p, false)
	}

	// 取消离线等待计时器
	if gs.offlineTimer != nil && gs.offlineIdx == idx {
		stopTimer(gs.offlineTimer)
		gs.offlineTimer = nil
	}

	if !gs.turnPaused || !gs.isCurrentTurnPlayer(idx) {
		return
	}

	// 恢复回合计时
	rest := gs.pausedRest
	gs.turnPaused = false
	gs.pausedRest = 0
	if rest <= 0 {
		// 剩余时间耗尽，立即托管
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

// handleOfflineTimeout 离线等待超时：仍离线且仍是其回合 → 转挂机并自动落子
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

	// 离线托管等价于挂机（重连后自动取消）
	if !player.Afk {
		player.Afk = true
		gs.broadcastAfk(player, true)
	}

	gs.autoplayFor(idx)
}

// --- 状态 DTO ---

// gameStateFor 生成玩家视角的对局状态 DTO（重连一次性还原棋盘）
func (gs *Session) gameStateFor(playerID string) *msg.GkGameStateDTO {
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

	myColor := rule.Empty
	if idx := gs.playerIdx(playerID); idx >= 0 {
		myColor = rule.ColorOfSeat(gs.players[idx].Seat)
	}

	return &msg.GkGameStateDTO{
		Phase:       phase,
		Players:     players,
		Board:       append([]int(nil), gs.board...),
		CurrentTurn: currentTurn,
		MyColor:     myColor,
		LastRow:     gs.lastRow,
		LastCol:     gs.lastCol,
		MoveNumber:  gs.moveNumber,
	}
}

// --- 快照 ---

// Snapshot GameSession 全量序列化结构（崩溃恢复用）
type Snapshot struct {
	RoomCode string `json:"room_code"`
	Phase    string `json:"phase"` // playing

	Players []SnapshotPlayer `json:"players"`

	Board         []int `json:"board"`
	CurrentPlayer int   `json:"current_player"`
	MoveNumber    int   `json:"move_number"`
	LastRow       int   `json:"last_row"`
	LastCol       int   `json:"last_col"`

	// 计时器状态
	TimerRemaining int64 `json:"timer_remaining_ms"`
	TurnPaused     bool  `json:"turn_paused"`
}

// SnapshotPlayer 快照中的玩家
type SnapshotPlayer struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Seat      int    `json:"seat"`
	IsBot     bool   `json:"is_bot"`
	IsOffline bool   `json:"is_offline"`
	Afk       bool   `json:"afk,omitempty"`
}

// snapshot 导出当前全量状态（仅 Actor 内调用）
func (gs *Session) snapshot() *Snapshot {
	phase := "playing"
	if gs.phase != PhasePlaying {
		// Init/Ended 不做快照恢复
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
		LastRow:        gs.lastRow,
		LastCol:        gs.lastCol,
		TimerRemaining: remaining,
		TurnPaused:     gs.turnPaused,
	}
}

// RestoreFromSnapshot 从快照反向构造 Session（未启动）。
// Run 启动后按快照恢复回合通知与计时器。
func RestoreFromSnapshot(snap *Snapshot, bc Broadcaster, sink ResultSink, to Timeouts) (*Session, error) {
	if snap == nil || snap.Phase == "" {
		return nil, errors.New("快照为空或不可恢复")
	}
	if len(snap.Players) != 2 {
		return nil, fmt.Errorf("快照玩家数非法: %d", len(snap.Players))
	}
	if len(snap.Board) != rule.Size*rule.Size {
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

// applySnapshot 在 Run 启动时应用快照（仅 Actor 内调用）
func (gs *Session) applySnapshot(snap *Snapshot) {
	// 恢复局按恢复时刻重新计时：endGame 生成复盘报告以 gameStartTime 非零为前提
	gs.gameStartTime = time.Now()
	gs.phase = PhasePlaying
	gs.board = append([]int(nil), snap.Board...)
	gs.currentPlayer = snap.CurrentPlayer
	gs.moveNumber = snap.MoveNumber
	gs.lastRow, gs.lastCol = snap.LastRow, snap.LastCol

	gs.notifyTurn()

	// 恢复时以快照剩余时间覆盖新启动的全时长计时器
	if snap.TimerRemaining > 0 && gs.timer != nil {
		rest := time.Duration(snap.TimerRemaining) * time.Millisecond
		gs.timerStartedAt = timerNow()
		gs.turnTotal = rest
		stopTimer(gs.timer)
		gs.timer = waitAfter(rest)
	}
}
