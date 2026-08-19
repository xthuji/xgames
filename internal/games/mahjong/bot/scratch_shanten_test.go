package bot

import (
	"testing"

	"xgames/internal/games/mahjong/rule"
)

func TestScratchShanten(t *testing.T) {
	pin1, pin2 := 9, 10 // 筒子 9-17，difficulty_test.go 仅定义了 pin3~pin9
	// 13 张听牌：三组万子 + 1-2-3筒 + 4筒单骑（听 4筒）
	tenpai := []int{man1, man2, man3, man4, man5, man6, man7, man8, man9, pin1, pin2, pin3, pin4}
	t.Logf("tenpai handBestShanten=%d", handBestShanten(tenpai))
	// 一向听：把 4筒换成北
	one := []int{man1, man2, man3, man4, man5, man6, man7, man8, man9, pin1, pin2, pin3, north}
	t.Logf("one-away handBestShanten=%d", handBestShanten(one))
	// 两向听：再换一张
	two := []int{man1, man2, man3, man4, man5, man6, man7, man8, man9, pin1, pin2, north, east}
	t.Logf("two-away handBestShanten=%d", handBestShanten(two))
	// 14 张（摸牌后）听牌
	tenpai14 := append(append([]int(nil), tenpai...), north)
	t.Logf("tenpai14 handBestShanten=%d", handBestShanten(tenpai14))
	_ = rule.NumTypes
}
