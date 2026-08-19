package bot

import (
	"fmt"
	"io"
	"log/slog"

	"xgames/internal/games/bot/replay"
	"xgames/internal/games/mahjong/rule"
)

// UserDiscardSnapshot 真人打牌决策快照（session 记录，本包分析器离线重建决策上下文用）
type UserDiscardSnapshot struct {
	Hand           []int      // 决策时的手牌（打牌前，14 张含刚摸的牌）
	ActualTile     int        // 实际打出的牌
	Melds          []MeldInfo // 已鸣牌面子
	DiscardedTiles []int      // 全局牌河
	MyDiscards     []int      // 自己的弃牌序列
	TurnNumber     int        // 当前巡数
	Seen           []int      // 记牌器：各牌可见张数
}

// AnalyzeUserDecisions 离线复盘分析：重建每个打牌决策点的完整上下文，
// 用决策引擎（含记牌器与防守分析）求"当时最优打牌"，与玩家实际选择对比判定失误。
func AnalyzeUserDecisions(moves []replay.PlayerDecision) []replay.MoveAnalysis {
	engine := NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))

	analyses := make([]replay.MoveAnalysis, 0, len(moves))
	for _, m := range moves {
		snap, ok := m.Snapshot.(UserDiscardSnapshot)
		if !ok || len(snap.Hand) == 0 {
			continue
		}
		analyses = append(analyses, analyzeUserDiscard(m, engine, snap))
	}
	replay.SortAnalysesByRound(analyses)
	return analyses
}

// analyzeUserDiscard 分析单次打牌决策
func analyzeUserDiscard(m replay.PlayerDecision, engine *Engine, snap UserDiscardSnapshot) replay.MoveAnalysis {
	best := engine.DecideDiscardEnhanced(DiscardContext{
		Hand:           snap.Hand,
		Melds:          snap.Melds,
		DiscardedTiles: snap.DiscardedTiles,
		MyDiscards:     snap.MyDiscards,
		TurnNumber:     snap.TurnNumber,
		Seen:           snap.Seen,
	})

	tile := snap.ActualTile
	a := replay.MoveAnalysis{
		Round:      m.Round,
		ActualDesc: rule.DisplayName(tile),
		BestDesc:   rule.DisplayName(best),
	}

	// 失误判定：打出后向听数（越接近胡牌越小）
	counts := rule.CountsFromHand(snap.Hand)
	shantenActual := rule.ShantenAfterDiscard(counts, tile)
	shantenBest := rule.ShantenAfterDiscard(counts, best)
	a.ActualScore = float64(shantenActual)
	a.BestScore = float64(shantenBest)

	if tile == best || shantenActual == shantenBest {
		// 同向听：与最优等效（仅牌效率细节差异），显著接近胡牌时标记亮点
		if shantenActual < 2 {
			a.Highlight = true
			a.Reason = fmt.Sprintf("与引擎推荐一致，已%s", tenpaiDesc(shantenActual))
		}
		return a
	}

	a.IsMistake = true
	switch {
	case shantenActual-shantenBest >= 2:
		a.Severity = replay.SeverityCritical
	case shantenActual-shantenBest == 1:
		a.Severity = replay.SeverityMajor
	default:
		a.Severity = replay.SeverityMinor
	}

	var parts []string
	if best != tile {
		parts = append(parts, fmt.Sprintf("打 %s 比实际打的 %s 更优",
			rule.DisplayName(best), rule.DisplayName(tile)))
	}
	if shantenActual < 99 {
		parts = append(parts, fmt.Sprintf("打出后向听数 %d（最优解为 %d，向听数越少离胡牌越近）",
			shantenActual, shantenBest))
	}
	if snap.TurnNumber >= 12 && shantenBest < shantenActual {
		parts = append(parts, "中后巡应优先保持牌效率，无望的搭子与孤张应尽早处理")
	}
	a.Reason = joinReason(parts)
	return a
}

// tenpaiDesc 向听数描述
func tenpaiDesc(shanten int) string {
	switch {
	case shanten < 0:
		return "和牌"
	case shanten == 0:
		return "听牌"
	default:
		return fmt.Sprintf("%d 向听", shanten)
	}
}

// joinReason 拼接原因说明
func joinReason(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "；"
		}
		out += p
	}
	return out
}
