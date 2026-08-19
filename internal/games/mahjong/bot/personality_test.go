package bot

import (
	"testing"
	"xgames/internal/games/mahjong/rule"
)

// TestBotPersonality_Basic 测试基本性格功能
func TestBotPersonality_Basic(t *testing.T) {
	p := NewBotPersonality(0.6, 0.5, 0.7)

	if p.Aggression != 0.6 {
		t.Errorf("Expected aggression=0.6, got %f", p.Aggression)
	}
	if p.Conservatism != 0.5 {
		t.Errorf("Expected conservatism=0.5, got %f", p.Conservatism)
	}
	if p.Patience != 0.7 {
		t.Errorf("Expected patience=0.7, got %f", p.Patience)
	}
}

// TestCalculateThinkDelay 测试动态思考延迟
func TestCalculateThinkDelay(t *testing.T) {
	p := NewBotPersonality(0.5, 0.5, 0.5)

	// 简单决策：多孤张字牌（下限对齐会话侧语音节奏预算 minActionGap=1.5s）
	simpleHand := []int{27, 28, 29, 30, 31, 32, 33, 0, 0, 9, 9, 18, 18}
	delay := p.CalculateThinkDelay(simpleHand, 3, false)
	delayMs := delay.Milliseconds()
	t.Logf("Simple hand delay: %v (%d ms)", delay, delayMs)
	if delayMs < 1500 || delayMs > 1900 {
		t.Errorf("Simple hand should have 1.5-1.9s delay, got %d ms", delayMs)
	}

	// 中等决策：正常手牌
	mediumHand := []int{0, 1, 2, 9, 10, 18, 19, 20, 27, 27, 4, 5, 6}
	delay = p.CalculateThinkDelay(mediumHand, 8, false)
	delayMs = delay.Milliseconds()
	t.Logf("Medium hand delay: %v (%d ms)", delay, delayMs)
	if delayMs < 1600 || delayMs > 2000 {
		t.Errorf("Medium hand should have 1.6-2s delay, got %d ms", delayMs)
	}

	// 复杂决策：晚巡或已听牌（上限受会话回合超时 afkActDelay=3s 约束）
	complexHand := []int{0, 1, 2, 9, 10, 11, 18, 19, 20, 27, 27, 4, 5}
	delay = p.CalculateThinkDelay(complexHand, 15, true)
	delayMs = delay.Milliseconds()
	t.Logf("Complex hand delay: %v (%d ms)", delay, delayMs)
	if delayMs < 2200 || delayMs > 2800 {
		t.Errorf("Complex hand should have 2.2-2.8s delay, got %d ms", delayMs)
	}
}

// TestUpdateMood 测试情绪更新
func TestUpdateMood(t *testing.T) {
	p := NewBotPersonality(0.5, 0.5, 0.5)

	// 好手牌（向听数低）
	goodHand := []int{0, 1, 2, 9, 10, 11, 18, 19, 20, 27, 27, 4, 5, 6}
	p.UpdateMood(goodHand, false, false)

	if p.Mood.Excitement < 0.5 {
		t.Errorf("Good hand should increase excitement, got %f", p.Mood.Excitement)
	}

	// 连续被胡
	p.UpdateMood(goodHand, true, false)
	if p.ConsecutiveDeals == 0 {
		t.Error("ConsecutiveDeals should increment")
	}
	if p.Mood.Caution < 0.1 {
		t.Errorf("Being dealt against should increase caution, got %f", p.Mood.Caution)
	}

	// 连败
	p.UpdateMood(goodHand, false, true)
	if p.ConsecutiveLosses == 0 {
		t.Error("ConsecutiveLosses should increment")
	}
	if p.Mood.Determination < 0.1 {
		t.Errorf("Losing should increase determination, got %f", p.Mood.Determination)
	}
}

// TestGetAdjustedAggression 测试调整后的激进程度
func TestGetAdjustedAggression(t *testing.T) {
	p := NewBotPersonality(0.5, 0.5, 0.5)

	// 基础值
	agg := p.GetAdjustedAggression("")
	if agg < 0.4 || agg > 0.6 {
		t.Errorf("Base aggression should be around 0.5, got %f", agg)
	}

	// 高决心提高激进度
	p.Mood.Determination = 0.8
	agg = p.GetAdjustedAggression("")
	if agg < 0.5 {
		t.Errorf("High determination should increase aggression, got %f", agg)
	}

	// 高谨慎降低激进度
	p.Mood.Determination = 0
	p.Mood.Caution = 0.8
	agg = p.GetAdjustedAggression("")
	if agg > 0.5 {
		t.Errorf("High caution should decrease aggression, got %f", agg)
	}
}

// TestRecordPlayerBehavior 测试玩家行为记录
func TestRecordPlayerBehavior(t *testing.T) {
	p := NewBotPersonality(0.5, 0.5, 0.5)

	// 模拟激进型玩家（早期大量打字牌）
	aggressiveDiscards := []int{27, 28, 29, 30, 0, 1, 2, 9, 10, 11}
	p.RecordPlayerBehavior("player1", aggressiveDiscards, false)

	profile := p.PlayerProfiles["player1"]
	if profile == nil {
		t.Fatal("Player profile should be created")
	}

	if profile.PlayStyle != StyleAggressive {
		t.Errorf("Aggressive player should be detected as StyleAggressive, got %v", profile.PlayStyle)
	}

	if profile.AggressionLevel < 0.5 {
		t.Errorf("Aggressive player should have high aggression level, got %f", profile.AggressionLevel)
	}

	// 模拟保守型玩家（保留字牌）
	conservativeDiscards := []int{0, 1, 2, 9, 10, 11, 18, 19, 20, 27}
	p.RecordPlayerBehavior("player2", conservativeDiscards, false)

	profile2 := p.PlayerProfiles["player2"]
	if profile2.PlayStyle != StyleConservative {
		t.Errorf("Conservative player should be detected as StyleConservative, got %v", profile2.PlayStyle)
	}
}

// TestCreateBotPersonality 测试机器人性格生成（确定性）
func TestCreateBotPersonality(t *testing.T) {
	// 同一名称应生成相同性格
	p1 := createBotPersonality("BotA")
	p2 := createBotPersonality("BotA")

	if p1.Aggression != p2.Aggression {
		t.Errorf("Same name should generate same aggression: %f vs %f", p1.Aggression, p2.Aggression)
	}
	if p1.Conservatism != p2.Conservatism {
		t.Errorf("Same name should generate same conservatism: %f vs %f", p1.Conservatism, p2.Conservatism)
	}
	if p1.Patience != p2.Patience {
		t.Errorf("Same name should generate same patience: %f vs %f", p1.Patience, p2.Patience)
	}

	// 不同名称应生成不同性格（大概率）
	p3 := createBotPersonality("BotB")
	if p1.Aggression == p3.Aggression && p1.Conservatism == p3.Conservatism {
		t.Log("Warning: Different names generated identical personalities (unlikely but possible)")
	}
}

// TestPersonalityClamping 测试参数钳制
func TestPersonalityClamping(t *testing.T) {
	// 超出范围的参数应被钳制
	p := NewBotPersonality(1.5, -0.5, 2.0)

	if p.Aggression > 1.0 {
		t.Errorf("Aggression should be clamped to 1.0, got %f", p.Aggression)
	}
	if p.Conservatism < 0.0 {
		t.Errorf("Conservatism should be clamped to 0.0, got %f", p.Conservatism)
	}
	if p.Patience > 1.0 {
		t.Errorf("Patience should be clamped to 1.0, got %f", p.Patience)
	}
}

// BenchmarkCalculateThinkDelay 性能测试：动态延迟计算
func BenchmarkCalculateThinkDelay(b *testing.B) {
	p := NewBotPersonality(0.5, 0.5, 0.5)
	hand := []int{0, 1, 2, 9, 10, 18, 19, 20, 27, 27, 4, 5, 6}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.CalculateThinkDelay(hand, 10, false)
	}
}

// BenchmarkRecordPlayerBehavior 性能测试：玩家行为记录
func BenchmarkRecordPlayerBehavior(b *testing.B) {
	p := NewBotPersonality(0.5, 0.5, 0.5)
	discards := make([]int, 20)
	for i := range discards {
		discards[i] = i % rule.NumTypes
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.RecordPlayerBehavior("player1", discards, i%2 == 0)
	}
}
