package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"xgames/internal/games"
	"xgames/internal/games/mahjong/msg"
	"xgames/internal/games/mahjong/rule"
	"xgames/internal/platform/protocol"
)

// --- 语音节奏预算（docs/architecture.md §10.3）单测 ---
// 覆盖：预算内节拍被延迟执行（A/B 拍）、托管取消回退到 askClaim/askSelfDrawWin、
// 代次失效（取消后到期事件不执行）、等待窗口内提交出牌被拒。
// 测试加速：in-package 直接把 minActionGap 调小（100ms），无需真等 1.5s。

// recorded 一次广播/定向发送的记录（to 为空表示广播）
type recorded struct {
	to  string
	typ protocol.MessageType
}

// recorderBroadcaster 记录全部出站消息的假 Broadcaster
type recorderBroadcaster struct {
	mu   sync.Mutex
	msgs []recorded
}

func (r *recorderBroadcaster) Broadcast(m protocol.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, recorded{typ: m.Type})
}

func (r *recorderBroadcaster) SendTo(playerID string, m protocol.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, recorded{to: playerID, typ: m.Type})
}

func (r *recorderBroadcaster) count(to string, typ protocol.MessageType) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, m := range r.msgs {
		if m.typ == typ && (to == "" || m.to == to) {
			n++
		}
	}
	return n
}

// countStaysWithin 断言 timeout 内指定消息的数量保持不超过 n（开局广播不计入）
func (r *recorderBroadcaster) countStaysWithin(t *testing.T, to string, typ protocol.MessageType, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		require.LessOrEqual(t, r.count(to, typ), n, "消息 %s（to=%q）数量不应超过 %d", typ, to, n)
		time.Sleep(2 * time.Millisecond)
	}
}

// waitForCount 断言 timeout 内指定消息数量达到 n
func (r *recorderBroadcaster) waitForCount(t *testing.T, to string, typ protocol.MessageType, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if r.count(to, typ) >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("等待消息 %s（to=%q）数量达到 %d 超时", typ, to, n)
}

// buildWall 按段落拼接确定性牌墙（前段依次为各用手牌/摸牌），其余用剩余牌填充
func buildWall(segments ...[]int) []int {
	wall := make([]int, 0, rule.TotalTiles)
	used := make(map[int]int)
	for _, seg := range segments {
		wall = append(wall, seg...)
		for _, tile := range seg {
			used[tile]++
		}
	}
	for t := 0; t < rule.NumTypes; t++ {
		for c := used[t]; c < rule.Copies; c++ {
			wall = append(wall, t)
		}
	}
	return wall
}

const pacingTestGap = 100 * time.Millisecond

// newPacingTestSession 构建双人局：p0 真人（确定性当庄），p1 由用例注入。
// 牌墙布局：p0 初始 13 张 → p1 初始 13 张 → p0 第 14 张（庄家摸）→ p1 首摸 → 填充。
func newPacingTestSession(t *testing.T, p1 Player, p0Hand, p1Hand, p0DealerDraw, p1FirstDraw []int) (*Session, *recorderBroadcaster) {
	t.Helper()

	// 测试加速：缩小节奏预算（in-package 直接覆盖）
	oldGap := minActionGap
	minActionGap = pacingTestGap
	t.Cleanup(func() { minActionGap = oldGap })

	// 确定性骰子：两家同点，庄家取先达到者 = 座位 0（p0）
	rule.DiceRoller = func() int { return 6 }
	t.Cleanup(func() { rule.DiceRoller = nil })

	rule.WallGenerator = func() []int {
		return buildWall(p0Hand, p1Hand, p0DealerDraw, p1FirstDraw)
	}
	t.Cleanup(func() { rule.WallGenerator = nil })

	bc := &recorderBroadcaster{}
	gs := New("paceTest", []*Player{
		{ID: "p0", Name: "真人0", IsBot: false},
		&p1,
	}, bc, nil, games.Timeouts{Turn: 2 * time.Second, OfflineWait: 10 * time.Second})
	gs.playerLogger = nil // 关闭复盘记录，避免测试向 data/replays 写文件

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		select {
		case <-gs.Done():
		case <-time.After(2 * time.Second):
		}
	})
	go gs.Run(ctx)
	return gs, bc
}

// TestPacingAutoPongDelayed A 拍：弃牌→bot 碰，在预算内被延迟执行（到期才广播 PongMade）
func TestPacingAutoPongDelayed(t *testing.T) {
	p0Hand := []int{5, 9, 9, 9, 10, 10, 10, 20, 20, 20, 30, 31, 32}
	p1Hand := []int{5, 5, 9, 10, 11, 18, 19, 20, 27, 28, 29, 30, 31}
	gs, bc := newPacingTestSession(t, Player{ID: "p1", Name: "bot1", IsBot: true},
		p0Hand, p1Hand, []int{6}, []int{7})

	// p0（庄）弃 5：p1 两张 5 可碰（手牌+5 不构成胡/杠）
	require.NoError(t, gs.Discard("p0", 5))

	// 预算内不执行
	bc.countStaysWithin(t, "", msg.MsgMjPongMade, 0, pacingTestGap/3)
	// 到期后执行
	bc.waitForCount(t, "", msg.MsgMjPongMade, 1, 2*pacingTestGap)
	// 碰后许可移交 p1 并广播回合（开局回合广播 1 次 + 碰后 1 次）
	bc.waitForCount(t, "", msg.MsgMjTurn, 2, 2*pacingTestGap)
}

// TestPacingAutoTurnAfterDrawDelayed B 拍：无人吃碰胡→下家 bot 摸牌，回合通知在预算内被延迟
func TestPacingAutoTurnAfterDrawDelayed(t *testing.T) {
	p0Hand := []int{20, 9, 9, 9, 10, 10, 10, 21, 21, 21, 30, 31, 32}
	p1Hand := []int{5, 6, 7, 14, 15, 16, 23, 24, 25, 27, 28, 29, 30}
	gs, bc := newPacingTestSession(t, Player{ID: "p1", Name: "bot1", IsBot: true},
		p0Hand, p1Hand, []int{19}, []int{8})

	// p0 弃 20：p1 无法吃碰胡 → p1 摸 8（不胡/不杠）
	require.NoError(t, gs.Discard("p0", 20))

	// 摸牌定向发送照旧（不等待预算）
	bc.waitForCount(t, "p1", msg.MsgMjDraw, 1, 2*pacingTestGap)
	// 回合通知在预算内被延迟（开局广播 1 次），到期后广播第 2 次
	bc.countStaysWithin(t, "", msg.MsgMjTurn, 1, pacingTestGap/3)
	bc.waitForCount(t, "", msg.MsgMjTurn, 2, 2*pacingTestGap)
}

// TestPacingGuardRejectsDiscardInWindow 等待窗口内提交出牌被拒（守卫生效）
func TestPacingGuardRejectsDiscardInWindow(t *testing.T) {
	p0Hand := []int{20, 9, 9, 9, 10, 10, 10, 21, 21, 21, 30, 31, 32}
	p1Hand := []int{5, 6, 7, 14, 15, 16, 23, 24, 25, 27, 28, 29, 30}
	gs, bc := newPacingTestSession(t, Player{ID: "p1", Name: "bot1", IsBot: true},
		p0Hand, p1Hand, []int{19}, []int{8})

	require.NoError(t, gs.Discard("p0", 20)) // 进入 B 拍等待窗口（p1 挂起）

	// 窗口内 p1（挂起玩家，currentTurn 已置为 p1）提交出牌：守卫拒绝
	err := gs.Discard("p1", 5)
	require.ErrorIs(t, err, ErrNotAllowed)

	// 到期后流程正常：回合通知广播（开局 1 次 + p1 出牌回合 1 次）
	bc.waitForCount(t, "", msg.MsgMjTurn, 2, 2*pacingTestGap)
}

// TestPacingCancelFallbackToAskClaim A 拍取消回退：托管玩家在等待窗口关闭托管 → 转回真人询问（askClaim）
func TestPacingCancelFallbackToAskClaim(t *testing.T) {
	p0Hand := []int{5, 9, 9, 9, 10, 10, 10, 20, 20, 20, 30, 31, 32}
	p1Hand := []int{5, 5, 9, 10, 11, 18, 19, 20, 27, 28, 29, 30, 31}
	gs, bc := newPacingTestSession(t, Player{ID: "p1", Name: "玩家1", Afk: true},
		p0Hand, p1Hand, []int{6}, []int{7})

	require.NoError(t, gs.Discard("p0", 5)) // p1（托管）可碰 → 挂起

	// 窗口内关闭托管：取消挂起动作并回退为真人询问
	require.NoError(t, gs.SetAfk("p1", false))
	bc.waitForCount(t, "p1", msg.MsgMjActionAvail, 1, 2*pacingTestGap)
	// 自动碰不再执行
	bc.countStaysWithin(t, "", msg.MsgMjPongMade, 0, 2*pacingTestGap)

	// 代次失效：迟到的到期事件（当前代次之前的 gen）不执行任何动作
	gs.post(autoActionEvent{gen: gs.actionGen - 1})
	bc.countStaysWithin(t, "", msg.MsgMjPongMade, 0, 2*pacingTestGap)

	// 真人过 → 流程继续推进到 p1 摸牌出牌
	require.NoError(t, gs.Pass("p1"))
	bc.waitForCount(t, "", msg.MsgMjTurn, 2, 2*pacingTestGap)
}

// TestPacingCancelFallbackToAskSelfDrawWin B 拍取消回退：摸牌后自摸胡的托管玩家关闭托管 → askSelfDrawWin，可手动胡
func TestPacingCancelFallbackToAskSelfDrawWin(t *testing.T) {
	p0Hand := []int{20, 21, 22, 9, 9, 9, 10, 10, 10, 30, 31, 32, 33}
	p1Hand := []int{0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6} // 七对听 6
	gs, bc := newPacingTestSession(t, Player{ID: "p1", Name: "玩家1", Afk: true},
		p0Hand, p1Hand, []int{19}, []int{6})

	// p0 弃 20：p1 无动作 → p1 摸 6 自摸胡（托管 → 挂起）
	require.NoError(t, gs.Discard("p0", 20))

	// 窗口内关闭托管：回退为真人自摸胡询问
	require.NoError(t, gs.SetAfk("p1", false))
	bc.waitForCount(t, "p1", msg.MsgMjActionAvail, 1, 2*pacingTestGap)
	bc.countStaysWithin(t, "", protocol.MsgGameOver, 0, 2*pacingTestGap)

	// 真人手动胡 → 正常结算
	require.NoError(t, gs.Win("p1", true))
	bc.waitForCount(t, "", protocol.MsgGameOver, 1, 2*pacingTestGap)
}
