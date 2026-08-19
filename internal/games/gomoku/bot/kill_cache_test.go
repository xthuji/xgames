package bot

import "testing"

func TestKillCache_ProbeDepthIntervalSemantics(t *testing.T) {
	kc := newKillCache()

	// 未存储的 key 不命中
	if _, ok := kc.probe(999, 5); ok {
		t.Fatal("未存储的 key 不应命中")
	}

	// true 结论：剩余深度更充足（>= 记录值）时命中
	kc.store(1, 5, true)
	if win, ok := kc.probe(1, 8); !ok || !win {
		t.Fatalf("剩余深度 8 >= 记录 5 应命中 true，实际 ok=%v win=%v", ok, win)
	}
	// 剩余深度不足时不得命中（浅层下未必能赢）
	if _, ok := kc.probe(1, 3); ok {
		t.Fatal("剩余深度 3 < 记录 5 不应命中 true 结论")
	}

	// false 结论：剩余深度更浅（<= 记录值）时命中
	kc.store(2, 5, false)
	if win, ok := kc.probe(2, 3); !ok || win {
		t.Fatalf("剩余深度 3 <= 记录 5 应命中 false，实际 ok=%v win=%v", ok, win)
	}
	// 剩余深度更深时不得命中（更深下可能赢）
	if _, ok := kc.probe(2, 7); ok {
		t.Fatal("剩余深度 7 > 记录 5 不应命中 false 结论")
	}
}

func TestTranspositionTable_BestMoveRoundTrip(t *testing.T) {
	tt := newTranspositionTable()

	// 带 bestMove 写入后应原样读回
	tt.put(7, 100, 3, boundExact, move{row: 5, col: 6}, true)
	res := tt.get(7, 1)
	if !res.ok || res.value != 100 || res.depth != 3 || res.bound != boundExact {
		t.Fatalf("基本字段回读失败: %+v", res)
	}
	if !res.hasBest || res.bestMove.row != 5 || res.bestMove.col != 6 {
		t.Fatalf("bestMove 回读失败: hasBest=%v bestMove=%+v", res.hasBest, res.bestMove)
	}

	// 无 bestMove 的写入（叶/终局节点）
	tt.put(8, -5, 2, boundUpper, move{}, false)
	res2 := tt.get(8, 1)
	if !res2.ok || res2.value != -5 || res2.hasBest {
		t.Fatalf("无 bestMove 条目回读失败: %+v", res2)
	}

	// 更深搜索结果覆盖浅条目时保留新 bestMove
	tt.put(7, 120, 4, boundExact, move{row: 1, col: 2}, true)
	res3 := tt.get(7, 1)
	if res3.value != 120 || res3.bestMove.row != 1 || res3.bestMove.col != 2 {
		t.Fatalf("更深条目应覆盖: %+v", res3)
	}
}
