package session

import (
	"context"
	"fmt"
	"time"

	"xgames/internal/games/bot/replay"
	"xgames/internal/games/chess/bot"
	"xgames/internal/games/chess/msg"
	"xgames/internal/games/chess/rule"
	"xgames/internal/platform/protocol"
)

// naturalLimitPly 自然限着：连续 13 回合（26 ply）双方均未吃子判和
// （《象棋竞赛规则》60 回合基准大幅缩短，休闲对战快速终局，吃子即重置）
const naturalLimitPly = 26

// repeatThreshold 不变作和：同一局面（含执子方）出现 3 次
const repeatThreshold = 3

// startGame 进入对局：0 号座执红先行（仅 Actor 内调用）
func (gs *Session) startGame() {
	gs.phase = PhasePlaying
	gs.gameStartTime = time.Now()
	gs.board = rule.NewBoard()
	gs.currentPlayer = 0
	gs.moveNumber = 1
	gs.lastFromRow, gs.lastFromCol = -1, -1
	gs.lastToRow, gs.lastToCol = -1, -1
	gs.redCaptures = nil
	gs.blackCaptures = nil
	gs.halfmoveClock = 0
	gs.drawRequester = -1
	gs.posCount = make(map[uint64]int)
	gs.recordPosition(rule.CampRed) // 初始局面计入重复计数
	gs.notifyTurn()
}

// recordPosition 记录当前局面哈希（含执子方）到重复计数（仅 Actor 内调用）
func (gs *Session) recordPosition(camp int) {
	h := rule.PositionHash(gs.board, camp)
	gs.posHistory = append(gs.posHistory, h)
	gs.posCount[h]++
}

// handleMove 处理走子（仅 Actor 内调用）
func (gs *Session) handleMove(playerID string, fromRow, fromCol, toRow, toCol int) error {
	if gs.phase != PhasePlaying {
		return ErrGameNotStart
	}
	current := gs.players[gs.currentPlayer]
	if current.ID != playerID {
		return ErrNotYourTurn
	}
	if !rule.InBounds(fromRow, fromCol) || !rule.InBounds(toRow, toCol) {
		return ErrIllegalMove
	}

	piece := gs.board[rule.Idx(fromRow, fromCol)]
	if piece == rule.Empty {
		return ErrIllegalMove
	}

	camp := rule.CampOfSeat(current.Seat)
	if rule.CampOf(piece) != camp {
		return ErrNotYourPiece
	}

	// 校验走法合法性
	legal := rule.LegalMoves(gs.board, fromRow, fromCol)
	found := false
	for _, m := range legal {
		if m.ToRow == toRow && m.ToCol == toCol {
			found = true
			break
		}
	}
	if !found {
		return ErrIllegalMove
	}

	// 全部校验通过才停计时器
	gs.stopTurnTimer()
	gs.drawRequester = -1 // 请求者走子后，未应答的和棋请求自动失效

	// 复盘：在走子前记录真人决策快照（bot 与托管代走不记录）
	gs.recordUserMove(current, camp, fromRow, fromCol, toRow, toCol)

	captured := gs.board[rule.Idx(toRow, toCol)]
	gs.board[rule.Idx(fromRow, fromCol)] = rule.Empty
	gs.board[rule.Idx(toRow, toCol)] = piece

	if captured != rule.Empty {
		if camp == rule.CampRed {
			gs.redCaptures = append(gs.redCaptures, captured)
		} else {
			gs.blackCaptures = append(gs.blackCaptures, captured)
		}
	}

	gs.lastFromRow, gs.lastFromCol = fromRow, fromCol
	gs.lastToRow, gs.lastToCol = toRow, toCol

	gs.broadcast(msg.MsgCcMoveMade, msg.CcMoveMadePayload{
		PlayerID:   playerID,
		PlayerName: current.Name,
		FromRow:    fromRow,
		FromCol:    fromCol,
		ToRow:      toRow,
		ToCol:      toCol,
		Piece:      piece,
		Captured:   captured,
		MoveNumber: gs.moveNumber,
	})

	// 检查对方是否被将死/困毙
	enemyCamp := rule.CampRed + rule.CampBlack - camp
	if rule.IsCheckmate(gs.board, enemyCamp) || rule.IsStalemate(gs.board, enemyCamp) {
		gs.endGame(current)
		return nil
	}

	// 和棋判定（优先级：三次重复不变作和 → 简单和棋局面 → 自然限着）
	gs.halfmoveClock++
	if captured != rule.Empty {
		gs.halfmoveClock = 0 // 吃子重置自然限着计数
	}
	gs.recordPosition(enemyCamp)
	if reason, ok := gs.checkDraw(); ok {
		gs.endGameDraw(reason)
		return nil
	}

	gs.moveNumber++
	gs.currentPlayer = 1 - gs.currentPlayer
	gs.notifyTurn()
	return nil
}

// checkDraw 判定当前局面是否满足和棋条件（仅 Actor 内调用）。
// 调用前提：最新局面已 recordPosition、halfmoveClock 已更新。
func (gs *Session) checkDraw() (rule.DrawReason, bool) {
	last := gs.posHistory[len(gs.posHistory)-1]
	if gs.posCount[last] >= repeatThreshold {
		return rule.DrawRepetition, true
	}
	if rule.InsufficientMaterial(gs.board) {
		return rule.DrawNoMaterial, true
	}
	if gs.halfmoveClock >= naturalLimitPly {
		return rule.DrawNaturalLimit, true
	}
	return "", false
}

// notifyTurn 通知当前玩家走子并启动计时
func (gs *Session) notifyTurn() {
	player := gs.players[gs.currentPlayer]
	camp := rule.CampOfSeat(player.Seat)
	gs.broadcast(msg.MsgCcTurn, msg.CcTurnPayload{
		PlayerID:   player.ID,
		Camp:       camp,
		Timeout:    int(gs.turnDelayFor(gs.currentPlayer).Seconds()),
		MoveNumber: gs.moveNumber,
	})
	gs.startTurnTimer(gs.turnDelayFor(gs.currentPlayer))
}

// handleTurnTimeout 回合超时
func (gs *Session) handleTurnTimeout() {
	gs.enterAfkOnTimeout(gs.currentPlayer)
	gs.autoplayFor(gs.currentPlayer)
}

// autoplayFor 托管走子
func (gs *Session) autoplayFor(idx int) {
	if gs.phase != PhasePlaying || gs.currentPlayer != idx {
		return
	}
	player := gs.players[idx]
	camp := rule.CampOfSeat(player.Seat)

	if gs.engine != nil {
		fr, fc, tr, tc := gs.engine.DecideMove(context.Background(), player.Name, gs.board, camp)
		if rule.InBounds(fr, fc) && rule.InBounds(tr, tc) {
			if err := gs.handleMove(player.ID, fr, fc, tr, tc); err == nil {
				return
			}
		}
		gs.logger.Warn("引擎走子非法，回退兜底", "room", gs.roomCode, "player", player.Name)
	}

	gs.fallbackMove(idx)
}

// fallbackMove 兜底走子：随机选一个合法走法
func (gs *Session) fallbackMove(idx int) {
	player := gs.players[idx]
	camp := rule.CampOfSeat(player.Seat)
	moves := rule.AllLegalMoves(gs.board, camp)
	if len(moves) == 0 {
		gs.endGame(nil)
		return
	}
	m := moves[0]
	_ = gs.handleMove(player.ID, m.FromRow, m.FromCol, m.ToRow, m.ToCol)
}

// endGame 结算（winner=nil 为平局；drawReason 可选判和原因，写入广播）
func (gs *Session) endGame(winner *Player, drawReason ...rule.DrawReason) {
	gs.phase = PhaseEnded
	gs.stopTurnTimer()
	stopTimer(gs.offlineTimer)
	gs.offlineTimer = nil

	// 复盘：为每个有决策记录的真人玩家生成失误分析报告（异步，不阻塞结算）
	if gs.playerLogger != nil && !gs.playerLogger.Empty() && !gs.gameStartTime.IsZero() {
		go gs.generateUserReplayReports(winner)
	}

	scores := make([]protocol.PlayerScore, len(gs.players))
	for i, p := range gs.players {
		score := 0
		if winner != nil {
			if p.ID == winner.ID {
				score = 1
			} else {
				score = -1
			}
		}
		scores[i] = protocol.PlayerScore{PlayerID: p.ID, PlayerName: p.Name, Score: score}
	}

	payload := protocol.GameOverPayload{Scores: scores}
	if winner != nil {
		payload.WinnerID = winner.ID
		payload.WinnerName = winner.Name
	}
	if len(drawReason) > 0 && drawReason[0] != "" {
		payload.DrawReason = string(drawReason[0])
	}
	gs.broadcast(protocol.MsgGameOver, payload)

	if winner != nil {
		gs.logger.Info("对局结束", "room", gs.roomCode, "winner", winner.Name)
	} else {
		gs.logger.Info("对局结束（平局）", "room", gs.roomCode)
	}

	if gs.sink != nil {
		result := GameResult{RoomCode: gs.roomCode, Multiplier: 1}
		for i, s := range scores {
			result.Players = append(result.Players, PlayerResult{
				PlayerID:   s.PlayerID,
				PlayerName: s.PlayerName,
				IsBot:      gs.players[i].IsBot,
				Score:      s.Score,
			})
		}
		gs.sink.OnGameOver(result)
	}

	gs.ended = true
}

// endGameDraw 判和结算：双方得分归零并广播判和原因
func (gs *Session) endGameDraw(reason rule.DrawReason) {
	gs.drawReason = reason
	gs.logger.Info("判和", "room", gs.roomCode, "reason", reason,
		"halfmove_clock", gs.halfmoveClock)
	gs.endGame(nil, reason)
}

// recordUserMove 记录真人玩家的一次走子决策快照（仅 Actor 内调用，走子前调用）
func (gs *Session) recordUserMove(p *Player, camp, fromRow, fromCol, toRow, toCol int) {
	if gs.playerLogger == nil || p.IsBot || p.Afk {
		return
	}
	gs.playerLogger.Record(replay.PlayerDecision{
		Round:      len(gs.playerLogger.DecisionsByPlayer(p.ID)) + 1,
		PlayerID:   p.ID,
		PlayerName: p.Name,
		ChosenDesc: fmt.Sprintf("(%d,%d)→(%d,%d)", fromRow, fromCol, toRow, toCol),
		Timestamp:  time.Now().Unix(),
		Snapshot: bot.UserMoveSnapshot{
			Board:   append([]int(nil), gs.board...),
			Camp:    camp,
			FromRow: fromRow,
			FromCol: fromCol,
			ToRow:   toRow,
			ToCol:   toCol,
		},
	})
}

// generateUserReplayReports 为每个有决策记录的真人玩家生成并保存失误分析报告（在 goroutine 中异步执行）
func (gs *Session) generateUserReplayReports(winner *Player) {
	for _, pid := range gs.playerLogger.PlayerIDs() {
		decisions := gs.playerLogger.DecisionsByPlayer(pid)
		if len(decisions) == 0 {
			continue
		}

		var player *Player
		for _, p := range gs.players {
			if p.ID == pid {
				player = p
				break
			}
		}
		if player == nil {
			continue
		}

		won := winner != nil && winner.ID == pid
		report := replay.BuildUserReport(replay.UserReportOptions{
			GameType:   "chess",
			GameID:     gs.roomCode,
			PlayerID:   pid,
			PlayerName: player.Name,
			WinnerName: winnerName(winner),
			PlayerWon:  won,
			Draw:       winner == nil,
			Analyses:   bot.AnalyzeUserDecisions(decisions),
			Duration:   time.Since(gs.gameStartTime),
		})
		text := replay.FormatUserReplayReport(report)

		path, err := replay.SaveUserReport("chess", gs.roomCode, pid, text)
		if err != nil {
			gs.logger.Error("保存用户复盘报告失败", "room", gs.roomCode, "player", pid, "err", err)
			continue
		}
		gs.logger.Info("用户复盘报告已保存", "file", path)
	}
}

// winnerName 安全取获胜者名称
func winnerName(winner *Player) string {
	if winner == nil {
		return ""
	}
	return winner.Name
}

// handleResign 处理认输（仅 Actor 内调用）
func (gs *Session) handleResign(playerID string) error {
	if gs.phase != PhasePlaying {
		return ErrGameNotStart
	}
	idx := gs.playerIdx(playerID)
	if idx == -1 {
		return ErrGameNotStart
	}
	// 对手获胜
	winner := gs.players[1-idx]
	gs.endGame(winner)
	return nil
}

// handleDrawRequest 处理和棋请求（仅 Actor 内调用）
func (gs *Session) handleDrawRequest(playerID string) error {
	if gs.phase != PhasePlaying {
		return ErrGameNotStart
	}
	idx := gs.playerIdx(playerID)
	if idx == -1 {
		return ErrGameNotStart
	}
	// 必须是当前回合玩家才能请求和棋
	if gs.currentPlayer != idx {
		return ErrDrawNotYourTurn
	}

	requester := gs.players[idx]
	opponent := gs.players[1-idx]

	// 对手是机器人：自动评估是否接受和棋
	if opponent.IsBot && gs.drawEval != nil {
		boardCopy := append([]int(nil), gs.board...)
		opponentCamp := rule.CampOfSeat(opponent.Seat)
		if gs.drawEval(boardCopy, opponentCamp) {
			gs.endGameDraw(rule.DrawAgreement)
		} else {
			// 拒绝：通知请求者
			gs.broadcaster.SendTo(playerID, protocol.NewMessage(msg.MsgCcDrawResult, msg.CcDrawResultPayload{Accepted: false}))
		}
		return nil
	}

	// 对手是人类：发送和棋请求给对手，并记录待处理请求
	gs.drawRequester = idx
	gs.broadcaster.SendTo(opponent.ID, protocol.NewMessage(msg.MsgCcDrawRequested, msg.CcDrawRequestedPayload{
		RequesterID:   requester.ID,
		RequesterName: requester.Name,
	}))
	return nil
}

// handleDrawResponse 处理和棋应答（仅 Actor 内调用）。
// 必须存在待处理请求，且应答者不能是请求者本人，防止自我批准强制和棋。
func (gs *Session) handleDrawResponse(playerID string, accept bool) error {
	if gs.phase != PhasePlaying {
		return ErrGameNotStart
	}
	idx := gs.playerIdx(playerID)
	if idx == -1 {
		return ErrGameNotStart
	}
	if gs.drawRequester == -1 || idx == gs.drawRequester {
		return ErrDrawNoPending
	}

	requester := gs.players[gs.drawRequester]
	gs.drawRequester = -1
	if accept {
		gs.endGameDraw(rule.DrawAgreement)
	} else {
		// 拒绝：通知请求者
		gs.broadcaster.SendTo(requester.ID, protocol.NewMessage(msg.MsgCcDrawResult, msg.CcDrawResultPayload{Accepted: false}))
	}
	return nil
}
