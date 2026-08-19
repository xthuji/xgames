package session

import (
	"context"

	"xgames/internal/games/ddz/bot"
	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/msg"
	"xgames/internal/games/ddz/rule"
)

// autoplayFor 托管出牌：
//   - 注入了机器人决策引擎时走规则引擎（全量记牌视图，含农民配合）
//   - 引擎为 nil（未启用机器人）时回退提示系统评分兕底
//   - 农民配合：队友出牌一般不压（让队友走牌）
func (gs *Session) autoplayFor(idx int) {
	if gs.phase != PhasePlaying {
		return
	}
	player := gs.players[idx]
	if len(player.Hand) == 0 {
		return
	}

	if gs.engine != nil {
		gs.autoplayByEngine(idx)
		return
	}

	// 必须出牌（新一轮领出）
	if gs.isMustPlay() {
		gs.aiPlayFromHints(idx, player.Hand, rule.ParsedHand{})
		return
	}

	// 跟牌：队友出牌 → 一般不压（让队友走牌）
	lastPlayer := gs.players[gs.lastPlayerIdx]
	isTeammate := !player.IsLandlord && !lastPlayer.IsLandlord
	if isTeammate {
		gs.pass(idx)
		return
	}

	gs.aiPlayFromHints(idx, player.Hand, gs.lastPlayedHand)
}

// autoplayByEngine 用规则引擎决策托管出牌（真人挂机托管自建全量记牌视图，与专家难度等价）
func (gs *Session) autoplayByEngine(idx int) {
	player := gs.players[idx]
	mustPlay := gs.isMustPlay()

	canBeat := mustPlay
	if !mustPlay {
		canBeat = rule.CanBeatWithHand(player.Hand, gs.lastPlayedHand)
	}

	// 记牌器：全副牌减去已出牌（含自己手牌）
	remaining := make(map[card.Rank]int)
	for r := card.Rank3; r <= card.Rank2; r++ {
		remaining[r] = 4
	}
	remaining[card.RankBlackJoker] = 1
	remaining[card.RankRedJoker] = 1
	for r, n := range gs.playedRankCounts {
		remaining[r] -= n
	}

	up := gs.players[(idx+2)%3]   // 上家
	down := gs.players[(idx+1)%3] // 下家

	gctx := bot.GameContext{
		IsLandlord:     player.IsLandlord,
		Hand:           player.Hand,
		BottomCards:    gs.bottomCards,
		RecentPlays:    gs.recentPlays,
		MustPlay:       mustPlay,
		CanBeat:        canBeat,
		PlayerCounts:   [2]int{len(up.Hand), len(down.Hand)},
		RemainingCards: remaining,
		PlayedBombs:    gs.bombCount,
		UpIsLandlord:   up.IsLandlord,
		DownIsLandlord: down.IsLandlord,
		HasPlayed:      gs.oncePlayed[idx],
		UnbeatenStreak: gs.beatStreaks[idx],
	}

	cards := gs.engine.DecidePlay(context.Background(), player.Name, gctx)
	if cards == nil {
		if mustPlay {
			// 防御兜底：领出必须有牌，避免回合超时循环
			gs.aiPlayFromHints(idx, player.Hand, rule.ParsedHand{})
			return
		}
		gs.pass(idx)
		return
	}
	gs.play(idx, cards)
}

// aiPlayFromHints 使用提示系统选择最优出牌（无引擎时的兜底路径）
func (gs *Session) aiPlayFromHints(idx int, hand []card.Card, target rule.ParsedHand) {
	hints := rule.GenerateHints(hand, target)
	// 选第一个 Score > 0 且真正能压过目标的建议（提示候选可能含跨型建议）
	for _, h := range hints {
		if h.Score <= 0 {
			continue
		}
		if !target.IsEmpty() {
			ph, err := rule.ParseHand(h.Cards)
			if err != nil || !rule.CanBeat(ph, target) {
				continue
			}
		}
		gs.play(idx, h.Cards)
		return
	}
	// 领出回合兜底：必出最小单张，避免回合超时循环；跟牌回合则 pass
	if target.IsEmpty() && len(hand) > 0 {
		gs.play(idx, []card.Card{smallestCardOf(hand)})
		return
	}
	gs.pass(idx)
}

// smallestCardOf 手牌中点数最小的一张（领出兜底）
func smallestCardOf(hand []card.Card) card.Card {
	min := hand[0]
	for _, c := range hand[1:] {
		if c.Rank < min.Rank {
			min = c
		}
	}
	return min
}

// play 执行出牌
func (gs *Session) play(idx int, cards []card.Card) {
	player := gs.players[idx]
	_ = gs.handlePlayCards(player.ID, msg.CardsToInfos(cards))
}

// pass 执行过牌
func (gs *Session) pass(idx int) {
	player := gs.players[idx]
	_ = gs.handlePass(player.ID)
}
