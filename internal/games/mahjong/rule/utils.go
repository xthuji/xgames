package rule

// ContainsInt 检查切片是否包含某值（通用工具函数）
func ContainsInt(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

// CountInSlice 统计某值在切片中出现的次数（通用工具函数）
func CountInSlice(val int, slice []int) int {
	count := 0
	for _, v := range slice {
		if v == val {
			count++
		}
	}
	return count
}

// IsTrueOrphan 判断 tile 是否为真正的孤张：
// - 无对子/刻子（counts[tile] == 1）
// - 同花色无邻牌（val-2 ~ val+2 范围内无其他牌）
// 这样的牌完全没有组成面子的潜力，应优先打出
func IsTrueOrphan(counts []int, tile int) bool {
	if IsHonor(tile) {
		return true // 字牌单张即为孤张
	}
	if counts[tile] != 1 {
		return false // 有对子或刻子，不是孤张
	}
	suit, val := SuitOf(tile), ValueOf(tile)
	base := suit * NumValues
	// 检查 val-2 ~ val+2 范围内是否有其他牌（排除自身）
	for dv := -2; dv <= 2; dv++ {
		if dv == 0 {
			continue
		}
		nv := val + dv
		if nv < 0 || nv >= NumValues {
			continue
		}
		if counts[base+nv] > 0 {
			return false // 有邻居，不是真孤张
		}
	}
	return true
}

// Abs 返回整数的绝对值
func Abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
