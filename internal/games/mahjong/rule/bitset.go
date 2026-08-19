// Package rule 麻将核心规则引擎
// bitset.go: 位运算手牌编码优化，提升内存效率和 CPU 缓存命中率

package rule

// HandBitset 位运算手牌编码
// 使用两个 uint64 存储 34 种牌的计数（每种牌最多 4 张，需要 3 bit）
// 34 × 3bit = 102bit，分为低 64bit（前 21 种牌）和高 64bit（后 13 种牌）
type HandBitset struct {
	lo uint64 // 前 21 种牌（tile 0-20），每牌 3bit
	hi uint64 // 后 13 种牌（tile 21-33），每牌 3bit
}

const (
	bitsPerTile = 3                    // 每种牌占用 3 bit（可表示 0-7）
	tileMask    = 0x7                  // 3bit 掩码 (0b111)
	lowTiles    = 21                   // 低位部分包含的牌种数
	highTiles   = NumTypes - lowTiles  // 高位部分包含的牌种数 (34-21=13)
)

// NewHandBitset 从计数数组创建位运算手牌
func NewHandBitset(counts []int) HandBitset {
	var bs HandBitset
	
	// 编码低位部分（tile 0-20）
	for i := 0; i < lowTiles && i < len(counts); i++ {
		bs.setLow(i, counts[i])
	}
	
	// 编码高位部分（tile 21-33）
	for i := lowTiles; i < NumTypes && i < len(counts); i++ {
		bs.setHigh(i-lowTiles, counts[i])
	}
	
	return bs
}

// GetCount 获取指定牌的计数
func (bs *HandBitset) GetCount(tile int) int {
	if tile < 0 || tile >= NumTypes {
		return 0
	}
	
	if tile < lowTiles {
		return int(bs.getLow(tile))
	}
	return int(bs.getHigh(tile - lowTiles))
}

// SetCount 设置指定牌的计数
func (bs *HandBitset) SetCount(tile int, count int) {
	if tile < 0 || tile >= NumTypes {
		return
	}
	
	if count < 0 {
		count = 0
	} else if count > 4 {
		count = 4
	}
	
	if tile < lowTiles {
		bs.setLow(tile, count)
	} else {
		bs.setHigh(tile-lowTiles, count)
	}
}

// AddTile 添加一张牌
func (bs *HandBitset) AddTile(tile int) {
	count := bs.GetCount(tile)
	if count < 4 {
		bs.SetCount(tile, count+1)
	}
}

// RemoveTile 移除一张牌
func (bs *HandBitset) RemoveTile(tile int) bool {
	count := bs.GetCount(tile)
	if count > 0 {
		bs.SetCount(tile, count-1)
		return true
	}
	return false
}

// TotalTiles 计算手牌总数
func (bs *HandBitset) TotalTiles() int {
	total := 0
	for i := 0; i < NumTypes; i++ {
		total += bs.GetCount(i)
	}
	return total
}

// ToCounts 转换为计数数组（用于兼容现有 API）
func (bs *HandBitset) ToCounts() []int {
	counts := make([]int, NumTypes)
	for i := 0; i < NumTypes; i++ {
		counts[i] = bs.GetCount(i)
	}
	return counts
}

// getLow 获取低位部分的牌计数
func (bs *HandBitset) getLow(index int) uint8 {
	shift := index * bitsPerTile
	return uint8((bs.lo >> shift) & tileMask)
}

// setLow 设置低位部分的牌计数
func (bs *HandBitset) setLow(index int, value int) {
	shift := index * bitsPerTile
	mask := uint64(tileMask) << shift
	bs.lo = (bs.lo &^ mask) | (uint64(value) << shift)
}

// getHigh 获取高位部分的牌计数
func (bs *HandBitset) getHigh(index int) uint8 {
	shift := index * bitsPerTile
	return uint8((bs.hi >> shift) & tileMask)
}

// setHigh 设置高位部分的牌计数
func (bs *HandBitset) setHigh(index int, value int) {
	shift := index * bitsPerTile
	mask := uint64(tileMask) << shift
	bs.hi = (bs.hi &^ mask) | (uint64(value) << shift)
}

// HasTaatsuRelation 检查是否与手牌有搭子关系
// 用于 Ukeire 预筛选，判断某张牌是否值得检查
func (bs *HandBitset) HasTaatsuRelation(tile int) bool {
	count := bs.GetCount(tile)
	
	// 如果手中已有该牌，有关系
	if count > 0 {
		return true
	}
	
	// 如果是字牌且手中没有，无关系
	if IsHonor(tile) {
		return false
	}
	
	// 检查相邻牌（n-2 ~ n+2）
	suit := SuitOf(tile)
	val := ValueOf(tile)
	base := suit * NumValues
	
	for dv := -2; dv <= 2; dv++ {
		if dv == 0 {
			continue
		}
		nv := val + dv
		if nv >= 0 && nv < NumValues {
			neighbor := base + nv
			if bs.GetCount(neighbor) > 0 {
				return true
			}
		}
	}
	
	return false
}

// GetCandidateTiles 获取所有候选进张牌（与手牌有搭子关系的牌）
// 返回的候选牌通常只有 8-12 张，而非全部 34 张
func (bs *HandBitset) GetCandidateTiles() []int {
	candidates := make([]int, 0, 12)
	
	for t := 0; t < NumTypes; t++ {
		if bs.HasTaatsuRelation(t) && bs.GetCount(t) < 4 {
			candidates = append(candidates, t)
		}
	}
	
	return candidates
}

// Copy 复制手牌
func (bs *HandBitset) Copy() HandBitset {
	return HandBitset{lo: bs.lo, hi: bs.hi}
}

// Equal 比较两手牌是否相等
func (bs *HandBitset) Equal(other *HandBitset) bool {
	return bs.lo == other.lo && bs.hi == other.hi
}

// MemorySize 返回内存占用（字节）
func (bs *HandBitset) MemorySize() int {
	return 16 // 2 × uint64
}

// CountsMemorySize 返回传统计数数组的内存占用（用于对比）
func CountsMemorySize() int {
	return NumTypes * 8 // 34 × int64 (假设 int 为 8 字节) = 272 字节
}
