package bot

import (
	"fmt"
	"strings"

	"xgames/internal/games/bot/replay"
	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// UserMoveSnapshot 真人出牌决策快照（session 记录，本包分析器离线重建决策上下文用）
type UserMoveSnapshot struct {
	Hand             []card.Card       // 决策时的手牌（出牌前）
	Chosen           []card.Card       // 实际出的牌；nil 表示过牌
	LastPlayed       rule.ParsedHand   // 上家出牌（新一轮为空）
	RecentPlays      [2]PlayRecord     // 最近两手出牌记录
	MustPlay         bool              // 是否领出（新一轮）
	CanBeat          bool              // 是否有牌可压上家
	IsLandlord       bool
	UpIsLandlord     bool
	DownIsLandlord   bool
	PlayerCounts     [2]int            // [0]=上家, [1]=下家 剩余牌数
	PlayedRankCounts map[card.Rank]int // 全场已出的各点数张数
	PlayedBombs      int
	HasPlayed        bool
	UnbeatenStreak   int
}

// AnalyzeUserDecisions 离线复盘分析：以硬难度人格（无故意犯错）重建每个决策点，
// 用规则引擎求"当时最优出法"，与玩家实际选择对比判定失误。
func AnalyzeUserDecisions(moves []replay.PlayerDecision) []replay.MoveAnalysis {
	analyses := make([]replay.MoveAnalysis, 0, len(moves))
	for _, m := range moves {
		snap, ok := m.Snapshot.(UserMoveSnapshot)
		if !ok || len(snap.Hand) == 0 {
			continue
		}
		analyses = append(analyses, analyzeUserMove(m, snap))
	}
	replay.SortAnalysesByRound(analyses)
	return analyses
}

// analyzeUserMove 分析单次决策
func analyzeUserMove(m replay.PlayerDecision, snap UserMoveSnapshot) replay.MoveAnalysis {
	gctx := buildUserGameContext(snap)
	best := unifiedPlay(gctx)

	a := replay.MoveAnalysis{Round: m.Round, ActualDesc: m.ChosenDesc}
	if best == nil {
		a.BestDesc = "过牌"
	} else {
		a.BestDesc = cardsToDisplayString(best)
	}

	current := minHandCountAfter(&gctx, nil)
	if sameCardsIgnoreOrder(snap.Chosen, best) {
		// 与引擎一致：完全等效；能显著简化局面时标记为亮点
		a.ActualScore = float64(minHandCountAfter(&gctx, snap.Chosen))
		a.BestScore = a.ActualScore
		if best != nil && current-minHandCountAfter(&gctx, best) >= 2 {
			a.Highlight = true
			a.Reason = "与引擎推荐一致，显著简化了手牌结构"
		}
		return a
	}

	// 失误判定：以出牌后最少手数衡量效率损失（手数越少离取胜越近）
	a.ActualScore = float64(minHandCountAfter(&gctx, snap.Chosen))
	a.BestScore = float64(minHandCountAfter(&gctx, best))
	diff := int(a.ActualScore - a.BestScore)
	a.IsMistake = true
	switch {
	case diff >= 2:
		a.Severity = replay.SeverityCritical
	case diff == 1:
		a.Severity = replay.SeverityMajor
	default:
		a.Severity = replay.SeverityMinor
	}
	a.Reason = buildUserMoveReason(&gctx, snap, best, diff)
	return a
}

// buildUserMoveReason 生成失误原因说明（面向玩家）
func buildUserMoveReason(gctx *GameContext, snap UserMoveSnapshot, best []card.Card, diff int) string {
	var parts []string
	if snap.Chosen == nil {
		parts = append(parts, "有牌可压却选择过牌，出牌权拱手让给对手")
	} else if diff >= 1 {
		parts = append(parts, fmt.Sprintf("该出法出完后仍需约 %d 手，比最优解多 %d 手（手数越少离取胜越近）",
			minHandCountAfter(gctx, snap.Chosen), diff))
	} else {
		parts = append(parts, "与最优解效率相同，但细节处理可以更好")
	}
	if tip := buildDDZSituationTip(gctx, snap, best); tip != "" {
		parts = append(parts, tip)
	}
	// 追加引擎对该最优解的局势解读
	if reasoning := generateDDZReasoning(*gctx, best, "", snap.IsLandlord); reasoning != "" {
		parts = append(parts, strings.TrimSuffix(reasoning, "\n"))
	}
	return strings.Join(parts, "；")
}

// buildDDZSituationTip 针对典型失误模式生成简短提示
func buildDDZSituationTip(gctx *GameContext, snap UserMoveSnapshot, best []card.Card) string {
	bestPh, err := rule.ParseHand(best)
	if err != nil {
		return ""
	}
	// 实际选择拆了炸弹/王炸而最优解没有
	if len(snap.Chosen) > 0 && breaksBomb(snap.Hand, snap.Chosen) && !breaksBomb(snap.Hand, best) {
		return "注意：实际出法拆散了炸弹，炸弹应尽量保留作为反制手段"
	}
	// 实际选择用掌权牌当小跟牌而最优解保留了掌权牌
	if len(snap.Chosen) > 0 && powerCardCount(snap.Chosen) > 0 && powerCardCount(best) == 0 {
		return "注意：实际出法消耗了 Q/K/A/2 等掌权牌，掌权牌应留作夺取牌权的关键手"
	}
	// 队友接近走完时应送牌
	if !snap.IsLandlord && bestPh.Type == rule.Single && (gctx.PlayerCounts[0] <= 2 || gctx.PlayerCounts[1] <= 2) {
		return "队友接近走完时，单张出牌应尽量送大牌方便队友过关"
	}
	return ""
}

// buildUserGameContext 从快照重建决策上下文（人格取 hard：无故意犯错的最强基线）
func buildUserGameContext(snap UserMoveSnapshot) GameContext {
	return GameContext{
		IsLandlord:     snap.IsLandlord,
		Hand:           snap.Hand,
		RecentPlays:    snap.RecentPlays,
		MustPlay:       snap.MustPlay,
		CanBeat:        snap.CanBeat,
		PlayerCounts:   snap.PlayerCounts,
		RemainingCards: remainingFromPlayed(snap.PlayedRankCounts),
		PlayedBombs:    snap.PlayedBombs,
		UpIsLandlord:   snap.UpIsLandlord,
		DownIsLandlord: snap.DownIsLandlord,
		HasPlayed:      snap.HasPlayed,
		UnbeatenStreak: snap.UnbeatenStreak,
		Personality:    DefaultPersonalities["hard"],
	}
}

// minHandCountAfter 出完 played 后（nil 表示过牌不动手牌）全手最少手数
func minHandCountAfter(gctx *GameContext, played []card.Card) int {
	counts := subtractCounts(handToCounts(gctx.Hand), handToCounts(played))
	return minHandCountWithCache(gctx, counts)
}

// sameCardsIgnoreOrder 忽略顺序比较两手牌是否相同（多重点数比较）
func sameCardsIgnoreOrder(a, b []card.Card) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	counts := make(map[card.Rank]int, len(a))
	for _, c := range a {
		counts[c.Rank]++
	}
	for _, c := range b {
		counts[c.Rank]--
		if counts[c.Rank] < 0 {
			return false
		}
	}
	for _, n := range counts {
		if n != 0 {
			return false
		}
	}
	return true
}

// remainingFromPlayed 记牌视图：全副牌 − 全场已出牌（含自己手牌）
func remainingFromPlayed(played map[card.Rank]int) map[card.Rank]int {
	m := make(map[card.Rank]int, 16)
	for r := card.Rank3; r <= card.Rank2; r++ {
		m[r] = 4
	}
	m[card.RankBlackJoker] = 1
	m[card.RankRedJoker] = 1
	for r, n := range played {
		m[r] -= n
		if m[r] < 0 {
			m[r] = 0
		}
	}
	return m
}
