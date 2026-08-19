package bot

import (
	"context"
	"math"
	"testing"

	"xgames/internal/games/mahjong/rule"
)

// 牌面常量（与 engine_test.go 的 man3/4/5/7、honorTile 互补）
// 万子 0-8、筒子 9-17、条子 18-26、字牌 27-33（东南西北白发）
const (
	man1  = 0
	man2  = 1
	man6  = 5
	man8  = 7
	man9  = 8
	pin3  = 11
	pin4  = 12
	pin5  = 13
	pin6  = 14
	pin7  = 15
	pin8  = 16
	pin9  = 17
	sou4  = 21
	sou5  = 22
	sou6  = 23
	sou7  = 24
	sou8  = 25
	sou9  = 26
	east  = 27
	south = 28
	west  = 29
	north = 30
)

// TestDifficultyFor 难度解析：未知/空档位回退 normal，三档预设关键字段单调差异
func TestDifficultyFor(t *testing.T) {
	for _, name := range []string{"", "unknown", "master"} {
		if got := DifficultyFor(name); got.Name != "normal" {
			t.Errorf("难度 %q 应回退 normal，实际 %s", name, got.Name)
		}
	}
	for _, name := range []string{"easy", "normal", "hard"} {
		if got := DifficultyFor(name); got != DifficultyPresets[name] {
			t.Errorf("难度 %s 应与预设一致", name)
		}
	}

	easy, normal, hard := DifficultyFor("easy"), DifficultyFor("normal"), DifficultyFor("hard")
	if easy.ErrorRate <= hard.ErrorRate {
		t.Errorf("easy 失误率(%v)应高于 hard(%v)", easy.ErrorRate, hard.ErrorRate)
	}
	if easy.DefenseWeight >= hard.DefenseWeight {
		t.Errorf("easy 防守权重(%v)应低于 hard(%v)", easy.DefenseWeight, hard.DefenseWeight)
	}
	if easy.ShantenPrecision >= hard.ShantenPrecision {
		t.Errorf("easy 向听精度(%d)应低于 hard(%d)", easy.ShantenPrecision, hard.ShantenPrecision)
	}
	if easy.ReadOpponent || !hard.ReadOpponent {
		t.Errorf("easy 不应启用对手读牌，hard 应启用")
	}
	// 对手疑似听牌推断阈值：hard 更早警惕对手听牌（阈值更低）
	if d, turn := hard.TenpaiInferGates(); d >= 8 || turn >= 12 {
		t.Errorf("hard 推断阈值 (%d,%d) 应低于生产默认 (8,12)", d, turn)
	}
	if d, turn := easy.TenpaiInferGates(); d != 8 || turn != 12 {
		t.Errorf("easy 推断阈值应保持生产默认 (8,12)，实际 (%d,%d)", d, turn)
	}
	// 现物兜底触发下界：hard 更早弃打生张转入现物防守
	if turn, danger := hard.GenbutsuGates(); turn != 9 || danger != 60 {
		t.Errorf("hard 现物兜底阈值应为 (9,60)，实际 (%d,%d)", turn, danger)
	}
	if turn, danger := normal.GenbutsuGates(); turn != 12 || danger != 70 {
		t.Errorf("normal 现物兜底阈值应保持生产默认 (12,70)，实际 (%d,%d)", turn, danger)
	}
}

// TestDifficultyNormalize 零值配置补全：未携带难度的调用方保持引擎既有行为
func TestDifficultyNormalize(t *testing.T) {
	got := DifficultyConfig{}.normalize()
	if got.DefenseWeight != 1.0 || got.ShantenPrecision != 8 || got.UkeireMode != "standard" ||
		got.Aggression != 0.5 || got.ErrorRate != 0 {
		t.Errorf("零值配置应归一化为既有行为（防守1.0/精度8/standard/激进度0.5/无失误），实际 %+v", got)
	}
	// 疑似听牌推断阈值回退生产默认口径（8 舍牌/12 巡）
	if d, turn := (DifficultyConfig{}).TenpaiInferGates(); d != 8 || turn != 12 {
		t.Errorf("零值配置推断阈值应回退 (8,12)，实际 (%d,%d)", d, turn)
	}
	// 现物兜底触发下界回退生产默认口径（12 巡/危险度 70）
	if turn, danger := (DifficultyConfig{}).GenbutsuGates(); turn != 12 || danger != 70 {
		t.Errorf("零值配置现物兜底阈值应回退 (12,70)，实际 (%d,%d)", turn, danger)
	}
	// 归一化不得覆盖已配置值
	if got := DifficultyFor("hard").normalize(); got.DefenseWeight != 0.9 || got.ErrorRate != 0.01 {
		t.Errorf("hard 预设归一化后字段不应被改写，实际 %+v", got)
	}
}

// TestPongSafeThreshold 碰牌安全牌余量阈值：简单 2 / 普通 3 / 困难 4 / 零值回退 3
func TestPongSafeThreshold(t *testing.T) {
	cases := []struct {
		name string
		cfg  DifficultyConfig
		want int
	}{
		{"easy", DifficultyFor("easy"), 2},
		{"normal", DifficultyFor("normal"), 3},
		{"hard", DifficultyFor("hard"), 4},
		{"zero", DifficultyConfig{}, 3},
	}
	for _, tc := range cases {
		if got := pongSafeThreshold(tc.cfg); got != tc.want {
			t.Errorf("%s 碰牌安全牌阈值应为 %d，实际 %d", tc.name, tc.want, got)
		}
	}
}

// TestDecideAction_WinPriority 胡牌永远优先接受，与难度和安全牌余量无关
func TestDecideAction_WinPriority(t *testing.T) {
	e := NewEngine(nil)
	// 13 张 + 荣和牌 = 4 面子 + 1 雀头
	winHand := []int{man1, man2, man3, man4, man5, man6, man7, man8, man9, pin3, pin4, sou7, sou7}
	// 10 张（1 鸣面子）+ 荣和牌 = 3 面子 + 1 雀头
	winHandWithMeld := []int{man1, man2, man3, man4, man5, man6, pin7, pin8, sou7, sou7}

	for _, name := range []string{"easy", "normal", "hard"} {
		cfg := DifficultyFor(name)
		if _, win := e.DecideAction(context.Background(), "bot", winHand, pin5, 0, cfg); !win {
			t.Errorf("%s: 荣和应接受", name)
		}
		if _, win := e.DecideAction(context.Background(), "bot", winHandWithMeld, pin9, 1, cfg); !win {
			t.Errorf("%s: 含鸣面子时荣和应接受", name)
		}
	}
}

// TestDecideAction_TenpaiAfterPong 碰后即听牌 → 无论难度与安全牌余量都碰
func TestDecideAction_TenpaiAfterPong(t *testing.T) {
	e := NewEngine(nil)
	// 碰东后：123万 456万 78筒 + 南南 → 听 6/9 筒
	hand := []int{east, east, man1, man2, man3, man4, man5, man6, pin7, pin8, south, south, north}
	for _, name := range []string{"easy", "normal", "hard"} {
		pong, win := e.DecideAction(context.Background(), "bot", hand, east, 0, DifficultyFor(name))
		if win {
			t.Fatalf("%s: 不应误判为胡", name)
		}
		if !pong {
			t.Errorf("%s: 碰后即听牌应碰", name)
		}
	}
}

// TestDecideAction_ImprovingShantenPong 碰后向听数改善 → 必碰（困难档安全牌不足也碰）
func TestDecideAction_ImprovingShantenPong(t *testing.T) {
	e := NewEngine(nil)
	// 碰前 2 向听（东东雀头 + 两顺子 + 78筒搭子），碰后 1 向听；安全牌余量仅 3（不足困难档阈值 4）
	hand := []int{east, east, south, north, man1, man2, man3, man4, man5, man6, pin7, pin8, sou9}
	for _, name := range []string{"easy", "normal", "hard"} {
		pong, _ := e.DecideAction(context.Background(), "bot", hand, east, 0, DifficultyFor(name))
		if !pong {
			t.Errorf("%s: 向听数改善应必碰", name)
		}
	}
}

// TestDecideAction_PongThresholdByDifficulty 碰后向听数不变时按难度阈值要求安全牌余量：
// 简单(2) < 普通(3) < 困难(4)；零值配置与普通档一致
func TestDecideAction_PongThresholdByDifficulty(t *testing.T) {
	e := NewEngine(nil)
	// 碰后向听数不变（碰前后均为 3 向听），碰后安全牌余量 = 3（东/南/西三张孤张字牌）
	handSafe3 := []int{east, east, man5, man6, pin3, pin4, pin6, pin7, sou7, sou8, south, north, west}
	// 西 换成 8筒（与 67筒 相连，不算安全牌）→ 余量 2
	handSafe2 := []int{east, east, man5, man6, pin3, pin4, pin6, pin7, pin8, sou7, sou8, south, north}

	type expect struct {
		cfg   DifficultyConfig
		pong3 bool
		pong2 bool
	}
	cases := []expect{
		{DifficultyFor("easy"), true, true},
		{DifficultyFor("normal"), true, false},
		{DifficultyFor("hard"), false, false},
		{DifficultyConfig{}, true, false},
	}
	for _, tc := range cases {
		if pong, win := e.DecideAction(context.Background(), "bot", handSafe3, east, 0, tc.cfg); win || pong != tc.pong3 {
			t.Errorf("余量3: 难度 %s 期望 pong=%v，实际 pong=%v win=%v", tc.cfg.Name, tc.pong3, pong, win)
		}
		if pong, win := e.DecideAction(context.Background(), "bot", handSafe2, east, 0, tc.cfg); win || pong != tc.pong2 {
			t.Errorf("余量2: 难度 %s 期望 pong=%v，实际 pong=%v win=%v", tc.cfg.Name, tc.pong2, pong, win)
		}
	}
}

// TestDecideAction_WithMelds_PongAfterTenpai 含鸣面子时的碰牌决策：
// 碰后凑成听牌（面子数参与听牌/向听口径）
func TestDecideAction_WithMelds_PongAfterTenpai(t *testing.T) {
	e := NewEngine(nil)
	// 1 鸣面子 + 10 张手牌，碰 4筒 后：8 张 + 2 鸣面子 → 123万 + 78万 + 孤张，摸 6/9 万即听
	hand := []int{pin4, pin4, man1, man2, man3, man7, man8, south, north, west}
	for _, name := range []string{"easy", "normal", "hard"} {
		pong, win := e.DecideAction(context.Background(), "bot", hand, pin4, 1, DifficultyFor(name))
		if win {
			t.Fatalf("%s: 不应误判为胡", name)
		}
		if !pong {
			t.Errorf("%s: 含鸣面子时碰后听牌应碰", name)
		}
	}
}

// TestDiscardWithErrors_NoError 无失误配置下与增强决策完全一致
func TestDiscardWithErrors_NoError(t *testing.T) {
	e := NewEngine(nil)
	hands := [][]int{
		{man3, man4, man5, honorTile},
		{man3, man5, man7, honorTile},
		{man1, man2, man3, man7, man8, man9, pin3, pin4, pin6, sou7, sou8, east, north, honorTile},
	}
	for i, hand := range hands {
		ctx := DiscardContext{Hand: hand, TurnNumber: 3}
		if got, want := e.DecideDiscardWithErrors(ctx), e.DecideDiscardEnhanced(ctx); got != want {
			t.Errorf("hand %d: ErrorRate=0 应与增强决策一致，got %s want %s",
				i, rule.DisplayName(got), rule.DisplayName(want))
		}
	}
}

// TestDiscardWithErrors_FullError 必失误时改打次优牌；唯一候选无法失误
func TestDiscardWithErrors_FullError(t *testing.T) {
	e := NewEngine(nil)
	hand := []int{man3, man4, man5, honorTile}
	ctx := DiscardContext{Hand: hand, TurnNumber: 0, Difficulty: DifficultyConfig{Name: "test", ErrorRate: 1}}
	best := e.DecideDiscardEnhanced(DiscardContext{Hand: hand, TurnNumber: 0})
	if best != honorTile {
		t.Fatalf("最优解应为孤张字牌，实际 %s", rule.DisplayName(best))
	}
	for i := 0; i < 20; i++ {
		if got := e.DecideDiscardWithErrors(ctx); got == best {
			t.Fatalf("ErrorRate=1 应改打次优牌，仍打出最优 %s", rule.DisplayName(best))
		}
	}

	// 去重后仅一种候选：无处可失误，仍打唯一牌
	single := DiscardContext{
		Hand:       []int{honorTile, honorTile, honorTile},
		TurnNumber: 0,
		Difficulty: DifficultyConfig{ErrorRate: 1},
	}
	if got := e.DecideDiscardWithErrors(single); got != honorTile {
		t.Errorf("唯一候选应原样打出，实际 %s", rule.DisplayName(got))
	}
}

// TestDiscardWithErrors_ErrorRateStatistics 失误率统计校准（±15pp 容差，避免随机抖动）
func TestDiscardWithErrors_ErrorRateStatistics(t *testing.T) {
	e := NewEngine(nil)
	hand := []int{man3, man4, man5, man7, honorTile}
	best := e.DecideDiscardEnhanced(DiscardContext{Hand: hand, TurnNumber: 0})
	for _, rate := range []float64{0.1, 0.5} {
		cfg := DifficultyConfig{Name: "stat", ErrorRate: rate}
		n := 800
		errs := 0
		for i := 0; i < n; i++ {
			if got := e.DecideDiscardWithErrors(DiscardContext{Hand: hand, TurnNumber: 0, Difficulty: cfg}); got != best {
				errs++
			}
		}
		if got := float64(errs) / float64(n); math.Abs(got-rate) > 0.15 {
			t.Errorf("ErrorRate=%.2f 实测失误率 %.3f 超出容差", rate, got)
		}
	}
}

// TestDiscardWithErrors_GenbutsuNotExempted 现物兜底为安全硬规则：失误路径同样不放铳
func TestDiscardWithErrors_GenbutsuNotExempted(t *testing.T) {
	e := NewEngine(nil)
	// 三张孤张字牌危险度相同（晚巡+听牌对手 = 80 > 70），
	// ErrorRate=1 必然改打次优牌，但任何危险候选都应被现物兜底拦下改打东
	ctx := DiscardContext{
		Hand:           []int{east, south, north},
		TurnNumber:     13,
		MyDiscards:     []int{east}, // 现物
		OpponentModels: []OpponentModel{{PlayerID: "opp", IsTenpai: true}},
		Difficulty:     DifficultyConfig{ErrorRate: 1},
	}
	for i := 0; i < 10; i++ {
		if got := e.DecideDiscardWithErrors(ctx); got != east {
			t.Fatalf("晚巡有听牌对手时应打现物字牌，实际 %s", rule.DisplayName(got))
		}
	}
}

// TestReadOpponent_GenbutsuFlip 对手读牌开关带来的难度差异：
// easy 不读牌 → 危险度不达兜底阈值照打；hard 识别听牌对手 → 触发现物兜底改打安全牌
func TestReadOpponent_GenbutsuFlip(t *testing.T) {
	e := NewEngine(nil)
	hand := []int{east, west, north} // 三张孤张字牌，牌效与向听完全相同

	// easy：无对手模型，危险度 60 ≤ 70 → 按手牌顺序打出东（危险牌）
	easyCtx := DiscardContext{
		Hand:       hand,
		TurnNumber: 13,
		MyDiscards: []int{west},
		Difficulty: DifficultyFor("easy"),
	}
	if got := e.DecideDiscardEnhanced(easyCtx); got != east {
		t.Errorf("easy 无读牌应照打东（危险牌），实际 %s", rule.DisplayName(got))
	}

	// hard：听牌对手 +20 危险度 → 80 > 70 → 现物兜底改打西
	hardCtx := DiscardContext{
		Hand:           hand,
		TurnNumber:     13,
		MyDiscards:     []int{west},
		OpponentModels: []OpponentModel{{PlayerID: "opp", IsTenpai: true}},
		Difficulty:     DifficultyFor("hard"),
	}
	if got := e.DecideDiscardEnhanced(hardCtx); got != west {
		t.Errorf("hard 应识别听牌并改打现物西，实际 %s", rule.DisplayName(got))
	}
}

// TestDefenseWeight_ScoreGap 防守权重单调放大危险牌与安全牌的评分差距；前巡防守权重不生效
func TestDefenseWeight_ScoreGap(t *testing.T) {
	e := NewEngine(nil)
	hand := []int{east, west, north}
	discards := []int{east, east} // 东已两家打过 → 相对安全

	gapAt := func(cfg DifficultyConfig, turn int) int {
		cands := e.scoreCandidates(DiscardContext{
			Hand:           hand,
			DiscardedTiles: discards,
			TurnNumber:     turn,
			Difficulty:     cfg,
		})
		var safe, danger int
		for _, c := range cands {
			switch c.tile {
			case east:
				safe = c.score
			case west:
				danger = c.score
			}
		}
		return danger - safe
	}

	easy, hard := DifficultyFor("easy"), DifficultyFor("hard")
	if gap := gapAt(easy, 14); gap <= 0 {
		t.Errorf("晚巡危险牌评分应高于安全牌，gap=%d", gap)
	}
	if gapAt(hard, 14) <= gapAt(easy, 14) {
		t.Errorf("防守权重越大，安全/危险评分差距应越大")
	}
	if gap := gapAt(hard, 0); gap != 0 {
		t.Errorf("前巡防守权重不应生效（攻守转换进度为 0），gap=%d", gap)
	}
}

// TestShantenPrecision_Collapse 向听数精度：低难度无法区分深向听差距（超出精度按上限截断）
func TestShantenPrecision_Collapse(t *testing.T) {
	e := NewEngine(nil)
	// 打东后 1 向听，打 9万 后 2 向听（东为孤张字牌、9万 为对子成员）
	hand := []int{man1, man2, man3, sou4, sou5, sou6, pin7, pin8, man9, man9, east, south, north, north}

	gapAt := func(precision int) int {
		cfg := DifficultyConfig{ShantenPrecision: precision}
		cands := e.scoreCandidates(DiscardContext{Hand: hand, TurnNumber: 0, Difficulty: cfg})
		var eastScore, man9Score int
		for _, c := range cands {
			switch c.tile {
			case east:
				eastScore = c.score
			case man9:
				man9Score = c.score
			}
		}
		return man9Score - eastScore
	}

	full := gapAt(8) // 全精度：向听差 1 → 差距含 1000 向听项
	low := gapAt(1)  // 低精度：2 向听被截断为 1 → 向听项消失
	if full-low != 1000 {
		t.Errorf("精度 8 与精度 1 的评分差距应恰好差一个向听项(1000)，full=%d low=%d", full, low)
	}
}

// TestUkeireMode_FastVsStandard ukeire 模式：standard 计进张种类×剩余枚数，fast 只计种类
func TestUkeireMode_FastVsStandard(t *testing.T) {
	// 13 张手牌，打 6万 后存在多处有效进张（剩余枚数 ≥ 2）
	hand := []int{man1, man2, man3, man5, man6, sou4, sou5, sou6, pin7, pin8, pin9, east, north}
	counts := rule.CountsFromHand(hand)
	std := discardLoss(counts, nil, man6, 0, false)
	fast := discardLoss(counts, nil, man6, 0, true)
	if fast <= 0 {
		t.Fatalf("打 6万 后应存在有效进张，fast=%d", fast)
	}
	if std <= fast {
		t.Errorf("standard ukeire 损失(%d)应大于 fast(%d)", std, fast)
	}
}
