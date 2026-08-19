// Package rule 麻将核心规则引擎（中国四人麻将标准：3 色数牌 × 9 × 4 + 7 字牌 × 4 = 136 张）。
//
// 牌编码：
//   0-26: 万/筒/条 (suit 0/1/2, value 0-8)
//   27-33: 字牌 (suit=3): 东(27)/南(28)/西(29)/北(30)/中(31)/发(32)/白(33)
//
// 手牌用 count[34]int 计数表示；墙用 []int 平铺。
package rule

import (
	"math/rand/v2"
	"sync"
)

const (
	NumSuits   = 3  // 万/筒/条
	NumValues  = 9  // 每花色 9 点数
	HonorStart = 27 // 字牌起始索引
	HonorCount = 7  // 字牌种类数
	NumTypes   = NumSuits*NumValues + HonorCount // 34
	Copies     = 4
	TotalTiles = NumTypes * Copies // 136
	HandSize   = 13
)

// TileType 牌类型（0-33）
type TileType = int

// SuitOf 牌类型 → 花色（0=万 1=筒 2=条 3=字牌）
func SuitOf(t int) int {
	if t < HonorStart {
		return t / NumValues
	}
	return 3
}

// ValueOf 牌类型 → 点数（数牌 0-8，字牌 0-6）
func ValueOf(t int) int {
	if t < HonorStart {
		return t % NumValues
	}
	return t - HonorStart
}

// IsHonor 是否为字牌
func IsHonor(t int) bool { return t >= HonorStart }

// IsNumber 是否为数牌
func IsNumber(t int) bool { return t < HonorStart }

// SuitName 花色名（前端展示用）
func SuitName(suit int) string {
	switch suit {
	case 0:
		return "万"
	case 1:
		return "筒"
	case 2:
		return "条"
	case 3:
		return "字"
	}
	return "?"
}

var honorNames = []string{"东", "南", "西", "北", "中", "发", "白"}

// DisplayName 牌面中文名（如 "3万" "东" "中"）
func DisplayName(t int) string {
	if IsHonor(t) {
		return honorNames[t-HonorStart]
	}
	return string(rune('1'+ValueOf(t))) + SuitName(SuitOf(t))
}

// HonorOfValue value → 字牌类型
func HonorOfValue(v int) int { return HonorStart + v }

// HonorValueOf 字牌类型 → 字牌序号
func HonorValueOf(t int) int { return t - HonorStart }

// --- 墙 ---

// WallGenerator 可被测试覆盖的墙生成器。默认使用全局随机数洗牌。
var WallGenerator func() []int

// DiceRoller 可被测试覆盖的掷骰函数（返回 1-6）。默认使用全局随机数。
var DiceRoller func() int

// RollDice 掷一枚色子（1-6）：定庄用，测试可注入确定性骰子
func RollDice() int {
	if DiceRoller != nil {
		return DiceRoller()
	}
	return rand.IntN(6) + 1
}

// NewWall 创建并洗牌（136 张）
func NewWall() []int {
	if WallGenerator != nil {
		return WallGenerator()
	}
	wall := make([]int, 0, TotalTiles)
	for t := 0; t < NumTypes; t++ {
		for c := 0; c < Copies; c++ {
			wall = append(wall, t)
		}
	}
	rand.Shuffle(len(wall), func(i, j int) { wall[i], wall[j] = wall[j], wall[i] })
	return wall
}

// DrawFromWall 从墙尾抽 n 张；返回抽到的牌和剩余墙。
// 抽出的牌必须拷贝：若直接返回 wall[:n]，其底层数组与剩余墙共享且带富余容量，
// 之后对各家手牌的 append 会原地写入相邻玩家的手牌区，造成手牌被篡改。
func DrawFromWall(wall []int, n int) ([]int, []int) {
	if n > len(wall) {
		n = len(wall)
	}
	drawn := make([]int, n)
	copy(drawn, wall[:n])
	return drawn, wall[n:]
}

// --- 手牌操作 ---

// AddTiles 向计数数组添加牌
func AddTiles(counts []int, tiles []int) {
	for _, t := range tiles {
		counts[t]++
	}
}

// RemoveTile 从计数数组移除一张牌；成功返回 true
func RemoveTile(counts []int, t int) bool {
	if counts[t] <= 0 {
		return false
	}
	counts[t]--
	return true
}

// CountTiles 计数数组中的牌总数
func CountTiles(counts []int) int {
	n := 0
	for _, c := range counts {
		n += c
	}
	return n
}

// CountsFromHand 从手牌列表构建计数数组
func CountsFromHand(hand []int) []int {
	counts := make([]int, NumTypes)
	AddTiles(counts, hand)
	return counts
}

// --- 胡牌判定 ---

// IsWinningHand 14 张牌是否胡牌（4 面子 + 1 雀头 或 7 对子 或 十三幺）
func IsWinningHand(counts []int) bool {
	if CountTiles(counts) != 14 {
		return false
	}
	return CheckStandardWin(counts) || CheckSevenPairs(counts)
}

// CheckStandardWin 标准胡：4 面子 + 1 雀头
func CheckStandardWin(counts []int) bool {
	cp := make([]int, NumTypes)
	copy(cp, counts)
	for t := 0; t < NumTypes; t++ {
		if cp[t] >= 2 {
			cp[t] -= 2
			if canDecompose(cp) {
				return true
			}
			cp[t] += 2
		}
	}
	return false
}

// canDecompose 剩余牌能否全部拆为面子（刻子 + 顺子）
func canDecompose(counts []int) bool {
	first := -1
	for t := 0; t < NumTypes; t++ {
		if counts[t] > 0 {
			first = t
			break
		}
	}
	if first == -1 {
		return true
	}

	// 尝试刻子
	if counts[first] >= 3 {
		counts[first] -= 3
		if canDecompose(counts) {
			counts[first] += 3
			return true
		}
		counts[first] += 3
	}

	// 尝试顺子（仅数牌可做顺子）
	if IsNumber(first) {
		suit := SuitOf(first)
		val := ValueOf(first)
		if val <= 6 {
			t1, t2 := first+1, first+2
			if t1 < HonorStart && t2 < HonorStart && SuitOf(t1) == suit && SuitOf(t2) == suit &&
				counts[t1] > 0 && counts[t2] > 0 {
				counts[first]--
				counts[t1]--
				counts[t2]--
				if canDecompose(counts) {
					counts[first]++
					counts[t1]++
					counts[t2]++
					return true
				}
				counts[first]++
				counts[t1]++
				counts[t2]++
			}
		}
	}

	return false
}

// CheckSevenPairs 七对子
func CheckSevenPairs(counts []int) bool {
	pairs := 0
	for _, c := range counts {
		if c != 0 && c != 2 && c != 4 {
			return false
		}
		pairs += c / 2
	}
	return pairs == 7
}

// IsWinningHandWithMelds 判断含已鸣面子的胡牌。
// meldCount 为已完成并置于面子区的完整面子数（每个碰/杠计 1 个面子）。
// 已有面子时，剩余手牌只需组成剩余的（4-meldCount）组面子 + 1 对雀头，
// 即手牌张数 = 14 - 3*meldCount。
func IsWinningHandWithMelds(counts []int, meldCount int) bool {
	if meldCount <= 0 {
		return IsWinningHand(counts)
	}
	if CountTiles(counts) != HandSize+1-3*meldCount {
		return false
	}
	return CheckStandardWin(counts)
}

// --- 听牌判定 ---

// IsTenpai 13 张牌是否听牌（任意一张能胡）
func IsTenpai(counts []int) bool {
	for t := 0; t < NumTypes; t++ {
		if counts[t] >= 4 {
			continue
		}
		counts[t]++
		won := IsWinningHand(counts)
		counts[t]--
		if won {
			return true
		}
	}
	return false
}

// --- 向听数计算 ---

// ShantenNumber 计算手牌的精确向听数（0-8）。
// 使用改进的搭子分解算法，能准确区分各向听阶段。
// 支持任意张数的手牌（测试场景或含面子状态）。
func ShantenNumber(counts []int) int {
	total := CountTiles(counts)
	if total < 13 {
		return 8
	}
	if total == 14 && IsWinningHand(counts) {
		return -1
	}

	// 使用标准向听数计算公式
	return calculateStandardShanten(counts, 0)
}

// ShantenWithMelds 计算带已鸣面子的手牌向听数（0-8）。
// counts 为不含面子牌的手牌计数（13 - 3*meldCount 张），meldCount 为已鸣面子数
// （碰/杠各计 1）。meldCount 为 0 时与 ShantenNumber 等价。
// 供碰牌决策等需要在手牌不足 13 张口径下比较向听数的场景使用。
func ShantenWithMelds(counts []int, meldCount int) int {
	if meldCount <= 0 {
		return ShantenNumber(counts)
	}
	return calculateStandardShanten(counts, meldCount)
}

// calculateStandardShanten 标准向听数计算（含已鸣面子）
// 公式：shanten = 8 - 2×(已完成面子数+已鸣面子数)
//        - min(搭子数, 4-已完成面子数-已鸣面子数) - (有雀头 ? 1 : 0)
// meldCount: 已鸣面子数（碰/杠各计 1），0 为纯手牌口径
func calculateStandardShanten(counts []int, meldCount int) int {
	if meldCount < 0 {
		meldCount = 0
	}
	if meldCount > 4 {
		meldCount = 4
	}
	base := 8 - 2*meldCount   // 已鸣面子贡献的向听数减免
	needSets := 4 - meldCount // 还需由手牌组成的面子数

	cp := make([]int, NumTypes)
	copy(cp, counts)

	bestShanten := base

	// 尝试所有可能的雀头组合
	for t := 0; t < NumTypes; t++ {
		if cp[t] < 2 {
			continue
		}
		cp[t] -= 2 // 移除雀头

		completed, remaining := countMentsuAndTaatsu(cp)

		// 标准向听数公式
		taatsuCap := needSets - completed
		if taatsuCap < 0 {
			taatsuCap = 0
		}
		shanten := base - 2*completed - minInt(remaining, taatsuCap) - 1
		if shanten < bestShanten {
			bestShanten = shanten
		}

		cp[t] += 2 // 恢复
	}

	// 无雀头的情况
	completed, remaining := countMentsuAndTaatsu(cp)
	taatsuCap := needSets - completed
	if taatsuCap < 0 {
		taatsuCap = 0
	}
	shantenNoHead := base - 2*completed - minInt(remaining, taatsuCap)
	if shantenNoHead < bestShanten {
		bestShanten = shantenNoHead
	}

	// 确保结果在合理范围内
	if bestShanten < 0 {
		bestShanten = 0
	}
	if bestShanten > 8 {
		bestShanten = 8
	}

	return bestShanten
}

// countMentsuAndTaatsu 计算已完成的面子数和潜在的搭子数
// mentsu: 完整的刻子或顺子
// taatsu: 未完成但有望完成的面子数（仅包括真正的搭子，不包括孤张）
func countMentsuAndTaatsu(counts []int) (mentsu, taatsu int) {
	cp := make([]int, NumTypes)
	copy(cp, counts)

	// 第一遍：提取刻子
	for t := 0; t < NumTypes; t++ {
		if cp[t] >= 3 {
			k := cp[t] / 3
			mentsu += k
			cp[t] -= k * 3
		}
	}

	// 第二遍：提取顺子和搭子（按花色处理）
	for suit := 0; suit < 3; suit++ {
		base := suit * NumValues
		m, t := countSuitMentsuAndTaatsu(cp[base : base+NumValues])
		mentsu += m
		taatsu += t
	}

	// 字牌的处理：只有对子才算搭子，单张不算
	for t := HonorStart; t < NumTypes; t++ {
		if cp[t] == 2 {
			taatsu++ // 对子算作搭子
			cp[t] = 0
		} else if cp[t] >= 3 {
			// 这种情况不应该出现，因为前面已经提取了刻子
			k := cp[t] / 3
			mentsu += k
			cp[t] -= k * 3
		}
		// 单张字牌不计入搭子
	}

	return mentsu, taatsu
}

// countSuitMentsuAndTaatsu 计算单个花色的面子和搭子数
func countSuitMentsuAndTaatsu(values []int) (mentsu, taatsu int) {
	cp := make([]int, len(values))
	copy(cp, values)

	// 先提取顺子
	for i := 0; i <= 6; i++ {
		if cp[i] > 0 && cp[i+1] > 0 && cp[i+2] > 0 {
			minCount := cp[i]
			if cp[i+1] < minCount {
				minCount = cp[i+1]
			}
			if cp[i+2] < minCount {
				minCount = cp[i+2]
			}
			mentsu += minCount
			cp[i] -= minCount
			cp[i+1] -= minCount
			cp[i+2] -= minCount
		}
	}

	// 统计剩余的搭子（两面向听、坎张、边张）
	for i := 0; i < len(values); i++ {
		if cp[i] == 0 {
			continue
		}
		
		// 两面向听：n 和 n+1 都有牌
		if i < 8 && cp[i+1] > 0 {
			taatsu++
			cp[i]--
			cp[i+1]--
			continue
		}
		
		// 坎张：n 和 n+2 都有牌
		if i < 7 && cp[i+2] > 0 {
			taatsu++
			cp[i]--
			cp[i+2]--
			continue
		}
		
		// 孤张不计入搭子
	}

	return mentsu, taatsu
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// containsInt 检查切片是否包含某值（内部辅助函数）
func containsInt(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

// shantenCache 向听数缓存（带线程安全保护和 LRU 淘汰策略）
type lruCacheEntry struct {
	key   string
	value int
}

var (
	shantenCache       = make(map[string]*lruCacheEntry)
	shantenCacheOrder  []*lruCacheEntry // 维护访问顺序（最近使用的在末尾）
	shantenCacheMutex  sync.RWMutex
	shantenCacheMaxSize = 10000
)

// countsToKey 将 counts 数组转换为缓存 key
func countsToKey(counts []int) string {
	// 使用简洁的字符串表示：每4位表示一个牌种的计数（0-4）
	// 34个牌种 × 3bit = 102bit，用十六进制表示约26字符
	key := make([]byte, 0, 34)
	for _, c := range counts {
		key = append(key, byte(c))
	}
	return string(key)
}

// ShantenNumberCached 带缓存的向听数计算（线程安全 + LRU 淘汰）
func ShantenNumberCached(counts []int) int {
	key := countsToKey(counts)
	
	// 读锁检查缓存
	shantenCacheMutex.RLock()
	if entry, ok := shantenCache[key]; ok {
		// 命中：移动到末尾（最近使用）
		moveToLRUTail(entry)
		shantenCacheMutex.RUnlock()
		return entry.value
	}
	shantenCacheMutex.RUnlock()
	
	// 缓存未命中，计算结果
	result := ShantenNumber(counts)
	
	// 写锁更新缓存
	shantenCacheMutex.Lock()
	addToLRUCache(key, result)
	shantenCacheMutex.Unlock()
	
	return result
}

// moveToLRUTail 将缓存条目移动到 LRU 队列末尾（最近使用）
func moveToLRUTail(entry *lruCacheEntry) {
	for i, e := range shantenCacheOrder {
		if e == entry {
			// 从当前位置移除
			shantenCacheOrder = append(shantenCacheOrder[:i], shantenCacheOrder[i+1:]...)
			// 添加到末尾
			shantenCacheOrder = append(shantenCacheOrder, entry)
			break
		}
	}
}

// addToLRUCache 添加新条目到 LRU 缓存
func addToLRUCache(key string, value int) {
	// 如果缓存已满，淘汰最久未使用的条目（队列头部）
	if len(shantenCache) >= shantenCacheMaxSize {
		evictLRUHead()
	}
	
	entry := &lruCacheEntry{key: key, value: value}
	shantenCache[key] = entry
	shantenCacheOrder = append(shantenCacheOrder, entry)
}

// evictLRUHead 淘汰 LRU 队列头部的条目（最久未使用）
func evictLRUHead() {
	if len(shantenCacheOrder) == 0 {
		return
	}
	
	oldest := shantenCacheOrder[0]
	delete(shantenCache, oldest.key)
	shantenCacheOrder = shantenCacheOrder[1:]
}

// ClearShantenCache 清空缓存（每局游戏结束后调用，线程安全）
func ClearShantenCache() {
	shantenCacheMutex.Lock()
	shantenCache = make(map[string]*lruCacheEntry)
	shantenCacheOrder = make([]*lruCacheEntry, 0)
	shantenCacheMutex.Unlock()
}

// ShantenAfterDiscard 模拟打出 discardTile 后的向听数
func ShantenAfterDiscard(counts []int, discardTile int) int {
	if counts[discardTile] == 0 {
		return 99
	}
	counts[discardTile]--
	result := ShantenNumberCached(counts)  // 使用缓存版本
	counts[discardTile]++
	return result
}

// UkeireAfterDiscard 计算打出 discardTile 后的 ukeire（有效进张总数）。
// ukeire = 所有能改善向听数的摸牌的（进张种类 × 剩余枚数）之和。
// seen 为各牌可见张数（牌河+面子+自己手牌），nil 时按全部可用保守处理。
// 返回值：ukeire 总数（进张种类×剩余枚数的累加和）
// 
// 性能优化：使用候选牌预筛选，只检查与手牌有搭子关系的牌，减少60-70%无效计算
func UkeireAfterDiscard(counts []int, discardTile int, seen []int) int {
	if counts[discardTile] == 0 {
		return 0
	}
	counts[discardTile]--
	beforeShanten := ShantenNumberCached(counts)  // 使用缓存版本
	
	// 预筛选候选进张（只保留与手牌有搭子关系的牌）
	candidates := filterCandidateTiles(counts)
	
	ukeire := 0
	for _, t := range candidates {
		if counts[t] >= 4 {
			continue // 已现 4 张，无法再摸
		}
		
		// 计算该牌的剩余枚数
		remaining := 4
		if seen != nil && t < len(seen) {
			remaining = 4 - seen[t]
		}
		if remaining <= 0 {
			continue
		}
		
		// 模拟摸上这张牌
		counts[t]++
		afterShanten := ShantenNumberCached(counts)  // 使用缓存版本
		counts[t]--
		
		// 如果能改善向听数（向听数减少），则计入 ukeire
		if afterShanten < beforeShanten {
			ukeire += remaining
		}
	}
	
	counts[discardTile]++
	return ukeire
}

// filterCandidateTiles 预筛选候选进张牌
// 只保留与手牌有搭子关系的牌（对子、邻牌、跳搭），大幅减少无效计算
// 返回候选牌列表（通常8-12张，而非全部34张）
func filterCandidateTiles(counts []int) []int {
	candidates := make([]int, 0, 12)
	seen := make(map[int]bool)
	
	for t := 0; t < NumTypes; t++ {
		if counts[t] == 0 || seen[t] {
			continue
		}
		seen[t] = true
		
		// 1. 已有牌本身（可能形成刻子或对子）
		if !containsInt(candidates, t) {
			candidates = append(candidates, t)
		}
		
		// 2. 如果是数牌，添加邻居和跳搭
		if IsNumber(t) {
			suit := SuitOf(t)
			val := ValueOf(t)
			base := suit * NumValues
			
			// 添加相邻牌（n-2 ~ n+2）
			for dv := -2; dv <= 2; dv++ {
				if dv == 0 {
					continue
				}
				nv := val + dv
				if nv >= 0 && nv < NumValues {
					neighbor := base + nv
					if !seen[neighbor] && counts[neighbor] < 4 {
						candidates = append(candidates, neighbor)
						seen[neighbor] = true
					}
				}
			}
		}
	}
	
	return candidates
}

// UkeireFast 快速版 ukeire 计算：只统计进张种类数，不乘剩余枚数。
// 适用于性能敏感场景（如模拟测试），速度提升约 30~50%。
// 返回值：进张种类数（每种有效进张计 1，不论剩余枚数）
// 
// 性能优化：使用候选牌预筛选
func UkeireFast(counts []int, discardTile int) int {
	if counts[discardTile] == 0 {
		return 0
	}
	counts[discardTile]--
	beforeShanten := ShantenNumberCached(counts)
	
	// 预筛选候选进张
	candidates := filterCandidateTiles(counts)
	
	ukeire := 0
	for _, t := range candidates {
		if counts[t] >= 4 {
			continue
		}
		
		counts[t]++
		afterShanten := ShantenNumberCached(counts)
		counts[t]--
		
		if afterShanten < beforeShanten {
			ukeire++  // 只计数，不乘剩余枚数
		}
	}
	
	counts[discardTile]++
	return ukeire
}

// UkeireScore 计算打出 discardTile 的牌效评分（基于标准 ukeire 统计）。
// 返回值越大表示牌效越好（进张越多），损失越小。
// seen 为各牌可见张数，nil 时按全部可用保守处理。
func UkeireScore(counts []int, discardTile int, seen []int) int {
	return UkeireAfterDiscard(counts, discardTile, seen)
}

// --- 动作检测 ---

// CanPong 手牌中是否有 2 张与弃牌相同（可碰）
func CanPong(hand []int, discard int) bool {
	n := 0
	for _, t := range hand {
		if t == discard {
			n++
		}
	}
	return n >= 2
}

// CanKongFromDiscard 是否可明杠（手牌中有 3 张与弃牌相同）
func CanKongFromDiscard(hand []int, discard int) bool {
	n := 0
	for _, t := range hand {
		if t == discard {
			n++
		}
	}
	return n >= 3
}

// CanSelfKong 是否可暗杠（手牌中有 4 张相同）
func CanSelfKong(counts []int) int {
	for t := 0; t < NumTypes; t++ {
		if counts[t] == 4 {
			return t
		}
	}
	return -1
}

// CanChi 是否可吃（仅数牌，形成顺子，只有下家能吃）
// 注意：推倒胡规则禁止吃牌，此函数已废弃，仅供其他麻将变体使用
// 参数：hand=手牌，discard=被吃的牌，discardPos=discard 在顺子中的位置 (0/1/2)
func CanChi(hand []int, discard, discardPos int) bool {
	if !IsNumber(discard) {
		return false // 字牌不能吃
	}
	val := ValueOf(discard)
	// discardPos 0: discard 是最小的 → 需要 discard+1, discard+2
	// discardPos 1: discard 在中间 → 需要 discard-1, discard+1
	// discardPos 2: discard 是最大的 → 需要 discard-2, discard-1
	var needed [2]int
	switch discardPos {
	case 0:
		if val > 6 {
			return false
		}
		needed[0] = discard + 1
		needed[1] = discard + 2
	case 1:
		if val == 0 || val == 8 {
			return false
		}
		needed[0] = discard - 1
		needed[1] = discard + 1
	case 2:
		if val < 2 {
			return false
		}
		needed[0] = discard - 2
		needed[1] = discard - 1
	default:
		return false
	}

	c0 := 0
	c1 := 0
	for _, t := range hand {
		if t == needed[0] {
			c0++
		}
		if t == needed[1] {
			c1++
		}
	}
	return c0 > 0 && c1 > 0
}

// CanWinFromDiscard 加上弃牌后是否胡牌（不含面子，meldCount 内传 0）
func CanWinFromDiscard(hand []int, discard int) bool {
	return CanWinFromDiscardWithMelds(hand, discard, 0)
}

// CanWinFromDiscardWithMelds 加上弃牌后是否胡牌（计入已鸣面子）
func CanWinFromDiscardWithMelds(hand []int, discard int, meldCount int) bool {
	counts := CountsFromHand(hand)
	counts[discard]++
	return IsWinningHandWithMelds(counts, meldCount)
}

// CanSelfDrawWin 自摸胡牌（手牌 14 张，不含面子）
func CanSelfDrawWin(hand []int) bool {
	return CanSelfDrawWinWithMelds(hand, 0)
}

// CanSelfDrawWinWithMelds 自摸胡牌（计入已鸣面子）
func CanSelfDrawWinWithMelds(hand []int, meldCount int) bool {
	counts := CountsFromHand(hand)
	return IsWinningHandWithMelds(counts, meldCount)
}

// --- 吃的位置组合（推倒胡规则已禁用）---

// ChiCombinations 返回吃牌的所有合法 discardPos 位置
// 注意：推倒胡规则禁止吃牌，此函数已废弃
func ChiCombinations(hand []int, discard int) []int {
	positions := make([]int, 0, 3)
	for pos := 0; pos < 3; pos++ {
		if CanChi(hand, discard, pos) {
			positions = append(positions, pos)
		}
	}
	return positions
}

// ChiTiles 返回吃牌后需要从手牌中移除的两张牌
// 注意：推倒胡规则禁止吃牌，此函数已废弃
func ChiTiles(discard, discardPos int) [2]int {
	switch discardPos {
	case 0:
		return [2]int{discard + 1, discard + 2}
	case 1:
		return [2]int{discard - 1, discard + 1}
	case 2:
		return [2]int{discard - 2, discard - 1}
	}
	return [2]int{-1, -1}
}

// --- 计分 ---

// CalcFan 计算番数（简化版）
func CalcFan(hand []int, melds []Meld, isSelfDraw bool) int {
	fan := 1 // 基础胡
	if isSelfDraw {
		fan++ // 自摸 +1
	}
	counts := CountsFromHand(hand)
	if isAllOneSuit(counts, melds) {
		fan += 4 // 清一色
	}
	if isAllHonor(counts, melds) {
		fan += 8 // 字一色
	}
	return fan
}

func isAllOneSuit(counts []int, melds []Meld) bool {
	suit := -1
	for t := 0; t < NumTypes; t++ {
		if counts[t] > 0 {
			s := SuitOf(t)
			if suit == -1 {
				suit = s
			} else if suit != s {
				return false
			}
		}
	}
	for _, m := range melds {
		s := SuitOf(m.Tile)
		if suit == -1 {
			suit = s
		} else if suit != s {
			return false
		}
	}
	return suit != -1
}

func isAllHonor(counts []int, melds []Meld) bool {
	for t := 0; t < NumTypes; t++ {
		if counts[t] > 0 && !IsHonor(t) {
			return false
		}
	}
	for _, m := range melds {
		if !IsHonor(m.Tile) {
			return false
		}
	}
	return true
}

// --- 面子 ---

// MeldType 面子类型
type MeldType int

const (
	MeldPong  MeldType = 0 // 碰（明刻）
	MeldKong  MeldType = 1 // 杠（明杠/暗杠/补杠）
	MeldChi   MeldType = 2 // 吃（顺子）
	MeldAnKong MeldType = 3 // 暗杠
)

// Meld 已声明的面子
type Meld struct {
	Type  MeldType
	Tile  int   // 牌类型
	From  int   // 来源玩家索引（-1 = 自摸/暗刻）
	Tiles []int // 组成牌（hand 中移除后的记录）
	// Chi 专用
	ChiPos int `json:"chi_pos,omitempty"` // 吃位置 0/1/2
}
