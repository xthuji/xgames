package session

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"xgames/internal/games"
	"xgames/internal/games/gomoku/bot"
	gkmsg "xgames/internal/games/gomoku/msg"
	"xgames/internal/games/gomoku/rule"
	"xgames/internal/platform/protocol"
	"xgames/internal/testutil"
)

// TestMain 将工作目录切到项目根：对局结束后异步落盘的复盘报告按相对路径
// data/replays 解析，go test 的 cwd 是包目录，不切换会把报告写到包目录下
func TestMain(m *testing.M) {
	testutil.ChdirProjectRoot()
	os.Exit(m.Run())
}

// --- 测试基础设施 ---

type testBroadcaster struct {
	mu         sync.Mutex
	broadcasts []protocol.Message
}

func newTestBroadcaster() *testBroadcaster { return &testBroadcaster{} }

func (b *testBroadcaster) Broadcast(msg protocol.Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.broadcasts = append(b.broadcasts, msg)
}

func (b *testBroadcaster) SendTo(string, protocol.Message) {}

// nextBroadcast 从 fromIdx 起等待下一条指定类型广播
func (b *testBroadcaster) nextBroadcast(t *testing.T, mt protocol.MessageType, fromIdx int, within time.Duration) (protocol.Message, int) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		for i := fromIdx; i < len(b.broadcasts); i++ {
			if b.broadcasts[i].Type == mt {
				msg := b.broadcasts[i]
				b.mu.Unlock()
				return msg, i + 1
			}
		}
		b.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待广播 %s 超时", mt)
	return protocol.Message{}, fromIdx
}

func (b *testBroadcaster) countBroadcasts(mt protocol.MessageType) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, m := range b.broadcasts {
		if m.Type == mt {
			n++
		}
	}
	return n
}

func decode[T any](t *testing.T, msg protocol.Message) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(msg.Payload, &v))
	return v
}

type testSink struct {
	mu      sync.Mutex
	results []games.GameResult
}

func (s *testSink) OnGameOver(r games.GameResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results = append(s.results, r)
}

func (s *testSink) last(t *testing.T) games.GameResult {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.NotEmpty(t, s.results)
	return s.results[len(s.results)-1]
}

// stubEngine 顺序返回预设落子的决策引擎
type stubEngine struct {
	mu    sync.Mutex
	moves [][2]int
}

func (e *stubEngine) DecideMove(_ context.Context, _ string, _ []int, _ int) (int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.moves) == 0 {
		return -1, -1 // 非法，触发会话侧兜底
	}
	m := e.moves[0]
	e.moves = e.moves[1:]
	return m[0], m[1]
}

func testTimeouts() Timeouts {
	return Timeouts{Turn: 3 * time.Second, OfflineWait: 3 * time.Second}
}

// newHarness 两人对局（A=0 黑 / B=1 白），可选注入引擎与结算接收器
func newHarness(t *testing.T, to Timeouts, engine bot.DecisionEngine, sink ResultSink) (*Session, *testBroadcaster) {
	t.Helper()
	players := []*Player{
		{ID: "A", Name: "玩家A", Seat: 0},
		{ID: "B", Name: "玩家B", Seat: 1},
	}
	bc := newTestBroadcaster()
	gs := New("000000", players, bc, sink, to)
	if engine != nil {
		gs.SetEngine(engine)
	}
	go gs.Run(context.Background())
	t.Cleanup(gs.Stop)
	return gs, bc
}

func moveMsg(row, col int) protocol.Message {
	return protocol.NewMessage(gkmsg.MsgGkMove, gkmsg.GkMovePayload{Row: row, Col: col})
}

func afkMsg(afk bool) protocol.Message {
	return protocol.NewMessage(gkmsg.MsgGkAfk, gkmsg.GkAfkPayload{Afk: afk})
}

// --- 对局流程 ---

func TestGameFlow_BlackWinsHorizontalFive(t *testing.T) {
	sink := &testSink{}
	gs, bc := newHarness(t, testTimeouts(), nil, sink)

	// 开局：黑方（A）先手
	m, idx := bc.nextBroadcast(t, gkmsg.MsgGkTurn, 0, time.Second)
	turn := decode[gkmsg.GkTurnPayload](t, m)
	assert.Equal(t, "A", turn.PlayerID)
	assert.Equal(t, 1, turn.MoveNumber)

	// 交替落子：A 在 7 行横向布五连，B 在 0 行散点
	aMoves := [][2]int{{7, 3}, {7, 4}, {7, 5}, {7, 6}, {7, 7}}
	bMoves := [][2]int{{0, 0}, {0, 2}, {0, 4}, {0, 6}}
	for i, am := range aMoves {
		require.NoError(t, gs.OnMessage("A", moveMsg(am[0], am[1])))
		mm, idx2 := bc.nextBroadcast(t, gkmsg.MsgGkMoveMade, idx, time.Second)
		made := decode[gkmsg.GkMoveMadePayload](t, mm)
		assert.Equal(t, "A", made.PlayerID)
		assert.Equal(t, rule.Black, made.Color)
		assert.Equal(t, am[0], made.Row)
		assert.Equal(t, am[1], made.Col)
		idx = idx2

		if i == len(aMoves)-1 {
			break // 第五手获胜，无需白方回合
		}
		require.NoError(t, gs.OnMessage("B", moveMsg(bMoves[i][0], bMoves[i][1])))
		_, idx = bc.nextBroadcast(t, gkmsg.MsgGkMoveMade, idx, time.Second)
	}

	// 结算
	m, _ = bc.nextBroadcast(t, protocol.MsgGameOver, idx, time.Second)
	over := decode[protocol.GameOverPayload](t, m)
	assert.Equal(t, "A", over.WinnerID)

	res := sink.last(t)
	assert.Equal(t, "000000", res.RoomCode)
	require.Len(t, res.Players, 2)
	assert.Equal(t, 1, res.Players[0].Score)  // 胜者 +1
	assert.Equal(t, -1, res.Players[1].Score) // 败者 -1

	// 终局后落子被拒（对局已结算，Actor 已退出）
	err := gs.OnMessage("B", moveMsg(1, 1))
	require.Error(t, err)
	assert.Equal(t, protocol.ErrCodeGameNotStart, games.CodeOf(err))
}

func TestMoveValidation(t *testing.T) {
	gs, bc := newHarness(t, testTimeouts(), nil, nil)
	bc.nextBroadcast(t, gkmsg.MsgGkTurn, 0, time.Second)

	t.Run("非本方回合", func(t *testing.T) {
		err := gs.OnMessage("B", moveMsg(7, 7))
		require.Error(t, err)
		assert.Equal(t, gkmsg.ErrCodeNotYourTurn, games.CodeOf(err))
	})

	t.Run("越界", func(t *testing.T) {
		err := gs.OnMessage("A", moveMsg(rule.Size, 0))
		require.Error(t, err)
		assert.Equal(t, gkmsg.ErrCodeOutOfBounds, games.CodeOf(err))
	})

	t.Run("合法落子后重复位置被拒", func(t *testing.T) {
		require.NoError(t, gs.OnMessage("A", moveMsg(7, 7)))
		require.NoError(t, gs.OnMessage("B", moveMsg(7, 8)))
		err := gs.OnMessage("A", moveMsg(7, 7))
		require.Error(t, err)
		assert.Equal(t, gkmsg.ErrCodeOccupied, games.CodeOf(err))
	})
}

func TestUnknownMessageRejected(t *testing.T) {
	gs, bc := newHarness(t, testTimeouts(), nil, nil)
	bc.nextBroadcast(t, gkmsg.MsgGkTurn, 0, time.Second)

	err := gs.OnMessage("A", protocol.NewMessage(protocol.MsgPing, nil))
	require.Error(t, err)
	assert.Equal(t, protocol.ErrCodeInvalidMsg, games.CodeOf(err))
}

// --- 挂机与托管 ---

func TestAfk_TakeoverByEngine(t *testing.T) {
	engine := &stubEngine{moves: [][2]int{{8, 8}}}
	gs, bc := newHarness(t, testTimeouts(), engine, nil)
	bc.nextBroadcast(t, gkmsg.MsgGkTurn, 0, time.Second)

	// A 主动挂机
	require.NoError(t, gs.OnMessage("A", afkMsg(true)))
	m, idx := bc.nextBroadcast(t, gkmsg.MsgGkAfkChanged, 0, time.Second)
	assert.True(t, decode[gkmsg.GkAfkChangedPayload](t, m).Afk)

	// 挂机后约 1s 由引擎代落
	m, _ = bc.nextBroadcast(t, gkmsg.MsgGkMoveMade, idx, 3*time.Second)
	made := decode[gkmsg.GkMoveMadePayload](t, m)
	assert.Equal(t, "A", made.PlayerID)
	assert.Equal(t, [2]int{made.Row, made.Col}, [2]int{8, 8})

	// 回合已移交给 B
	m, _ = bc.nextBroadcast(t, gkmsg.MsgGkTurn, idx, time.Second)
	assert.Equal(t, "B", decode[gkmsg.GkTurnPayload](t, m).PlayerID)
}

func TestTurnTimeout_AutoAfkAndAutoplay(t *testing.T) {
	// 短回合超时：在线玩家超时自动转挂机并由兜底策略代落
	gs, bc := newHarness(t, Timeouts{Turn: 200 * time.Millisecond, OfflineWait: 3 * time.Second}, nil, nil)
	bc.nextBroadcast(t, gkmsg.MsgGkTurn, 0, time.Second)

	// 超时 → 挂机广播 + 托管落子
	m, idx := bc.nextBroadcast(t, gkmsg.MsgGkAfkChanged, 0, 3*time.Second)
	assert.True(t, decode[gkmsg.GkAfkChangedPayload](t, m).Afk)
	m, _ = bc.nextBroadcast(t, gkmsg.MsgGkMoveMade, idx, 3*time.Second)
	assert.Equal(t, "A", decode[gkmsg.GkMoveMadePayload](t, m).PlayerID)

	// B 落子后又是 A 的回合：A 主动行动自动退出挂机
	require.NoError(t, gs.OnMessage("B", moveMsg(0, 0)))
	require.NoError(t, gs.OnMessage("A", moveMsg(0, 1)))
	m, _ = bc.nextBroadcast(t, gkmsg.MsgGkAfkChanged, idx, 3*time.Second)
	cg := decode[gkmsg.GkAfkChangedPayload](t, m)
	assert.Equal(t, "A", cg.PlayerID)
	assert.False(t, cg.Afk)
}

// --- 离线 ---

func TestOffline_PauseResume(t *testing.T) {
	// Turn=600ms 离线暂停，重连后剩余时间恢复
	gs, bc := newHarness(t, Timeouts{Turn: 600 * time.Millisecond, OfflineWait: 5 * time.Second}, nil, nil)
	bc.nextBroadcast(t, gkmsg.MsgGkTurn, 0, time.Second)

	gs.PlayerOffline("A")
	time.Sleep(800 * time.Millisecond) // 若不暂停，600ms 已超时托管
	assert.Equal(t, 0, bc.countBroadcasts(gkmsg.MsgGkMoveMade), "离线期间不应自动落子")

	// 重连恢复计时并行动
	gs.PlayerOnline("A")
	require.NoError(t, gs.OnMessage("A", moveMsg(7, 7)))
	m, _ := bc.nextBroadcast(t, gkmsg.MsgGkMoveMade, 0, time.Second)
	assert.Equal(t, "A", decode[gkmsg.GkMoveMadePayload](t, m).PlayerID)
}

func TestOffline_TimeoutTriggersAutoplay(t *testing.T) {
	gs, bc := newHarness(t, Timeouts{Turn: 3 * time.Second, OfflineWait: 200 * time.Millisecond}, nil, nil)
	bc.nextBroadcast(t, gkmsg.MsgGkTurn, 0, time.Second)

	gs.PlayerOffline("A")
	// 离线等待超时 → 转挂机并自动落子
	m, idx := bc.nextBroadcast(t, gkmsg.MsgGkAfkChanged, 0, 3*time.Second)
	assert.True(t, decode[gkmsg.GkAfkChangedPayload](t, m).Afk)
	m, _ = bc.nextBroadcast(t, gkmsg.MsgGkMoveMade, idx, 3*time.Second)
	assert.Equal(t, "A", decode[gkmsg.GkMoveMadePayload](t, m).PlayerID)
}

// --- 状态恢复 ---

func TestGameStateAndSnapshotRestore(t *testing.T) {
	gs, bc := newHarness(t, testTimeouts(), nil, nil)
	bc.nextBroadcast(t, gkmsg.MsgGkTurn, 0, time.Second)
	require.NoError(t, gs.OnMessage("A", moveMsg(7, 7)))
	bc.nextBroadcast(t, gkmsg.MsgGkMoveMade, 0, time.Second)
	require.NoError(t, gs.OnMessage("B", moveMsg(8, 8)))
	bc.nextBroadcast(t, gkmsg.MsgGkMoveMade, 0, time.Second)

	// 玩家视角状态
	dto := gs.GameStateFor("B")
	require.NotNil(t, dto)
	assert.Equal(t, "playing", dto.Phase)
	assert.Equal(t, "A", dto.CurrentTurn)
	assert.Equal(t, rule.White, dto.MyColor)
	assert.Equal(t, rule.Black, dto.Board[rule.Idx(7, 7)])
	assert.Equal(t, rule.White, dto.Board[rule.Idx(8, 8)])
	assert.Equal(t, 3, dto.MoveNumber)

	// StateMessage 可用性
	_, ok := gs.StateMessage("B")
	assert.True(t, ok)
	require.NotNil(t, gs.GameStateJSON("B"))

	// 快照 → 恢复
	snap := gs.SnapshotState()
	require.NotNil(t, snap)
	gs.Stop()

	bc2 := newTestBroadcaster()
	gs2, err := RestoreFromSnapshot(snap, bc2, nil, testTimeouts())
	require.NoError(t, err)
	go gs2.Run(context.Background())
	t.Cleanup(gs2.Stop)

	// 恢复后广播回合通知（轮到 A），状态一致
	m, _ := bc2.nextBroadcast(t, gkmsg.MsgGkTurn, 0, time.Second)
	turn := decode[gkmsg.GkTurnPayload](t, m)
	assert.Equal(t, "A", turn.PlayerID)
	assert.Equal(t, 3, turn.MoveNumber)

	dto2 := gs2.GameStateFor("A")
	require.NotNil(t, dto2)
	assert.Equal(t, rule.Black, dto2.Board[rule.Idx(7, 7)])
	assert.Equal(t, rule.White, dto2.Board[rule.Idx(8, 8)])
}

func TestRestoreFromSnapshot_RejectsBadInput(t *testing.T) {
	_, err := RestoreFromSnapshot(nil, newTestBroadcaster(), nil, testTimeouts())
	require.Error(t, err)

	bad := &Snapshot{Phase: "playing", Players: make([]SnapshotPlayer, 3), Board: rule.NewBoard()}
	_, err = RestoreFromSnapshot(bad, newTestBroadcaster(), nil, testTimeouts())
	require.Error(t, err)
}

// TestGameFlow_DrawByNoWinPossible 僵局判和：双方均无连五可能时（棋盘尚余 1 空位）立即判平，
// 并广播 DrawReason=no_win_possible。
// 终局图案构造：基底 (r+2c)%5∈{0,1} 为黑（90 子），再翻转 22 个 (r+2c)%5==2 的白子
// （r 为偶数的前 22 个）补至 112，(0,4) 留空；任意方向 5 连窗口仍必含黑白两色，
// 黑白各 112 子 → 224 手可严格交替走完，全程不会成五。
func TestGameFlow_DrawByNoWinPossible(t *testing.T) {
	sink := &testSink{}
	gs, bc := newHarness(t, testTimeouts(), nil, sink)
	bc.nextBroadcast(t, gkmsg.MsgGkTurn, 0, time.Second)

	blackCells, whiteCells := make([][2]int, 0, 112), make([][2]int, 0, 112)
	flipped := 0
	for r := 0; r < rule.Size; r++ {
		for c := 0; c < rule.Size; c++ {
			if r == 0 && c == 4 {
				continue // 终局留空位
			}
			switch m := (r + 2*c) % 5; {
			case m < 2:
				blackCells = append(blackCells, [2]int{r, c})
			case m == 2 && r%2 == 0 && flipped < 22:
				blackCells = append(blackCells, [2]int{r, c})
				flipped++
			default:
				whiteCells = append(whiteCells, [2]int{r, c})
			}
		}
	}
	require.Len(t, blackCells, 112)
	require.Len(t, whiteCells, 112)

	// 严格交替落子；对局结束后后续落子会报错，直接停止
	play := func(id string, cell [2]int) bool {
		return gs.OnMessage(id, moveMsg(cell[0], cell[1])) == nil
	}
	for i := 0; i < 112; i++ {
		if !play("A", blackCells[i]) || !play("B", whiteCells[i]) {
			break
		}
	}

	m, _ := bc.nextBroadcast(t, protocol.MsgGameOver, 0, time.Second)
	over := decode[protocol.GameOverPayload](t, m)
	assert.Empty(t, over.WinnerID)
	assert.Equal(t, rule.DrawReasonNoWinPossible, over.DrawReason)

	res := sink.last(t)
	require.Len(t, res.Players, 2)
	assert.Equal(t, 0, res.Players[0].Score) // 平局双方均 0 分
	assert.Equal(t, 0, res.Players[1].Score)
}
