package bot

// DifficultyConfig 五子棋难度配置（设计蓝图见 docs/bots/gomoku-bot.md §9 Phase 1；
// 实施蓝本参照 mahjong/bot/difficulty.go）。
type DifficultyConfig struct {
	Name            string  // 难度名称：easy/normal/hard
	MaxDepth        int     // 迭代加深最大搜索深度（简单 2 / 普通 3 / 困难 4）
	VCFMaxDepth     int     // VCF/VCT 杀棋搜索最大深度（简单 6 / 普通 8 / 困难 8）
	OpeningBookRate float64 // 开局库使用概率 (0-1]：低档位概率性放弃定式，模拟人类不熟开局
	RandomTopN      int     // Top-N 候选按 softmax 概率选择：1=永远选最优（既有行为）
	SearchTimeMs    int     // 迭代加深时间预算（毫秒）；0 = 不限时（跑满 MaxDepth）
}

// DifficultyPresets 三档难度预设
var DifficultyPresets = map[string]DifficultyConfig{
	"easy": {
		Name:            "easy",
		MaxDepth:        2,
		VCFMaxDepth:     6,
		OpeningBookRate: 0.3, // 70% 概率不用开局库
		RandomTopN:      5,   // 前 5 候选概率选择，明显失误
		SearchTimeMs:    2000,
	},
	"normal": {
		Name:            "normal",
		MaxDepth:        3,
		VCFMaxDepth:     8,
		OpeningBookRate: 0.5,
		RandomTopN:      3, // 前 3 候选，偶有偏差
		SearchTimeMs:    2000,
	},
	"hard": {
		Name:            "hard",
		MaxDepth:        4,
		VCFMaxDepth:     8, // 从 12 降至 8：过深的 VCF 挤占主搜索时间预算，反而削弱实力
		OpeningBookRate: 0.8,
		RandomTopN:      2, // 前 2 候选，保持挑战性
		SearchTimeMs:    2000,
	},
}

// DifficultyFor 解析难度档位：空或未知档位回退 normal
func DifficultyFor(name string) DifficultyConfig {
	if cfg, ok := DifficultyPresets[name]; ok {
		return cfg
	}
	return DifficultyPresets["normal"]
}

// normalize 补全零值配置：未携带难度的调用方（会话托管兜底、复盘分析、既有测试）
// 保持引擎既有行为——4 层搜索、16 层杀棋、始终用开局库、永远选最优着法。
func (c DifficultyConfig) normalize() DifficultyConfig {
	if c.MaxDepth <= 0 {
		c.MaxDepth = maxSearchDepth
	}
	if c.VCFMaxDepth <= 0 {
		c.VCFMaxDepth = vcfMaxDepth
	}
	if c.OpeningBookRate <= 0 {
		c.OpeningBookRate = 1.0
	}
	if c.RandomTopN <= 0 {
		c.RandomTopN = 1
	}
	if c.SearchTimeMs <= 0 {
		c.SearchTimeMs = searchTimeMs
	}
	if c.Name == "" {
		c.Name = "normal"
	}
	return c
}

// QuickDifficultyPresets 测试专用快速预设：关闭时间预算（SearchTimeMs=999999，等效不限时），
// 搜索跑满 MaxDepth 即停；VCF 深度同步缩减，避免杀棋搜索挤占主搜索。
// 难度梯度仍由 MaxDepth 保证（easy 2 < normal 3 < hard 4）。
var QuickDifficultyPresets = map[string]DifficultyConfig{
	"easy":   {Name: "easy", MaxDepth: 2, VCFMaxDepth: 4, OpeningBookRate: 0.3, RandomTopN: 5, SearchTimeMs: 999999},
	"normal": {Name: "normal", MaxDepth: 3, VCFMaxDepth: 6, OpeningBookRate: 0.5, RandomTopN: 3, SearchTimeMs: 999999},
	"hard":   {Name: "hard", MaxDepth: 4, VCFMaxDepth: 8, OpeningBookRate: 0.8, RandomTopN: 2, SearchTimeMs: 999999},
}

// QuickDifficultyFor 测试专用难度解析：使用 QuickDifficultyPresets
func QuickDifficultyFor(name string) DifficultyConfig {
	if cfg, ok := QuickDifficultyPresets[name]; ok {
		return cfg
	}
	return QuickDifficultyPresets["normal"]
}
