package session

import (
	"time"

	"xgames/internal/games/ddz/msg"
)

// 挂机（AFK）：玩家显式托管开关。
//
// 真人玩家永远不会被当作机器人：在线且未挂机时所有决策由本人做出，
// 机器人策略（autoplayFor）仅在玩家挂机或离线时接管；
// 在线玩家回合超时会自动转为挂机状态（广播 afk_changed 告知全桌），
// 玩家随时可通过取消挂机或主动出牌恢复手动控制。

// afkActDelay 挂机后决策前的反应延迟（拟人，也留出取消挂机的窗口）
const afkActDelay = 1 * time.Second

// SetAfk 挂机 / 取消挂机（WS 层调用）
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

	// 挂机状态变化时恰为其决策回合 → 重置计时（挂机加速，取消挂机恢复完整时长）
	if gs.isCurrentTurnPlayer(idx) && gs.timer != nil {
		if afk {
			gs.startTurnTimer(gs.timerKind, afkActDelay)
		} else {
			gs.startTurnTimer(gs.timerKind, gs.turnDuration())
		}
	}
	return nil
}

// turnDuration 当前阶段的完整回合时长
func (gs *Session) turnDuration() time.Duration {
	if gs.phase == PhasePlaying {
		return gs.timeouts.Turn
	}
	return gs.timeouts.Bid
}

// turnDelayFor 当前决策玩家的回合计时时长：挂机时快速行动，否则完整时长
func (gs *Session) turnDelayFor(idx int) time.Duration {
	if gs.players[idx].Afk {
		return afkActDelay
	}
	return gs.turnDuration()
}

// broadcastAfk 广播挂机状态变更
func (gs *Session) broadcastAfk(player *Player, afk bool) {
	gs.broadcast(msg.MsgAfkChanged, msg.AfkChangedPayload{
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
// 注意：挂机托管走 gs.handlePlayCards/handlePass 直调（不经事件队列），不会被此逻辑误取消。
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
