// Package nickname 随机昵称生成器（词库 + 组合逻辑）
package nickname

import (
	"crypto/rand"
	"math/big"
)

// 昵称词库（对齐原项目）
var (
	adjectives = []string{
		"勇敢的", "聪明的", "快乐的", "神秘的", "酷炫的",
		"优雅的", "可爱的", "威武的", "沉稳的", "活泼的",
		"机智的", "潇洒的", "温柔的", "霸气的", "淡定的",
		"闪亮的", "迷人的", "傲娇的", "呆萌的", "高冷的",
	}
	nouns = []string{
		"小鸡", "熊猫", "老虎", "狮子", "猴子",
		"兔子", "狐狸", "海豚", "企鹅", "考拉",
		"柯基", "柴犬", "布偶", "龙猫", "仓鼠",
		"刺猬", "松鼠", "浣熊", "水獭", "羊驼",
	}
)

// Generate 生成随机昵称（形容词 + 名词）
func Generate() string {
	return adjectives[randInt(len(adjectives))] + nouns[randInt(len(nouns))]
}

func randInt(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(v.Int64())
}
