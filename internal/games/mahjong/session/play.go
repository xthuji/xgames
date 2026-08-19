package session

import (
	"context"
	"encoding/json"
	"time"

	"xgames/internal/games/bot/replay"
	"xgames/internal/games/mahjong/bot"
	"xgames/internal/games/mahjong/msg"
	"xgames/internal/games/mahjong/rule"
	"xgames/internal/platform/protocol"
)

const claimTimeout = 10 * time.Second

// startGame 开局：洗牌、发牌、庄家先出
func (gs *Session) startGame() {
	gs.phase = PhasePlaying
	gs.gameStartTime = time.Now()
	n := len(gs.players)
	gs.wall = rule.NewWall()
	gs.hands = make([][]int, n)
	gs.melds = make([][]rule.Meld, n)
	gs.discardPools = make([][]int, n)

	// 掷骰定庄：每位玩家掷 1 枚色子（1-6），点数最大者为庄家（先手）；平局取先达到者
	dice := make([]int, n)
	maxDice := 0
	gs.dealer = 0
	for i := range gs.players {
		dice[i] = rule.RollDice()
		if dice[i] > maxDice {
			maxDice, gs.dealer = dice[i], i
		}
	}
	gs.broadcast(msg.MsgMjDice, msg.MjDicePayload{
		Values: dice,
		Dealer: gs.dealer,
	})

	gs.meldNumber = 0

	// 每人发 13 张
	for i := 0; i < n; i++ {
		drawn, rest := rule.DrawFromWall(gs.wall, rule.HandSize)
		gs.hands[i] = drawn
		gs.wall = rest
	}
	gs.tenpaiRounds = make([]int, n)

	// 庄家先摸一张（14 张），否则庄家首回合弃牌后只剩 12 张，永远无法胡牌
	dealerDrawn, rest := rule.DrawFromWall(gs.wall, 1)
	gs.wall = rest
	gs.hands[gs.dealer] = append(gs.hands[gs.dealer], dealerDrawn[0])

	// 通知各玩家手牌（定向）
	for i, p := range gs.players {
		gs.sendTo(p.ID, protocol.MsgGameStart, protocol.GameStartPayload{
			Players: gs.playerInfos(),
		})
		gs.sendHandTo(i)
	}

	gs.broadcast(msg.MsgMjTurn, msg.MjTurnPayload{
		PlayerID:   gs.players[gs.dealer].ID,
		Timeout:    int(gs.turnDelayFor(gs.dealer).Seconds()),
		MeldNumber: 1,
	})
	gs.currentTurn = gs.dealer
	gs.startTurnTimer(gs.turnDelayFor(gs.dealer))
}

// sendHandTo 定向发送手牌给指定玩家
func (gs *Session) sendHandTo(idx int) {
	p := gs.players[idx]
	hand := make([]int, len(gs.hands[idx]))
	copy(hand, gs.hands[idx])
	sortHand(hand)
	gs.sendTo(p.ID, protocol.MsgGameState, protocol.GameStatePayload{
		Available: true,
		State:     mustMarshalHand(hand, gs.melds[idx], len(gs.wall)),
	})
}

func mustMarshalHand(hand []int, melds []rule.Meld, wallRemain int) []byte {
	type handState struct {
		Hand       []int        `json:"hand"`
		Melds      []rule.Meld  `json:"melds"`
		WallRemain int          `json:"wall_remain"`
	}
	data, _ := jsonMarshal(handState{Hand: hand, Melds: melds, WallRemain: wallRemain})
	return data
}

// handleDiscard 处理出牌
func (gs *Session) handleDiscard(playerID string, tile int) error {
	if gs.phase != PhasePlaying {
		return ErrGameNotStart
	}
	if gs.pendingAuto != nil {
		// 语音节奏预算等待窗口（≤1.5s）内无合法动作可达，拒绝脏操作（见 docs/architecture.md §10.3.3）
		return ErrNotAllowed
	}
	idx := gs.playerIdx(playerID)
	if idx == -1 || idx != gs.currentTurn {
		gs.logger.Warn("出牌被拒绝：非当前回合", "room", gs.roomCode,
			"player", playerID, "idx", idx, "currentTurn", gs.currentTurn)
		return ErrNotYourTurn
	}
	if gs.waitingForClaim {
		if gs.claimIsSelfDraw && idx == gs.claimPlayer {
			// 自摸胡询问中直接出牌 = 放弃胡
			gs.waitingForClaim = false
			gs.claimIsSelfDraw = false
			gs.claimSeq++ // 使挂起的询问定时器失效
		} else {
			gs.logger.Warn("出牌被拒绝：正在等待碰/杠/吃/胡响应", "room", gs.roomCode, "player", playerID)
			return ErrNotYourTurn
		}
	}

	// 检查手牌中是否有这张牌
	// 复盘：先在移牌前记录真人打牌决策快照（14 张手牌含待打之牌；bot 与托管代打不记录）
	recPlayer := gs.players[idx]
	if gs.playerLogger != nil && !recPlayer.IsBot && !recPlayer.Afk {
		gs.recordUserDiscard(recPlayer, idx, tile)
	}
	if !gs.removeFromHand(idx, tile) {
		gs.logger.Warn("出牌被拒绝：牌不在手牌中", "room", gs.roomCode,
			"player", playerID, "tile", tile, "hand", gs.hands[idx])
		return ErrInvalidTile
	}

	gs.stopTurnTimer()
	gs.lastDiscard = tile
	gs.lastDiscardFrom = idx
	gs.discardPools[idx] = append(gs.discardPools[idx], tile)
	gs.meldNumber++

	gs.broadcast(msg.MsgMjDiscarded, msg.MjDiscardedPayload{
		PlayerID: playerID,
		Tile:     tile,
	})
	gs.touchActionAt() // 弃牌播报为节奏预算锚点

	// 检查其他玩家是否可以碰/杠/吃/胡
	gs.checkClaimsAfterDiscard(idx, tile)
	return nil
}

// checkClaimsAfterDiscard 弃牌后检查其他玩家的动作
// 优先级：胡 > 杠 > 碰（推倒胡规则：禁止吃牌）
func (gs *Session) checkClaimsAfterDiscard(fromIdx, tile int) {
	gs.resolveClaimsAfter(fromIdx, fromIdx)
}

// resolveClaimsAfter 检查 fromIdx 弃牌后、从 afterIdx 之后（不含）的玩家是否可碰/杠/胡。
// 优先级：胡 > 杠 > 碰；机器人与托管玩家由服务端按机器人策略自动决策，
// 真人玩家向客户端发送询问；无人可动作则推进下家摸牌。
func (gs *Session) resolveClaimsAfter(fromIdx, afterIdx int) {
	n := len(gs.players)
	tile := gs.lastDiscard
	// afterIdx 相对 fromIdx 的环距 +1 为起始偏移（afterIdx == fromIdx 时从下家开始）
	start := (afterIdx-fromIdx+n)%n + 1

	// 先检查胡牌（优先级最高，所有玩家都可胡）
	for d := start; d < n; d++ {
		pidx := (fromIdx + d) % n
		if gs.players[pidx].IsOffline {
			continue
		}
		if rule.CanWinFromDiscardWithMelds(gs.hands[pidx], tile, len(gs.melds[pidx])) {
			if gs.players[pidx].IsBot || gs.players[pidx].Afk {
				// A 拍（弃牌→自动胡）：挂入语音节奏预算，决策已并发完成，许可移交等待播报
				gs.scheduleAutoAction(pidx,
					func() {
						if gs.phase != PhasePlaying || !rule.CanWinFromDiscardWithMelds(gs.hands[pidx], tile, len(gs.melds[pidx])) {
							gs.logger.Warn("挂起自动胡校验失败，跳过", "room", gs.roomCode)
							return
						}
						gs.executeWin(pidx, false)
					},
					func() { gs.askClaim(pidx, tile, false, false, true) })
				return
			}
			gs.askClaim(pidx, tile, false, false, true)
			return
		}
	}

	// 再检查杠（所有玩家都可杠）
	for d := start; d < n; d++ {
		pidx := (fromIdx + d) % n
		if gs.players[pidx].IsOffline {
			continue
		}
		if rule.CanKongFromDiscard(gs.hands[pidx], tile) {
			if gs.players[pidx].IsBot || gs.players[pidx].Afk {
				// 机器人决策是否杠（调度时同步算好并捕获进 exec）
				if gs.botDecideKong(pidx, tile) {
					// A 拍（弃牌→自动明杠）
					gs.scheduleAutoAction(pidx,
						func() {
							if gs.phase != PhasePlaying || !rule.CanKongFromDiscard(gs.hands[pidx], tile) {
								gs.logger.Warn("挂起自动明杠校验失败，跳过", "room", gs.roomCode)
								return
							}
							gs.executeKong(pidx, tile, fromIdx, false)
						},
						func() { gs.askClaim(pidx, tile, false, true, false) })
					return
				}
				continue
			}
			gs.askClaim(pidx, tile, false, true, false)
			return
		}
	}

	// 再检查碰（所有玩家都可碰）
	for d := start; d < n; d++ {
		pidx := (fromIdx + d) % n
		if gs.players[pidx].IsOffline {
			continue
		}
		if rule.CanPong(gs.hands[pidx], tile) {
			if gs.players[pidx].IsBot || gs.players[pidx].Afk {
				if gs.botDecidePong(pidx, tile) {
					// A 拍（弃牌→自动碰）
					gs.scheduleAutoAction(pidx,
						func() {
							if gs.phase != PhasePlaying || !rule.CanPong(gs.hands[pidx], tile) {
								gs.logger.Warn("挂起自动碰校验失败，跳过", "room", gs.roomCode)
								return
							}
							gs.executePong(pidx, tile, fromIdx)
						},
						func() { gs.askClaim(pidx, tile, true, false, false) })
					return
				}
				continue
			}
			gs.askClaim(pidx, tile, true, false, false)
			return
		}
	}

	// 无人可碰/杠/胡，下家摸牌
	gs.advanceToNext(fromIdx)
}

// askClaim 询问真人玩家是否碰/杠/胡
func (gs *Session) askClaim(pidx, tile int, canPong, canKong, canWin bool) {
	gs.waitingForClaim = true
	gs.claimPlayer = pidx
	p := gs.players[pidx]

	gs.sendTo(p.ID, msg.MsgMjActionAvail, msg.MjActionAvailPayload{
		CanPong: canPong,
		CanKong: canKong,
		CanWin:  canWin,
		Tile:    tile,
		Timeout: int(claimTimeout.Seconds()),
	})

	gs.startClaimTimer()
}

// startClaimTimer 启动询问超时定时器（携带询问序号，过期事件会被丢弃）
func (gs *Session) startClaimTimer() {
	gs.claimSeq++
	seq := gs.claimSeq
	go func() {
		timer := time.NewTimer(claimTimeout)
		defer timer.Stop()
		select {
		case <-timer.C:
			gs.post(claimTimeoutEvent{seq: seq})
		case <-gs.done:
		}
	}()
}

// askSelfDrawWin 询问真人玩家是否自摸胡（摸牌/杠后）
func (gs *Session) askSelfDrawWin(pidx, tile int) {
	gs.currentTurn = pidx
	gs.sendTo(gs.players[pidx].ID, msg.MsgMjActionAvail, msg.MjActionAvailPayload{
		CanWin:  true,
		Tile:    tile,
		Timeout: int(claimTimeout.Seconds()),
	})
	gs.waitingForClaim = true
	gs.claimIsSelfDraw = true
	gs.claimPlayer = pidx
	gs.startClaimTimer()
}

// handleClaimTimeout 碰/杠/吃/胡询问超时（seq 与当前询问序号不符则忽略）
func (gs *Session) handleClaimTimeout(seq int) {
	if seq != gs.claimSeq || !gs.waitingForClaim {
		return
	}
	if gs.claimIsSelfDraw {
		pidx := gs.claimPlayer
		gs.waitingForClaim = false
		gs.claimIsSelfDraw = false
		gs.broadcast(msg.MsgMjTurn, msg.MjTurnPayload{
			PlayerID:   gs.players[pidx].ID,
			Timeout:    int(gs.turnDelayFor(pidx).Seconds()),
			MeldNumber: gs.meldNumber,
		})
		gs.startTurnTimer(gs.turnDelayFor(pidx))
		return
	}
	pidx := gs.claimPlayer
	fromIdx := gs.lastDiscardFrom
	gs.waitingForClaim = false

	// 继续检查后续玩家（胡优先，然后杠，然后碰）
	gs.resolveClaimsAfter(fromIdx, pidx)
}

// handlePong 处理碰
func (gs *Session) handlePong(playerID string, tile int) error {
	if gs.phase != PhasePlaying {
		return ErrGameNotStart
	}
	if gs.pendingAuto != nil {
		// 语音节奏预算等待窗口内无合法动作可达，拒绝脏操作
		return ErrNotAllowed
	}
	idx := gs.playerIdx(playerID)
	if idx == -1 {
		return ErrNotYourTurn
	}
	if !gs.waitingForClaim || idx != gs.claimPlayer {
		return ErrNotAllowed
	}
	if tile != gs.lastDiscard {
		return ErrInvalidTile
	}
	if !rule.CanPong(gs.hands[idx], tile) {
		return ErrNotAllowed
	}

	gs.waitingForClaim = false
	gs.claimSeq++ // 使挂起的询问定时器失效
	gs.executePong(idx, tile, gs.lastDiscardFrom)
	return nil
}

// executePong 执行碰
func (gs *Session) executePong(pidx, tile, fromIdx int) {
	gs.removeFromHand(pidx, tile)
	gs.removeFromHand(pidx, tile)

	gs.melds[pidx] = append(gs.melds[pidx], rule.Meld{
		Type: rule.MeldPong,
		Tile: tile,
		From: fromIdx,
	})

	gs.broadcast(msg.MsgMjPongMade, msg.MjPongMadePayload{
		PlayerID: gs.players[pidx].ID,
		Tile:     tile,
		From:     gs.players[fromIdx].ID,
	})
	gs.touchActionAt() // 碰播报为节奏预算锚点

	// 碰后该玩家出牌
	gs.currentTurn = pidx
	gs.updateTenpaiRounds(pidx)
	gs.broadcast(msg.MsgMjTurn, msg.MjTurnPayload{
		PlayerID:   gs.players[pidx].ID,
		Timeout:    int(gs.turnDelayFor(pidx).Seconds()),
		MeldNumber: gs.meldNumber,
	})
	gs.startTurnTimer(gs.turnDelayFor(pidx))
}

// handleKong 处理杠：等待询问响应时为明杠（吃弃牌）；自己回合内为暗杠（4 张）或补杠（已碰 + 1 张）
func (gs *Session) handleKong(playerID string, tile int) error {
	if gs.phase != PhasePlaying {
		return ErrGameNotStart
	}
	if gs.pendingAuto != nil {
		// 语音节奏预算等待窗口内无合法动作可达，拒绝脏操作
		return ErrNotAllowed
	}
	idx := gs.playerIdx(playerID)
	if idx == -1 {
		return ErrNotYourTurn
	}

	if gs.waitingForClaim {
		if idx != gs.claimPlayer {
			return ErrNotAllowed
		}
		if tile != gs.lastDiscard {
			return ErrInvalidTile
		}
		if !rule.CanKongFromDiscard(gs.hands[idx], tile) {
			return ErrNotAllowed
		}
		gs.waitingForClaim = false
		gs.claimSeq++ // 使挂起的询问定时器失效
		gs.executeKong(idx, tile, gs.lastDiscardFrom, false)
		return nil
	}

	// 自杠：必须轮到自己
	if idx != gs.currentTurn {
		return ErrNotYourTurn
	}
	counts := rule.CountsFromHand(gs.hands[idx])
	if counts[tile] == 4 {
		// 暗杠
		gs.executeKong(idx, tile, -1, true)
		return nil
	}
	if counts[tile] >= 1 && gs.hasPongMeld(idx, tile) {
		// 补杠（碰后加杠）
		gs.executeAddKong(idx, tile)
		return nil
	}
	return ErrNotAllowed
}

// executeKong 执行杠（明杠/暗杠）
func (gs *Session) executeKong(pidx, tile, fromIdx int, isAnKong bool) {
	if isAnKong {
		// 暗杠：从手牌移除 4 张相同的
		for i := 0; i < 4; i++ {
			gs.removeFromHand(pidx, tile)
		}
	} else {
		// 明杠：从手牌移除 3 张
		for i := 0; i < 3; i++ {
			gs.removeFromHand(pidx, tile)
		}
	}

	meldType := rule.MeldKong
	if isAnKong {
		meldType = rule.MeldAnKong
	}
	gs.melds[pidx] = append(gs.melds[pidx], rule.Meld{
		Type: meldType,
		Tile: tile,
		From: fromIdx,
	})

	fromID := ""
	if !isAnKong {
		fromID = gs.players[fromIdx].ID
	}

	gs.broadcast(msg.MsgMjKongMade, msg.MjKongMadePayload{
		PlayerID: gs.players[pidx].ID,
		Tile:     tile,
		From:     fromID,
		IsAnKong: isAnKong,
	})
	gs.touchActionAt() // 杠播报为节奏预算锚点

	gs.afterKongDraw(pidx, tile)
}

// executeAddKong 补杠（加杠）：将已碰面子升级为杠，从手牌补入第 4 张
func (gs *Session) executeAddKong(pidx, tile int) {
	gs.removeFromHand(pidx, tile)
	for i := range gs.melds[pidx] {
		if gs.melds[pidx][i].Type == rule.MeldPong && gs.melds[pidx][i].Tile == tile {
			gs.melds[pidx][i].Type = rule.MeldKong
			break
		}
	}

	gs.broadcast(msg.MsgMjKongMade, msg.MjKongMadePayload{
		PlayerID:  gs.players[pidx].ID,
		Tile:      tile,
		IsAddKong: true,
	})
	gs.touchActionAt() // 补杠播报为节奏预算锚点

	gs.afterKongDraw(pidx, tile)
}

// afterKongDraw 杠后摸一张补牌，检查杠上开花自摸胡，否则进入出牌阶段
func (gs *Session) afterKongDraw(pidx, kongTile int) {
	gs.currentTurn = pidx

	if len(gs.wall) > 0 {
		drawn, rest := rule.DrawFromWall(gs.wall, 1)
		gs.wall = rest
		gs.hands[pidx] = append(gs.hands[pidx], drawn[0])
		gs.updateTenpaiRounds(pidx)

		gs.sendTo(gs.players[pidx].ID, msg.MsgMjDraw, msg.MjDrawPayload{
			Tile:       drawn[0],
			WallRemain: len(gs.wall),
		})
	}

	// 检查自摸胡（杠上开花）
	if rule.CanSelfDrawWinWithMelds(gs.hands[pidx], len(gs.melds[pidx])) {
		if gs.players[pidx].IsBot || gs.players[pidx].Afk {
			// C 拍（杠→杠上开花自摸胡）：挂入语音节奏预算
			gs.scheduleAutoAction(pidx,
				func() {
					if gs.phase != PhasePlaying || !rule.CanSelfDrawWinWithMelds(gs.hands[pidx], len(gs.melds[pidx])) {
						gs.logger.Warn("挂起杠上开花校验失败，跳过", "room", gs.roomCode)
						return
					}
					gs.executeWin(pidx, true)
				},
				func() { gs.askSelfDrawWin(pidx, kongTile) })
			return
		}
		gs.askSelfDrawWin(pidx, kongTile)
		return
	}

	// 杠后该玩家出牌
	gs.broadcast(msg.MsgMjTurn, msg.MjTurnPayload{
		PlayerID:   gs.players[pidx].ID,
		Timeout:    int(gs.turnDelayFor(pidx).Seconds()),
		MeldNumber: gs.meldNumber,
	})
	gs.startTurnTimer(gs.turnDelayFor(pidx))
}

// handleWin 处理胡
func (gs *Session) handleWin(playerID string, isSelfDraw bool) error {
	if gs.phase != PhasePlaying {
		return ErrGameNotStart
	}
	if gs.pendingAuto != nil {
		// 语音节奏预算等待窗口内无合法动作可达，拒绝脏操作
		return ErrNotAllowed
	}
	idx := gs.playerIdx(playerID)
	if idx == -1 {
		return ErrNotYourTurn
	}

	if isSelfDraw {
		// 自摸：必须轮到自己且手牌张数符合（14 - 已鸣面子数×3）
		if idx != gs.currentTurn || len(gs.hands[idx]) != rule.HandSize+1-3*len(gs.melds[idx]) {
			return ErrNotAllowed
		}
		if !rule.CanSelfDrawWinWithMelds(gs.hands[idx], len(gs.melds[idx])) {
			return ErrNotAllowed
		}
		gs.stopTurnTimer()
		gs.executeWin(idx, true)
		return nil
	}

	// 点炮：必须在等待碰/杠/吃/胡响应中
	if !gs.waitingForClaim || idx != gs.claimPlayer {
		return ErrNotAllowed
	}
	if !rule.CanWinFromDiscardWithMelds(gs.hands[idx], gs.lastDiscard, len(gs.melds[idx])) {
		return ErrNotAllowed
	}
	gs.waitingForClaim = false
	gs.claimSeq++ // 使挂起的询问定时器失效

	gs.hands[idx] = append(gs.hands[idx], gs.lastDiscard)
	gs.executeWin(idx, false)
	return nil
}

// executeWin 执行胡牌结算
func (gs *Session) executeWin(winnerIdx int, isSelfDraw bool) {
	winner := gs.players[winnerIdx]
	hand := gs.hands[winnerIdx]
	fan := rule.CalcFan(hand, gs.melds[winnerIdx], isSelfDraw)

	gs.phase = PhaseEnded
	gs.stopTurnTimer()
	rule.ClearShantenCache() // 对局结束，清空向听数缓存

	// 复盘：为每个有决策记录的真人玩家生成失误分析报告（异步，不阻塞结算）
	if gs.playerLogger != nil && !gs.playerLogger.Empty() {
		go gs.generateUserReplayReports(gs.players[winnerIdx], false)
	}

	scores := make([]protocol.PlayerScore, len(gs.players))
	for i, p := range gs.players {
		score := 0
		if i == winnerIdx {
			// 腾讯麻将零和计分：自摸=其余各家各付 fan；点炮=仅点炮者支付 fan，
			// 赢家只收同额，保证一局结束后各家增减之和恒为 0
			if isSelfDraw {
				score = fan * (len(gs.players) - 1)
			} else {
				score = fan
			}
		} else {
			if isSelfDraw {
				score = -fan
			} else if i == gs.lastDiscardFrom {
				score = -fan
			}
		}
		scores[i] = protocol.PlayerScore{PlayerID: p.ID, PlayerName: p.Name, Score: score}
	}

	extra, err := json.Marshal(msg.GameOverExtra{
		PlayerHands: gs.buildPlayerHands(),
	})
	if err != nil {
		gs.logger.Error("结算扩展数据序列化失败", "room", gs.roomCode, "err", err)
		extra = []byte("{}")
	}

	payload := protocol.GameOverPayload{
		WinnerID:   winner.ID,
		WinnerName: winner.Name,
		Scores:     scores,
		Extra:      extra,
	}
	gs.broadcast(protocol.MsgGameOver, payload)
	gs.touchActionAt() // 结算播报为节奏预算锚点
	gs.logger.Info("对局结束", "room", gs.roomCode, "winner", winner.Name, "selfDraw", isSelfDraw, "fan", fan)

	if gs.sink != nil {
		result := GameResult{RoomCode: gs.roomCode}
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

// handlePass 处理放弃碰/杠/吃/胡
func (gs *Session) handlePass(playerID string) error {
	if gs.phase != PhasePlaying {
		return ErrGameNotStart
	}
	if gs.pendingAuto != nil {
		// 语音节奏预算等待窗口内无合法动作可达，拒绝脏操作
		return ErrNotAllowed
	}
	idx := gs.playerIdx(playerID)
	if idx == -1 || !gs.waitingForClaim || idx != gs.claimPlayer {
		return ErrNotAllowed
	}

	gs.waitingForClaim = false
	gs.claimSeq++ // 使挂起的询问定时器失效
	if gs.claimIsSelfDraw {
		gs.claimIsSelfDraw = false
		gs.broadcast(msg.MsgMjTurn, msg.MjTurnPayload{
			PlayerID: playerID,
			Timeout:    int(gs.turnDelayFor(idx).Seconds()),
			MeldNumber: gs.meldNumber,
		})
		gs.startTurnTimer(gs.turnDelayFor(idx))
		return nil
	}
	fromIdx := gs.lastDiscardFrom

	// 继续检查后续玩家（胡→杠→碰）
	gs.resolveClaimsAfter(fromIdx, idx)
	return nil
}

// advanceToNext 推进到下一个玩家摸牌出牌
func (gs *Session) advanceToNext(fromIdx int) {
	nextIdx := (fromIdx + 1) % len(gs.players)

	// 检查牌墙
	if len(gs.wall) == 0 {
		gs.handleWallEmpty()
		return
	}

	// 摸牌
	drawn, rest := rule.DrawFromWall(gs.wall, 1)
	gs.wall = rest
	gs.hands[nextIdx] = append(gs.hands[nextIdx], drawn[0])
	gs.updateTenpaiRounds(nextIdx)

	gs.sendTo(gs.players[nextIdx].ID, msg.MsgMjDraw, msg.MjDrawPayload{
		Tile:       drawn[0],
		WallRemain: len(gs.wall),
	})

	// 许可先行置为下家。bot/托管回合把"自摸胡 > 暗杠 > 补杠 > 通知出牌"整段挂入
	// 语音节奏预算（决策在等待窗口内并发完成，许可移交等待播报，B 拍）；
	// 真人保持现状（即时询问自摸胡/通知出牌）
	gs.currentTurn = nextIdx
	if gs.players[nextIdx].IsBot || gs.players[nextIdx].Afk {
		drawnTile := drawn[0]
		gs.scheduleAutoAction(nextIdx,
			func() { // 预算到期：按优先级重新评估执行
				if gs.phase != PhasePlaying || gs.currentTurn != nextIdx {
					gs.logger.Warn("挂起摸牌动作校验失败，跳过", "room", gs.roomCode,
						"player", gs.players[nextIdx].Name)
					return
				}
				if rule.CanSelfDrawWinWithMelds(gs.hands[nextIdx], len(gs.melds[nextIdx])) {
					gs.executeWin(nextIdx, true)
					return
				}
				counts := rule.CountsFromHand(gs.hands[nextIdx])
				if anKongTile := rule.CanSelfKong(counts); anKongTile >= 0 {
					gs.executeKong(nextIdx, anKongTile, -1, true)
					return
				}
				if kongTile := gs.addKongCandidate(nextIdx); kongTile >= 0 {
					gs.executeAddKong(nextIdx, kongTile)
					return
				}
				gs.broadcastTurnFor(nextIdx)
			},
			func() { // 等待期间关闭托管：转回真人交互（暗杠/补杠仍可手动执行）
				if gs.phase != PhasePlaying {
					return
				}
				if rule.CanSelfDrawWinWithMelds(gs.hands[nextIdx], len(gs.melds[nextIdx])) {
					gs.askSelfDrawWin(nextIdx, drawnTile)
					return
				}
				gs.broadcastTurnFor(nextIdx)
			})
		return
	}

	if rule.CanSelfDrawWinWithMelds(gs.hands[nextIdx], len(gs.melds[nextIdx])) {
		gs.askSelfDrawWin(nextIdx, drawn[0])
		return
	}

	// 通知出牌
	gs.broadcastTurnFor(nextIdx)
}

// broadcastTurnFor 广播回合消息并启动回合定时器
func (gs *Session) broadcastTurnFor(idx int) {
	gs.broadcast(msg.MsgMjTurn, msg.MjTurnPayload{
		PlayerID:   gs.players[idx].ID,
		Timeout:    int(gs.turnDelayFor(idx).Seconds()),
		MeldNumber: gs.meldNumber,
	})
	gs.startTurnTimer(gs.turnDelayFor(idx))
}

// handleWallEmpty 牌墙摸完，流局
func (gs *Session) handleWallEmpty() {
	gs.phase = PhaseEnded
	gs.stopTurnTimer()
	rule.ClearShantenCache() // 对局结束，清空向听数缓存

	// 复盘：为每个有决策记录的真人玩家生成失误分析报告（异步，不阻塞结算）
	if gs.playerLogger != nil && !gs.playerLogger.Empty() {
		go gs.generateUserReplayReports(nil, true)
	}

	scores := make([]protocol.PlayerScore, len(gs.players))
	for i, p := range gs.players {
		scores[i] = protocol.PlayerScore{PlayerID: p.ID, PlayerName: p.Name, Score: 0}
	}
	extra, err := json.Marshal(msg.GameOverExtra{
		PlayerHands: gs.buildPlayerHands(),
	})
	if err != nil {
		gs.logger.Error("结算扩展数据序列化失败", "room", gs.roomCode, "err", err)
		extra = []byte("{}")
	}
	gs.broadcast(protocol.MsgGameOver, protocol.GameOverPayload{Scores: scores, Extra: extra})
	gs.broadcast(msg.MsgMjWallEmpty, struct{}{})
	gs.logger.Info("对局结束（流局）", "room", gs.roomCode)

	if gs.sink != nil {
		result := GameResult{RoomCode: gs.roomCode}
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

// buildPlayerHands 构建所有玩家手牌信息（结算时展示）
func (gs *Session) buildPlayerHands() []msg.PlayerHand {
	hands := make([]msg.PlayerHand, len(gs.players))
	for i, p := range gs.players {
		melds := make([]msg.MjMeldDTO, len(gs.melds[i]))
		for j, m := range gs.melds[i] {
			melds[j] = msg.MjMeldDTO{Type: int(m.Type), Tile: m.Tile, From: m.From}
		}
		hand := make([]int, len(gs.hands[i]))
		copy(hand, gs.hands[i])
		sortHand(hand) // 结算弹窗展示排序后的手牌，方便查看
		hands[i] = msg.PlayerHand{
			PlayerID:   p.ID,
			PlayerName: p.Name,
			Hand:       hand,
			Melds:      melds,
		}
	}
	return hands
}

// handleTurnTimeout 出牌超时
func (gs *Session) handleTurnTimeout() {
	if gs.waitingForClaim {
		gs.handleClaimTimeout(gs.claimSeq)
		return
	}
	gs.logger.Warn("回合超时，自动代打", "room", gs.roomCode,
		"player", gs.players[gs.currentTurn].Name)
	gs.enterAfkOnTimeout(gs.currentTurn)
	gs.autoplayFor(gs.currentTurn)
}

// autoplayFor 机器人/托管代打
func (gs *Session) autoplayFor(idx int) {
	if gs.phase != PhasePlaying || gs.currentTurn != idx {
		return
	}
	player := gs.players[idx]

	// 先检查暗杠
	counts := rule.CountsFromHand(gs.hands[idx])
	if anKongTile := rule.CanSelfKong(counts); anKongTile >= 0 {
		gs.executeKong(idx, anKongTile, -1, true)
		return
	}

	// 再检查补杠（已碰面子 + 手牌持有第 4 张）
	if kongTile := gs.addKongCandidate(idx); kongTile >= 0 {
		gs.executeAddKong(idx, kongTile)
		return
	}

	if gs.engine != nil {
		meldInfos := make([]bot.MeldInfo, len(gs.melds[idx]))
		for i, m := range gs.melds[idx] {
			meldInfos[i] = bot.MeldInfo{Tile: m.Tile, Type: int(m.Type)}
		}
		tile := gs.engine.DecideDiscardEnhanced(bot.DiscardContext{
			Hand:           gs.hands[idx],
			Melds:          meldInfos,
			DiscardedTiles: gs.allDiscards(),
			MyDiscards:     gs.discardPools[idx], // 自己打出的牌（现物兜底用）
			TurnNumber:     len(gs.discardPools[idx]),
			Seen:           gs.visibleCounts(idx),
		})
		// 听牌保护：听牌后不随意拆牌，按记牌成胡可能性延迟拆听
		tile = gs.tingGuardDiscard(idx, tile)
		if gs.hasTileInHand(idx, tile) {
			_ = gs.handleDiscard(player.ID, tile)
			return
		}
	}

	if len(gs.hands[idx]) > 0 {
		_ = gs.handleDiscard(player.ID, gs.hands[idx][0])
	}
}

// --- 辅助 ---

// tingGuardDiscard 听牌保护包装：交由引擎 TingGuard 结合记牌视图与听牌持续轮数决策
func (gs *Session) tingGuardDiscard(idx, suggested int) int {
	if gs.engine == nil {
		return suggested
	}
	return gs.engine.TingGuard(gs.hands[idx], len(gs.melds[idx]), gs.visibleCounts(idx), gs.tenpaiRounds[idx], suggested)
}

// visibleCounts 汇总可见牌（各家牌河+面子+自己手牌）的各牌种张数（记牌器）
func (gs *Session) visibleCounts(idx int) []int {
	meldTiles := make([][]int, 0, len(gs.melds))
	for _, melds := range gs.melds {
		tiles := make([]int, 0, len(melds)*4)
		for _, m := range melds {
			n := 3
			if m.Type != rule.MeldPong {
				n = 4 // 杠类面子占 4 张
			}
			for j := 0; j < n; j++ {
				tiles = append(tiles, m.Tile)
			}
		}
		meldTiles = append(meldTiles, tiles)
	}
	return rule.VisibleCounts(gs.discardPools, meldTiles, gs.hands[idx])
}

// allDiscards 汇总全局牌河（所有玩家弃牌序列）
func (gs *Session) allDiscards() []int {
	tiles := make([]int, 0, 64)
	for _, pool := range gs.discardPools {
		tiles = append(tiles, pool...)
	}
	return tiles
}

// updateTenpaiRounds 该玩家完成一次摸牌/碰后更新听牌持续轮数：听牌则累计，否则清零
func (gs *Session) updateTenpaiRounds(idx int) {
	if idx < 0 || idx >= len(gs.hands) {
		return
	}
	if rule.IsTenpaiWithMelds(gs.hands[idx], len(gs.melds[idx])) {
		gs.tenpaiRounds[idx]++
	} else {
		gs.tenpaiRounds[idx] = 0
	}
}

// hasPongMeld 是否已有 tile 的碰面子（补杠前提）
func (gs *Session) hasPongMeld(idx, tile int) bool {
	for _, m := range gs.melds[idx] {
		if m.Type == rule.MeldPong && m.Tile == tile {
			return true
		}
	}
	return false
}

// addKongCandidate 返回可补杠的牌（已碰面子且手牌持有第 4 张），无可补杠返回 -1
func (gs *Session) addKongCandidate(idx int) int {
	for _, m := range gs.melds[idx] {
		if m.Type == rule.MeldPong && gs.hasTileInHand(idx, m.Tile) {
			return m.Tile
		}
	}
	return -1
}

func (gs *Session) removeFromHand(idx, tile int) bool {
	hand := gs.hands[idx]
	for i, t := range hand {
		if t == tile {
			gs.hands[idx] = append(hand[:i], hand[i+1:]...)
			return true
		}
	}
	return false
}

func (gs *Session) hasTileInHand(idx, tile int) bool {
	for _, t := range gs.hands[idx] {
		if t == tile {
			return true
		}
	}
	return false
}

func (gs *Session) botDecidePong(pidx, tile int) bool {
	if gs.engine == nil {
		return true
	}
	player := gs.players[pidx]
	// 托管/兜底路径不携带房间难度，零值配置按引擎既有行为处理
	pong, _ := gs.engine.DecideAction(context.Background(), player.Name, gs.hands[pidx], tile, len(gs.melds[pidx]), bot.DifficultyConfig{})
	return pong
}

func (gs *Session) botDecideKong(pidx, tile int) bool {
	if gs.engine == nil {
		return true
	}
	
	counts := rule.CountsFromHand(gs.hands[pidx])
	meldCount := len(gs.melds[pidx])
	
	// 1. 检查是否已听牌
	isTenpai := rule.IsTenpaiWithMelds(gs.hands[pidx], meldCount)
	if isTenpai {
		// 听牌时杠需慎重：可能破坏听口
		// 检查杠的牌是否是听牌的一部分
		waits := rule.TenpaiWaits(gs.hands[pidx], meldCount)
		isWaitTile := false
		for _, w := range waits {
			if w == tile {
				isWaitTile = true
				break
			}
		}
		
		if isWaitTile {
			// 杠的牌是待牌之一，杠了会破坏听口 → 不杠
			return false
		}
		
		// 不是待牌，但杠后会减少手牌，可能影响听牌质量
		// 检查杠后是否仍听牌
		counts[tile] -= 4 // 暗杠消耗 4 张
		stillTenpai := rule.IsTenpaiWithMelds(gs.hands[pidx], meldCount+1)
		counts[tile] += 4
		
		if !stillTenpai {
			// 杠后会拆听 → 不杠
			return false
		}
		
		// 杠后仍听牌 → 可以杠（改善和牌机会）
		return true
	}
	
	// 2. 未听牌时：评估杠的收益与风险
	// 杠的收益：多摸一张牌，可能杠上开花
	// 杠的风险：副露增多，防守变弱
	
	// 简化决策：计算杠后的向听数改善
	beforeShanten := rule.ShantenNumber(counts)
	counts[tile] -= 4 // 模拟暗杠
	afterShanten := rule.ShantenNumber(counts)
	counts[tile] += 4
	
	if afterShanten < beforeShanten {
		// 杠后向听数改善 → 必杠
		return true
	}
	
	// 向听数不变时，根据巡数判断：
	// 早巡（<8）优先进攻，晚巡（>12）谨慎副露
	if gs.turnOfPlayer(pidx) < 8 {
		return true  // 早巡，积极杠
	} else if gs.turnOfPlayer(pidx) > 12 {
		// 晚巡，检查安全牌余量
		safeTiles := gs.countSafeTiles(pidx)
		if safeTiles >= 4 {
			return true  // 安全牌充足，可以杠
		}
		return false // 安全牌不足，保守不杠
	}
	
	// 中巡默认杠
	return true
}

// turnOfPlayer 获取玩家的当前巡数（已弃牌数）
func (gs *Session) turnOfPlayer(idx int) int {
	if idx < 0 || idx >= len(gs.discardPools) {
		return 0
	}
	return len(gs.discardPools[idx])
}

// countSafeTiles 统计玩家手牌中的安全牌数量（孤张字牌 + 真孤张数牌）
func (gs *Session) countSafeTiles(idx int) int {
	if idx < 0 || idx >= len(gs.hands) {
		return 0
	}
	
	counts := rule.CountsFromHand(gs.hands[idx])
	safeCount := 0
	
	for t := 0; t < rule.NumTypes; t++ {
		if counts[t] == 0 {
			continue
		}
		
		// 孤张字牌 = 绝对安全
		if rule.IsHonor(t) && counts[t] == 1 {
			safeCount++
			continue
		}
		
		// 数牌孤张（无邻居）= 相对安全
		if rule.IsNumber(t) && counts[t] == 1 {
			suit, val := rule.SuitOf(t), rule.ValueOf(t)
			base := suit * rule.NumValues
			hasNeighbor := false
			for dv := -2; dv <= 2; dv++ {
				if dv == 0 {
					continue
				}
				nv := val + dv
				if nv >= 0 && nv < rule.NumValues && counts[base+nv] > 0 {
					hasNeighbor = true
					break
				}
			}
			if !hasNeighbor {
				safeCount++
			}
		}
	}
	
	return safeCount
}

func (gs *Session) playerInfos() []protocol.PlayerInfo {
	infos := make([]protocol.PlayerInfo, len(gs.players))
	for i, p := range gs.players {
		infos[i] = protocol.PlayerInfo{
			ID:     p.ID,
			Name:   p.Name,
			Seat:   p.Seat,
			Ready:  true,
			Online: !p.IsOffline,
			IsBot:  p.IsBot,
			Afk:    p.Afk,
		}
	}
	return infos
}

// sortHand 排序手牌（字牌在数牌之后，同花色按点数）
func sortHand(hand []int) {
	for i := 1; i < len(hand); i++ {
		for j := i; j > 0 && hand[j] < hand[j-1]; j-- {
			hand[j], hand[j-1] = hand[j-1], hand[j]
		}
	}
}

func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// recordUserDiscard 记录真人玩家的一次打牌决策快照（仅 Actor 内调用，移牌前调用）
func (gs *Session) recordUserDiscard(p *Player, idx int, tile int) {
	meldInfos := make([]bot.MeldInfo, len(gs.melds[idx]))
	for i, meld := range gs.melds[idx] {
		meldInfos[i] = bot.MeldInfo{Tile: meld.Tile, Type: int(meld.Type)}
	}

	gs.playerLogger.Record(replay.PlayerDecision{
		Round:      len(gs.playerLogger.DecisionsByPlayer(p.ID)) + 1,
		PlayerID:   p.ID,
		PlayerName: p.Name,
		ChosenDesc: rule.DisplayName(tile),
		Timestamp:  time.Now().Unix(),
		Snapshot: bot.UserDiscardSnapshot{
			Hand:           append([]int(nil), gs.hands[idx]...),
			ActualTile:     tile,
			Melds:          meldInfos,
			DiscardedTiles: gs.allDiscards(),
			MyDiscards:     append([]int(nil), gs.discardPools[idx]...),
			TurnNumber:     len(gs.discardPools[idx]),
			Seen:           gs.visibleCounts(idx),
		},
	})
}

// generateUserReplayReports 为每个有决策记录的真人玩家生成并保存失误分析报告（在 goroutine 中异步执行）
func (gs *Session) generateUserReplayReports(winner *Player, draw bool) {
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

		report := replay.BuildUserReport(replay.UserReportOptions{
			GameType:   "mahjong",
			GameID:     gs.roomCode,
			PlayerID:   pid,
			PlayerName: player.Name,
			WinnerName: winnerNameOf(winner),
			PlayerWon:  winner != nil && winner.ID == pid,
			Draw:       draw,
			Analyses:   bot.AnalyzeUserDecisions(decisions),
			Duration:   time.Since(gs.gameStartTime),
		})
		text := replay.FormatUserReplayReport(report)

		path, err := replay.SaveUserReport("mahjong", gs.roomCode, pid, text)
		if err != nil {
			gs.logger.Error("保存用户复盘报告失败", "room", gs.roomCode, "player", pid, "err", err)
			continue
		}
		gs.logger.Info("用户复盘报告已保存", "file", path)
	}
}

// winnerNameOf 安全取获胜者名称
func winnerNameOf(winner *Player) string {
	if winner == nil {
		return ""
	}
	return winner.Name
}
