package bot

import (
	"math/rand/v2"
	"sort"
)

// DifficultyConfig 难度配置（设计蓝图见 docs/bots/mahjong-bot.md §6）
type DifficultyConfig struct {
	Name             string  // 难度名称：easy/normal/hard
	DefenseWeight    float64 // 防守权重 (0-1]：乘入攻守转换进度，影响晚巡防守强度
	ErrorRate        float64 // 失误率 (0-1)：以小概率选择次优出牌，模拟人类判断误差
	ShantenPrecision int     // 向听数精度：仅能区分 0~该值的向听差距，更深一律视为该值
	UkeireMode       string  // ukeire 模式："fast"/"standard"/"precise"（precise 暂与 standard 等价，见 §11.3）
	ReadOpponent     bool    // 是否启用对手读牌（听牌推断/染手识别/现物兜底）
	Aggression       float64 // 激进程度 (0-1)：影响碰牌所需的安全牌余量阈值
	// TenpaiInferDiscards/TenpaiInferTurn 对手疑似听牌推断阈值（最小舍牌数/最小巡数，
	// 生产默认 8/12）：难度越高阈值越低，越早警惕对手听牌、越早进入防守
	// （驱动危险度 +20 修正与现物兜底触发，见 controller.buildOpponentModels）
	TenpaiInferDiscards int
	TenpaiInferTurn     int
	// GenbutsuTurn/GenbutsuDanger 现物兜底触发下界（巡数 > GenbutsuTurn 且
	// 危险度 > GenbutsuDanger 时改打现物，生产默认 12/70）：难度越高阈值越低，
	// 越早放弃生张进攻、转入现物防守（hard 档与更早的听牌推断联动，见 §6）
	GenbutsuTurn   int
	GenbutsuDanger int
}

// DifficultyPresets 三档难度预设
var DifficultyPresets = map[string]DifficultyConfig{
	"easy": {
		Name:             "easy",
		DefenseWeight:    0.3,    // 进攻优先：防守修正弱
		ErrorRate:        0.10,   // 10% 概率选择次优牌
		ShantenPrecision: 2,      // 只能区分 0/1/2 向听
		UkeireMode:       "fast", // 快速 ukeire（只统计进张种类）
		ReadOpponent:     false,  // 不分析对手舍牌模式
		Aggression:       0.7,    // 早巡积极碰牌
	},
	"normal": {
		Name:             "normal",
		DefenseWeight:    0.6,        // 攻守平衡
		ErrorRate:        0.05,       // 5% 概率选择次优牌
		ShantenPrecision: 4,          // 能区分 0-4 向听
		UkeireMode:       "standard", // 标准 ukeire（种类×剩余枚数）
		ReadOpponent:     true,       // 启用对手读牌
		Aggression:       0.5,        // 攻守平衡
	},
	"hard": {
		Name:                "hard",
		// 0.9 保持最高防守强度；胜率瓶颈由 genbutsuFallback 的“听牌不弃胡”条件解决
		DefenseWeight:       0.9,
		ErrorRate:           0.01, // 1% 概率选择次优牌
		ShantenPrecision:    8,    // 精确区分 0-8 向听
		UkeireMode:          "precise",
		ReadOpponent:        true, // 对手读牌 + 染手识别
		Aggression:          0.4,  // 晚巡谨慎碰牌（实测 0.5 会把 normal 压到 easy 之下）
		TenpaiInferDiscards: 6,    // 更早警惕对手听牌（6 舍牌/9 巡即视为疑似听牌）
		TenpaiInferTurn:     9,
		GenbutsuTurn:        9,  // 10 巡起即放弃生张改打现物（前 12 巡积极防守，见 §6）
		GenbutsuDanger:      60, // 生张+疑似听牌修正即达 70，60 阈值恰好覆盖生张
	},
}

// DifficultyFor 解析难度档位：空或未知档位回退 normal
func DifficultyFor(name string) DifficultyConfig {
	if cfg, ok := DifficultyPresets[name]; ok {
		return cfg
	}
	return DifficultyPresets["normal"]
}

// normalize 补全零值配置：未携带难度的调用方（会话托管兜底、既有测试、
// 直接构造 DiscardContext 的场景）保持引擎既有行为——
// 防守权重 1.0、全精度向听、标准 ukeire、无失误、中性碰牌阈值、
// 对手疑似听牌推断沿用生产默认口径（8 舍牌/12 巡）。
func (c DifficultyConfig) normalize() DifficultyConfig {
	if c.DefenseWeight <= 0 {
		c.DefenseWeight = 1.0
	}
	if c.ShantenPrecision <= 0 {
		c.ShantenPrecision = 8
	}
	if c.UkeireMode == "" {
		c.UkeireMode = "standard"
	}
	if c.Aggression <= 0 {
		c.Aggression = 0.5
	}
	if c.TenpaiInferDiscards <= 0 {
		c.TenpaiInferDiscards = 8
	}
	if c.TenpaiInferTurn <= 0 {
		c.TenpaiInferTurn = 12
	}
	if c.GenbutsuTurn <= 0 {
		c.GenbutsuTurn = 12
	}
	if c.GenbutsuDanger <= 0 {
		c.GenbutsuDanger = 70
	}
	return c
}

// TenpaiInferGates 对手疑似听牌推断阈值（最小舍牌数, 最小巡数）。
// 生产口径见 controller.buildOpponentModels：舍牌数与巡数同时达标才视为疑似听牌，
// 进而触发危险度 +20 修正与现物兜底。零值配置回退生产默认（8, 12）。
func (c DifficultyConfig) TenpaiInferGates() (minDiscards, minTurn int) {
	c = c.normalize()
	return c.TenpaiInferDiscards, c.TenpaiInferTurn
}

// GenbutsuGates 现物兜底触发下界（最小巡数, 最小危险度）：
// 巡数与危险度同时超过阈值才改打现物（engine.genbutsuFallback）。
// normal 档保持既有生产行为（12 巡/危险度 70）；零值配置回退默认。
func (c DifficultyConfig) GenbutsuGates() (minTurn, minDanger int) {
	c = c.normalize()
	return c.GenbutsuTurn, c.GenbutsuDanger
}

// pongSafeThreshold 碰牌所需的安全牌余量阈值：激进档放宽、保守档收紧。
// 中性档（含零值回退）为 3，与既有行为一致。
func pongSafeThreshold(cfg DifficultyConfig) int {
	cfg = cfg.normalize()
	switch {
	case cfg.Aggression >= 0.6:
		return 2
	case cfg.Aggression <= 0.45:
		return 4
	default:
		return 3
	}
}

// chooseSuboptimalTile 可控失误的次优选牌：候选按评分升序（评分越小越优）排列，
// 跳过最优后取前两名（即评分第二/第三优）随机选择一张，失误"不离谱"。
// rng 为 nil 时使用全局随机源；返回 false 表示无其他候选、无法失误。
func chooseSuboptimalTile(rng *rand.Rand, cands []scoredCandidate, bestTile int) (scoredCandidate, bool) {
	sorted := make([]scoredCandidate, len(cands))
	copy(sorted, cands)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].score < sorted[j].score })

	alts := make([]scoredCandidate, 0, 2)
	for _, c := range sorted {
		if c.tile == bestTile {
			continue
		}
		alts = append(alts, c)
		if len(alts) == 2 {
			break
		}
	}
	if len(alts) == 0 {
		return scoredCandidate{}, false
	}
	if rng != nil {
		return alts[rng.IntN(len(alts))], true
	}
	return alts[rand.IntN(len(alts))], true
}
