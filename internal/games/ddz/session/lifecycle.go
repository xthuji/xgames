package session

import (
	"encoding/json"
	"math/rand/v2"
	"time"

	"xgames/internal/games/bot/replay"
	"xgames/internal/games/ddz/bot"
	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/msg"
	"xgames/internal/platform/protocol"
)

// startBiddingRound 发牌并进入叫分阶段（仅 Actor 内调用）
func (gs *Session) startBiddingRound() {
	gs.dealNewRound()
	
	// P1: 记录对局开始时间
	gs.gameStartTime = time.Now()

	gs.phase = PhaseBidding
	gs.currentBidder = rand.IntN(3) // 随机首叫
	gs.notifyBidTurn()
}

// dealNewRound 重置本局状态并发牌
func (gs *Session) dealNewRound() {
	gs.highBid = 0
	gs.highBidder = -1
	gs.bidActed = 0
	gs.doubleActed = 0
	gs.bidMultiplier = 1
	gs.bombCount = 0
	gs.landlordPlays = 0
	gs.farmerPlays = 0
	gs.lastPlayedHand = zeroParsedHand()
	gs.lastPlayedCards = nil
	gs.consecutivePasses = 0
	gs.beatStreaks = [3]int{}
	for _, p := range gs.players {
		p.Hand = nil
		p.IsLandlord = false
	}

	deck := card.NewDeck()
	deck.Shuffle()
	gs.deal(deck)
}

// deal 发牌：每人 17 张，剩余 3 张为底牌
func (gs *Session) deal(deck card.Deck) {
	for range 17 {
		for i := range 3 {
			gs.players[i].Hand = append(gs.players[i].Hand, deck[0])
			deck = deck[1:]
		}
	}
	gs.bottomCards = deck

	for _, p := range gs.players {
		sortHandDesc(p.Hand)
	}

	// 各家手牌定向发送（底牌暂不显示）
	for _, p := range gs.players {
		gs.sendTo(p.ID, msg.MsgDealCards, msg.DealCardsPayload{
			Cards:       msg.CardsToInfos(p.Hand),
			BottomCards: make([]protocol.CardInfo, 3),
		})
	}
}

// endGame 结算并广播 game_over
func (gs *Session) endGame(winner *Player) {
	gs.phase = PhaseEnded
	gs.stopTurnTimer()
	stopTimer(gs.offlineTimer)
	gs.offlineTimer = nil

	// 复盘：为每个有决策记录的真人玩家生成失误分析报告（异步，不阻塞结算）
	if gs.playerLogger != nil && !gs.playerLogger.Empty() && !gs.gameStartTime.IsZero() {
		go gs.generateUserReplayReports(winner)
	}

	multiplier := gs.finalMultiplier(winner)
	scores := gs.computeScores(winner, multiplier)

	playerHands := make([]msg.PlayerHand, len(gs.players))
	for i, p := range gs.players {
		playerHands[i] = msg.PlayerHand{
			PlayerID:   p.ID,
			PlayerName: p.Name,
			Cards:      msg.CardsToInfos(p.Hand),
		}
	}

	// 斗地主扩展数据（剩余手牌/倍数）放入平台 game_over 信封的 Extra 字段
	extra, err := json.Marshal(msg.GameOverExtra{PlayerHands: playerHands, Multiplier: multiplier})
	if err != nil {
		gs.logger.Error("结算扩展数据序列化失败", "room", gs.roomCode, "err", err)
		extra = []byte("{}")
	}
	gs.broadcast(protocol.MsgGameOver, protocol.GameOverPayload{
		WinnerID:   winner.ID,
		WinnerName: winner.Name,
		Scores:     scores,
		Extra:      extra,
	})

	gs.logger.Info("对局结束", "room", gs.roomCode,
		"winner", winner.Name, "landlord_win", winner.IsLandlord, "multiplier", multiplier)

	// 结算落库（机器人在应用层过滤）
	if gs.sink != nil {
		result := GameResult{
			RoomCode:    gs.roomCode,
			LandlordID:  gs.players[gs.landlordIdx()].ID,
			LandlordWin: winner.IsLandlord,
			Multiplier:  multiplier,
		}
		for _, s := range scores {
			idx := gs.playerIdx(s.PlayerID)
			result.Players = append(result.Players, PlayerResult{
				PlayerID:   s.PlayerID,
				PlayerName: s.PlayerName,
				IsLandlord: s.IsLandlord,
				IsBot:      idx >= 0 && gs.players[idx].IsBot,
				Score:      s.Score,
			})
		}
		gs.sink.OnGameOver(result)
	}

	gs.ended = true
}

// landlordIdx 地主座位索引（对局进行中或结束后有效）
func (gs *Session) landlordIdx() int {
	for i, p := range gs.players {
		if p.IsLandlord {
			return i
		}
	}
	return gs.highBidder
}

// finalMultiplier 最终倍数：底倍 × 炸弹/王炸 × 春天/反春天
func (gs *Session) finalMultiplier(winner *Player) int {
	mult := max(gs.bidMultiplier, 1)

	for range gs.bombCount {
		mult *= 2
	}

	// 春天：地主获胜且农民一张未出；反春天：农民获胜且地主只出过一手
	switch {
	case winner.IsLandlord && gs.farmerPlays == 0:
		mult *= 2
	case !winner.IsLandlord && gs.landlordPlays == 1:
		mult *= 2
	}
	return mult
}

// computeScores 按最终倍数计算得分（地主独自对抗两名农民）
func (gs *Session) computeScores(winner *Player, mult int) []protocol.PlayerScore {
	landlordWins := winner.IsLandlord
	scores := make([]protocol.PlayerScore, len(gs.players))
	for i, p := range gs.players {
		var score int
		switch {
		case p.IsLandlord && landlordWins:
			score = 2 * mult
		case p.IsLandlord && !landlordWins:
			score = -2 * mult
		case !p.IsLandlord && landlordWins:
			score = -mult
		default:
			score = mult
		}
		scores[i] = protocol.PlayerScore{
			PlayerID:   p.ID,
			PlayerName: p.Name,
			IsLandlord: p.IsLandlord,
			Score:      score,
		}
	}
	return scores
}

// generateUserReplayReports 为每个有决策记录的真人玩家生成并保存失误分析报告（在 goroutine 中异步执行）
func (gs *Session) generateUserReplayReports(winner *Player) {
	landlordName := ""
	for _, p := range gs.players {
		if p.IsLandlord {
			landlordName = p.Name
			break
		}
	}

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

		summary := "身份: 农民"
		if player.IsLandlord {
			summary = "身份: 地主"
		}
		if landlordName != "" {
			summary += "（地主: " + landlordName + "）"
		}

		won := winner != nil && winner.ID == pid
		report := replay.BuildUserReport(replay.UserReportOptions{
			GameType:    "ddz",
			GameID:      gs.roomCode,
			PlayerID:    pid,
			PlayerName:  player.Name,
			WinnerName:  winnerName(winner),
			PlayerWon:   won,
			Analyses:    bot.AnalyzeUserDecisions(decisions),
			Duration:    time.Since(gs.gameStartTime),
			GameSummary: summary,
		})
		text := replay.FormatUserReplayReport(report)

		path, err := replay.SaveUserReport("ddz", gs.roomCode, pid, text)
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

