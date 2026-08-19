package session

import "time"

// minActionGap 语音节奏预算（松弛画音同步，见 docs/architecture.md §10）：托管/bot 自动动作的播报
// 距上一动作播报的最小间隔。客户端采用"画面即时走、语音只保最新"策略（不挂起画面），
// 本预算用于限定托管节奏上限、使单拍间隔不致于短到频繁丢弃语音。
// 松弛模式可调；未来服务端下发音频（时长可知）时升级为严格同步。
var minActionGap = 1500 * time.Millisecond

// SetMinActionGapForTest 覆盖语音节奏预算（仅限测试注入，返回恢复函数）。
// 无 UI 的自动化测试传 0（或极小值）关闭节奏等待，避免 1.5s/拍拖慢测试；生产代码不得调用。
func SetMinActionGapForTest(d time.Duration) (restore func()) {
	old := minActionGap
	minActionGap = d
	return func() { minActionGap = old }
}

// autoActionEvent 语音节奏预算到期事件（gen 与当前代次不符则丢弃）
type autoActionEvent struct{ gen int }

// pendingAutoAction 挂起中的托管/bot 自动动作（语音节奏预算等待中）。
// 与 waitingForClaim 互斥：挂起期间无碰/杠/胡询问在进行。
type pendingAutoAction struct {
	gen       int
	playerIdx int
	exec      func() // 预算到期后在 Actor 内执行（闭包自带状态再校验）
	cancel    func() // 等待期间玩家关闭托管时，转回真人交互处理
}

// scheduleAutoAction 托管/bot 自动动作统一入口：距上一动作播报不足 minActionGap 时
// 延迟执行（松弛画音同步——许可移交等待语音时间预算，决策已在等待窗口内并发完成）。
// 仅在 Actor goroutine 内调用；沿用 startClaimTimer 的"独立 goroutine 定时器 + 事件投递"模式。
func (gs *Session) scheduleAutoAction(playerIdx int, exec, cancel func()) {
	gs.actionGen++
	gen := gs.actionGen

	delay := minActionGap - timerNow().Sub(gs.lastActionAt)
	if delay <= 0 {
		exec()
		return
	}

	gs.pendingAuto = &pendingAutoAction{gen: gen, playerIdx: playerIdx, exec: exec, cancel: cancel}
	go func() {
		timer := waitAfter(delay)
		defer stopTimer(timer)
		select {
		case <-timer.C:
			gs.post(autoActionEvent{gen: gen})
		case <-gs.done:
		}
	}()
}

// firePendingAuto 预算到期：代次匹配且仍在对局中才执行挂起动作
func (gs *Session) firePendingAuto(gen int) {
	if gs.pendingAuto == nil || gs.pendingAuto.gen != gen || gs.phase != PhasePlaying {
		return
	}
	pa := gs.pendingAuto
	gs.pendingAuto = nil // 先清挂起：exec 内可能再排下一个自动动作（如杠后摸牌）
	pa.exec()
}

// cancelPendingAutoFor 玩家关闭托管/重连回手动：取消其挂起中的自动动作，
// actionGen++ 使迟到的到期事件失效，并把局面转回真人交互（cancel 闭包）。
// 机器人没有真人交互可回退，挂起动作照常执行。
func (gs *Session) cancelPendingAutoFor(idx int) {
	if gs.pendingAuto == nil || gs.pendingAuto.playerIdx != idx || gs.players[idx].IsBot {
		return
	}
	gs.actionGen++
	pa := gs.pendingAuto
	gs.pendingAuto = nil
	if pa.cancel != nil {
		pa.cancel()
	}
}

// touchActionAt 记录最近一次带语音的动作广播时刻（节奏预算锚点，仅 Actor 内调用）
func (gs *Session) touchActionAt() { gs.lastActionAt = timerNow() }
