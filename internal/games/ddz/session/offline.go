package session

// 离线暂停/恢复计时与离线托管（行为对齐原 timer.go，机制重写为 channel 模式）。

// isCurrentTurnPlayer 玩家是否为当前回合决策者
func (gs *Session) isCurrentTurnPlayer(idx int) bool {
	return ((gs.phase == PhaseBidding || gs.phase == PhaseDoubling) && gs.currentBidder == idx) ||
		(gs.phase == PhasePlaying && gs.currentPlayer == idx)
}

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

// handleOfflineTimeout 离线等待超时：仍离线且仍是其回合 → 转挂机并自动执行操作
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

	switch gs.phase {
	case PhaseBidding:
		_ = gs.handleBid(player.ID, 0)
	case PhaseDoubling:
		_ = gs.handleDouble(player.ID, false)
	default:
		gs.autoplayFor(idx)
	}
}
