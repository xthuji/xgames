package rule

import (
	"testing"
)

// TestHandBitset_Basic 测试基本功能
func TestHandBitset_Basic(t *testing.T) {
	counts := make([]int, NumTypes)
	counts[0] = 2  // 1万 × 2
	counts[9] = 3  // 1筒 × 3
	counts[27] = 1 // 东 × 1
	
	bs := NewHandBitset(counts)
	
	// 验证计数
	if bs.GetCount(0) != 2 {
		t.Errorf("Expected count[0]=2, got %d", bs.GetCount(0))
	}
	if bs.GetCount(9) != 3 {
		t.Errorf("Expected count[9]=3, got %d", bs.GetCount(9))
	}
	if bs.GetCount(27) != 1 {
		t.Errorf("Expected count[27]=1, got %d", bs.GetCount(27))
	}
	
	// 验证总数
	total := bs.TotalTiles()
	expectedTotal := 6
	if total != expectedTotal {
		t.Errorf("Expected total=%d, got %d", expectedTotal, total)
	}
}

// TestHandBitset_AddRemove 测试添加和移除牌
func TestHandBitset_AddRemove(t *testing.T) {
	bs := NewHandBitset(make([]int, NumTypes))
	
	// 添加牌
	bs.AddTile(0)
	bs.AddTile(0)
	if bs.GetCount(0) != 2 {
		t.Errorf("Expected count[0]=2 after adding twice, got %d", bs.GetCount(0))
	}
	
	// 移除牌
	if !bs.RemoveTile(0) {
		t.Error("RemoveTile should return true when tile exists")
	}
	if bs.GetCount(0) != 1 {
		t.Errorf("Expected count[0]=1 after removing once, got %d", bs.GetCount(0))
	}
	
	// 移除不存在的牌
	bs.SetCount(5, 0)
	if bs.RemoveTile(5) {
		t.Error("RemoveTile should return false when tile doesn't exist")
	}
}

// TestHandBitset_ToCounts 测试转换为计数数组
func TestHandBitset_ToCounts(t *testing.T) {
	original := make([]int, NumTypes)
	original[0] = 2
	original[9] = 3
	original[27] = 1
	
	bs := NewHandBitset(original)
	recovered := bs.ToCounts()
	
	for i := 0; i < NumTypes; i++ {
		if recovered[i] != original[i] {
			t.Errorf("Index %d: expected %d, got %d", i, original[i], recovered[i])
		}
	}
}

// TestHandBitset_HasTaatsuRelation 测试搭子关系检测
func TestHandBitset_HasTaatsuRelation(t *testing.T) {
	counts := make([]int, NumTypes)
	counts[0] = 1 // 1万
	counts[1] = 1 // 2万
	
	bs := NewHandBitset(counts)
	
	// 已有牌应该有关系
	if !bs.HasTaatsuRelation(0) {
		t.Error("Tile 0 should have taatsu relation (already in hand)")
	}
	
	// 相邻牌应该有关系
	if !bs.HasTaatsuRelation(2) {
		t.Error("Tile 2 (3万) should have taatsu relation with tile 0-1")
	}
	
	// 跳搭应该有关系
	if !bs.HasTaatsuRelation(3) {
		t.Error("Tile 3 (4万) should have taatsu relation (jump wait)")
	}
	
	// 无关的牌应该没有关系
	if bs.HasTaatsuRelation(18) {
		t.Error("Tile 18 (1条) should NOT have taatsu relation")
	}
}

// TestHandBitset_GetCandidateTiles 测试候选牌获取
func TestHandBitset_GetCandidateTiles(t *testing.T) {
	counts := make([]int, NumTypes)
	counts[0] = 1 // 1万
	counts[1] = 1 // 2万
	
	bs := NewHandBitset(counts)
	candidates := bs.GetCandidateTiles()
	
	// 候选牌应该在合理范围内（通常 8-12 张）
	if len(candidates) == 0 || len(candidates) > 20 {
		t.Errorf("Expected 8-20 candidates, got %d", len(candidates))
	}
	
	// 应该包含相关的牌
	found := false
	for _, c := range candidates {
		if c == 2 { // 3万
			found = true
			break
		}
	}
	if !found {
		t.Error("Candidates should include tile 2 (3万)")
	}
}

// TestHandBitset_MemoryEfficiency 测试内存效率
func TestHandBitset_MemoryEfficiency(t *testing.T) {
	var bs HandBitset
	bitsetSize := bs.MemorySize()
	arraySize := CountsMemorySize()
	
	t.Logf("HandBitset size: %d bytes", bitsetSize)
	t.Logf("[]int size: %d bytes", arraySize)
	t.Logf("Memory reduction: %.1f%%", float64(arraySize-bitsetSize)/float64(arraySize)*100)
	
	// 位运算版本应该节省至少 90% 内存
	reduction := float64(arraySize-bitsetSize) / float64(arraySize) * 100
	if reduction < 90 {
		t.Errorf("Expected memory reduction >= 90%%, got %.1f%%", reduction)
	}
}

// TestHandBitset_Copy 测试复制功能
func TestHandBitset_Copy(t *testing.T) {
	counts := make([]int, NumTypes)
	counts[0] = 2
	counts[9] = 1
	
	bs := NewHandBitset(counts)
	copy := bs.Copy()
	
	// 修改副本不应影响原件
	copy.AddTile(0)
	
	if bs.GetCount(0) != 2 {
		t.Errorf("Original should not be affected by copy modification")
	}
	if copy.GetCount(0) != 3 {
		t.Errorf("Copy should have count[0]=3, got %d", copy.GetCount(0))
	}
}

// TestHandBitset_Equal 测试相等性比较
func TestHandBitset_Equal(t *testing.T) {
	counts1 := make([]int, NumTypes)
	counts1[0] = 2
	counts1[9] = 1
	
	counts2 := make([]int, NumTypes)
	counts2[0] = 2
	counts2[9] = 1
	
	bs1 := NewHandBitset(counts1)
	bs2 := NewHandBitset(counts2)
	
	if !bs1.Equal(&bs2) {
		t.Error("Two identical hands should be equal")
	}
	
	bs2.AddTile(0)
	if bs1.Equal(&bs2) {
		t.Error("Different hands should not be equal")
	}
}

// BenchmarkHandBitset_GetCount 性能测试：获取计数
func BenchmarkHandBitset_GetCount(b *testing.B) {
	counts := make([]int, NumTypes)
	counts[0] = 2
	counts[9] = 3
	counts[27] = 1
	
	bs := NewHandBitset(counts)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bs.GetCount(i % NumTypes)
	}
}

// BenchmarkHandBitset_AddTile 性能测试：添加牌
func BenchmarkHandBitset_AddTile(b *testing.B) {
	bs := NewHandBitset(make([]int, NumTypes))
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bs.AddTile(i % NumTypes)
	}
}

// BenchmarkHandBitset_CandidateTiles 性能测试：获取候选牌
func BenchmarkHandBitset_CandidateTiles(b *testing.B) {
	counts := make([]int, NumTypes)
	counts[0] = 1
	counts[1] = 1
	counts[9] = 2
	counts[18] = 1
	
	bs := NewHandBitset(counts)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bs.GetCandidateTiles()
	}
}
