package session

import (
	"context"
	"fmt"
	"time"

	"xgames/internal/games/bot/replay"
	"xgames/internal/games/gomoku/bot"
	"xgames/internal/games/gomoku/msg"
	"xgames/internal/games/gomoku/rule"
	"xgames/internal/platform/protocol"
)

// startGame 进入对局：0 号座执黑先行（仅 Actor 内调用）
func (gs *Session) startGame() {
	gs.phase = PhasePlaying
	gs.gameStartTime = time.Now()
	gs.board = rule.NewBoard()
	gs.currentPlayer = 0
	gs.moveNumber = 1
	gs.lastRow, gs.lastCol = -1, -1
	gs.drawRequester = -1
	gs.notifyTurn()
}

// handleMove 处理落子（仅 Actor 内调用）
func (gs *Session) handleMove(playerID string, row, col int) error {
	if gs.phase != PhasePlaying {
		return ErrGameNotStart
	}
	current := gs.players[gs.currentPlayer]
	if current.ID != playerID {
		return ErrNotYourTurn
	}
	if !rule.InBounds(row, col) {
		return ErrOutOfBounds
	}
	if gs.board[rule.Idx(row, col)] != rule.Empty {
		return ErrOccupied
	}

	// 全部校验通过才停计时器
	gs.stopTurnTimer()
	gs.drawRequester = -1 // 请求者落子后，未应答的和棋请求自动失效

	color := rule.ColorOfSeat(current.Seat)

	// 复盘：在落子前记录真人决策快照（bot 与托管代下不记录）
	gs.recordUserMove(current, color, row, col)

	gs.board[rule.Idx(row, col)] = color
	gs.lastRow, gs.lastCol = row, col

	gs.broadcast(msg.MsgGkMoveMade, msg.GkMoveMadePayload{
		PlayerID:   playerID,
		PlayerName: current.Name,
		Row:        row,
		Col:        col,
		Color:      color,
		MoveNumber: gs.moveNumber,
	})

	// 连五获胜
	if rule.IsWin(gs.board, row, col) {
		gs.endGame(current, "")
		return nil
	}
	// 棋盘下满判平
	if rule.IsFull(gs.board) {
		gs.endGame(nil, rule.DrawReasonBoardFull)
		return nil
	}
	// 僵局判平：双方均已无连五可能，继续行棋也无法分出胜负
	if rule.IsDead(gs.board) {
		gs.endGame(nil, rule.DrawReasonNoWinPossible)
		return nil
	}

	gs.moveNumber++
	gs.currentPlayer = 1 - gs.currentPlayer
	gs.notifyTurn()
	return nil
}

// notifyTurn 通知当前玩家落子并启动计时（仅 Actor 内调用）
func (gs *Session) notifyTurn() {
	player := gs.players[gs.currentPlayer]
	gs.broadcast(msg.MsgGkTurn, msg.GkTurnPayload{
		PlayerID:   player.ID,
		Timeout:    int(gs.turnDelayFor(gs.currentPlayer).Seconds()),
		MoveNumber: gs.moveNumber,
	})
	gs.startTurnTimer(gs.turnDelayFor(gs.currentPlayer))
}

// handleTurnTimeout 回合超时：先转挂机再由机器人策略代下（真人仅挂机后才被代打）
func (gs *Session) handleTurnTimeout() {
	gs.enterAfkOnTimeout(gs.currentPlayer)
	gs.autoplayFor(gs.currentPlayer)
}

// autoplayFor 托管落子：注入引擎时用引擎决策，否则兜底（避免回合超时循环）
func (gs *Session) autoplayFor(idx int) {
	if gs.phase != PhasePlaying || gs.currentPlayer != idx {
		return
	}
	player := gs.players[idx]
	color := rule.ColorOfSeat(player.Seat)

	if gs.engine != nil {
		row, col := gs.engine.DecideMove(context.Background(), player.Name, gs.board, color)
		if rule.InBounds(row, col) && gs.board[rule.Idx(row, col)] == rule.Empty {
			_ = gs.handleMove(player.ID, row, col)
			return
		}
		gs.logger.Warn("引擎落子非法，回退兜底", "room", gs.roomCode, "player", player.Name, "row", row, "col", col)
	}

	row, col := fallbackMove(gs.board)
	_ = gs.handleMove(player.ID, row, col)
}

// fallbackMove 兜底落子：优先最近一手周边空位，其次中心，最后首个空位
func fallbackMove(board []int) (int, int) {
	if r, c, ok := firstEmptyNear(board); ok {
		return r, c
	}
	mid := rule.Size / 2
	if board[rule.Idx(mid, mid)] == rule.Empty {
		return mid, mid
	}
	for r := range rule.Size {
		for c := range rule.Size {
			if board[rule.Idx(r, c)] == rule.Empty {
				return r, c
			}
		}
	}
	return 0, 0 // 理论不可达（棋盘满时不会走到落子）
}

// firstEmptyNear 距离已有棋子 1 格内的首个空位
func firstEmptyNear(board []int) (int, int, bool) {
	for r := range rule.Size {
		for c := range rule.Size {
			if board[rule.Idx(r, c)] == rule.Empty {
				continue
			}
			for dr := -1; dr <= 1; dr++ {
				for dc := -1; dc <= 1; dc++ {
					nr, nc := r+dr, c+dc
					if rule.InBounds(nr, nc) && board[rule.Idx(nr, nc)] == rule.Empty {
						return nr, nc, true
					}
				}
			}
		}
	}
	return 0, 0, false
}

// endGame 结算并广播 game_over；winner 为 nil 表示平局（drawReason 为判和原因）
func (gs *Session) endGame(winner *Player, drawReason string) {
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

	payload := protocol.GameOverPayload{Scores: scores, DrawReason: drawReason}
	if winner != nil {
		payload.WinnerID = winner.ID
		payload.WinnerName = winner.Name
	}
	gs.broadcast(protocol.MsgGameOver, payload)

	if winner != nil {
		gs.logger.Info("对局结束", "room", gs.roomCode, "winner", winner.Name)
	} else {
		gs.logger.Info("对局结束（平局）", "room", gs.roomCode, "reason", drawReason)
	}

	// 结算落库（机器人在应用层过滤）
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

// recordUserMove 记录真人玩家的一次落子决策快照（仅 Actor 内调用，落子前调用）
func (gs *Session) recordUserMove(p *Player, color, row, col int) {
	if gs.playerLogger == nil || p.IsBot || p.Afk {
		return
	}
	gs.playerLogger.Record(replay.PlayerDecision{
		Round:      len(gs.playerLogger.DecisionsByPlayer(p.ID)) + 1,
		PlayerID:   p.ID,
		PlayerName: p.Name,
		ChosenDesc: fmt.Sprintf("(%d,%d) 落子", row, col),
		Timestamp:  time.Now().Unix(),
		Snapshot: bot.UserMoveSnapshot{
			Board: append([]int(nil), gs.board...),
			Color: color,
			Row:   row,
			Col:   col,
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
			GameType:   "gomoku",
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

		path, err := replay.SaveUserReport("gomoku", gs.roomCode, pid, text)
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
	gs.endGame(winner, "resignation")
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
		if gs.drawEval(boardCopy) {
			gs.endGame(nil, "agreement")
		} else {
			// 拒绝：通知请求者
			gs.broadcaster.SendTo(playerID, protocol.NewMessage(msg.MsgGkDrawResult, msg.GkDrawResultPayload{Accepted: false}))
		}
		return nil
	}

	// 对手是人类：广播和棋请求给对手，并记录待处理请求
	gs.drawRequester = idx
	gs.broadcaster.SendTo(opponent.ID, protocol.NewMessage(msg.MsgGkDrawRequested, msg.GkDrawRequestedPayload{
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
		gs.endGame(nil, "agreement")
	} else {
		// 拒绝：通知请求者
		gs.broadcaster.SendTo(requester.ID, protocol.NewMessage(msg.MsgGkDrawResult, msg.GkDrawResultPayload{Accepted: false}))
	}
	return nil
}
