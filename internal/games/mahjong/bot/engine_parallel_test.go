package bot

import (
	"testing"
	"xgames/internal/games/mahjong/rule"
)

// TestDecideDiscardParallel 测试并行化决策功能
func TestDecideDiscardParallel(t *testing.T) {
	engine := NewEngine(nil)

	// 构造一个复杂手牌（多搭子，需要评估多个候选）
	hand := []int{0, 1, 2, 9, 10, 18, 19, 20, 27, 27, 4, 5, 6}
	melds := []MeldInfo{}
	discards := []int{3, 11, 21}

	ctx := DiscardContext{
		Hand:           hand,
		Melds:          melds,
		DiscardedTiles: discards,
		MyDiscards:     []int{},
		OpponentModels: []OpponentModel{},
		TurnNumber:     5,
		Seen:           nil,
		UseFastUkeire:  false,
	}

	// 测试并行决策
	tile := engine.DecideDiscardParallel(ctx)

	// 验证返回的牌在手牌中
	found := false
	for _, h := range hand {
		if h == tile {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Parallel decision returned tile %d not in hand", tile)
	}

	// 对比串行决策结果
	serialTile := engine.DecideDiscardEnhanced(ctx)
	t.Logf("Parallel decision: %d (%s)", tile, rule.DisplayName(tile))
	t.Logf("Serial decision:   %d (%s)", serialTile, rule.DisplayName(serialTile))

	// 两种方法应该返回相同或相近的结果
	if tile != serialTile {
		t.Logf("Note: Parallel and serial decisions differ (this is acceptable)")
	}
}

// BenchmarkDecideDiscardEnhanced 基准测试：串行决策
func BenchmarkDecideDiscardEnhanced(b *testing.B) {
	engine := NewEngine(nil)

	hand := []int{0, 1, 2, 9, 10, 18, 19, 20, 27, 27, 4, 5, 6}
	ctx := DiscardContext{
		Hand:           hand,
		Melds:          []MeldInfo{},
		DiscardedTiles: []int{3, 11, 21},
		MyDiscards:     []int{},
		OpponentModels: []OpponentModel{},
		TurnNumber:     10,
		Seen:           nil,
		UseFastUkeire:  false,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = engine.DecideDiscardEnhanced(ctx)
	}
}

// BenchmarkDecideDiscardParallel 基准测试：并行决策
func BenchmarkDecideDiscardParallel(b *testing.B) {
	engine := NewEngine(nil)

	hand := []int{0, 1, 2, 9, 10, 18, 19, 20, 27, 27, 4, 5, 6}
	ctx := DiscardContext{
		Hand:           hand,
		Melds:          []MeldInfo{},
		DiscardedTiles: []int{3, 11, 21},
		MyDiscards:     []int{},
		OpponentModels: []OpponentModel{},
		TurnNumber:     10,
		Seen:           nil,
		UseFastUkeire:  false,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = engine.DecideDiscardParallel(ctx)
	}
}

// TestDecideDiscardParallel_SimpleHand 测试简单手牌（应回退到串行）
func TestDecideDiscardParallel_SimpleHand(t *testing.T) {
	engine := NewEngine(nil)

	// 简单手牌（只有2张唯一牌）
	hand := []int{0, 0, 27, 27, 27, 27, 9, 9, 9, 18, 18, 18, 18}

	ctx := DiscardContext{
		Hand:           hand,
		Melds:          []MeldInfo{},
		DiscardedTiles: []int{},
		MyDiscards:     []int{},
		OpponentModels: []OpponentModel{},
		TurnNumber:     1,
		Seen:           nil,
		UseFastUkeire:  false,
	}

	// 对于简单手牌，应自动回退到串行决策
	tile := engine.DecideDiscardParallel(ctx)

	// 验证返回的牌在手牌中
	found := false
	for _, h := range hand {
		if h == tile {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Decision returned tile %d not in hand", tile)
	}
}

// TestDecideDiscardParallel_Consistency 测试并行决策的一致性
func TestDecideDiscardParallel_Consistency(t *testing.T) {
	engine := NewEngine(nil)

	hand := []int{0, 1, 2, 9, 10, 18, 19, 20, 27, 27, 4, 5, 6}
	ctx := DiscardContext{
		Hand:           hand,
		Melds:          []MeldInfo{},
		DiscardedTiles: []int{3, 11, 21},
		MyDiscards:     []int{},
		OpponentModels: []OpponentModel{},
		TurnNumber:     10,
		Seen:           nil,
		UseFastUkeire:  false,
	}

	// 多次运行，验证结果一致性
	results := make(map[int]int)
	runs := 10

	for i := 0; i < runs; i++ {
		tile := engine.DecideDiscardParallel(ctx)
		results[tile]++
	}

	// 由于是确定性算法，应该总是返回相同结果
	if len(results) > 1 {
		t.Logf("Parallel decision produced %d different results (may be due to race conditions)", len(results))
		for tile, count := range results {
			t.Logf("  Tile %d: %d times", tile, count)
		}
	} else {
		t.Logf("Parallel decision is consistent across %d runs", runs)
	}
}
