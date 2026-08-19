package bot

import (
	"sync"
	"testing"
	"time"

	"xgames/internal/games/gomoku/msg"
	"xgames/internal/games/gomoku/rule"
	"xgames/internal/platform/protocol"
)

// fakeSession 记录机器人落子动作的 BotResponder 桩
type fakeSession struct {
	mu    sync.Mutex
	moves map[string][]msg.GkMovePayload
}

func newFakeSession() *fakeSession {
	return &fakeSession{moves: make(map[string][]msg.GkMovePayload)}
}

func (f *fakeSession) SubmitBotAction(playerID string, m protocol.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m.Type != msg.MsgGkMove {
		return nil
	}
	p, _ := protocol.ParsePayload[msg.GkMovePayload](m)
	f.moves[playerID] = append(f.moves[playerID], p)
	return nil
}

func (f *fakeSession) lastMove(playerID string) (msg.GkMovePayload, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ms := f.moves[playerID]
	if len(ms) == 0 {
		return msg.GkMovePayload{}, false
	}
	return ms[len(ms)-1], true
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	// 引擎搜索时间预算为 2s（searchTimeMs），预算检查有时间粒度余量，
	// 且全量并行测试时 CPU 被抢占会进一步拉长实际耗时，3s 上限会偶发超时；
	// 本测试只验证“机器人会响应”，不约束响应时长，故放宽到 10s
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待机器人动作超时")
}

// setup 构造人机对局跟踪：b1 机器人执白（1 号座），p0 真人执黑
func setup(t *testing.T) (*Controller, *fakeSession) {
	t.Helper()
	ctrl := NewController(NewEngine(discardLogger()), discardLogger())
	ctrl.SetDelay(func() time.Duration { return 0 })
	resp := newFakeSession()

	ctrl.OnBroadcast("R1", protocol.NewMessage(protocol.MsgGameStart, protocol.GameStartPayload{
		Players: []protocol.PlayerInfo{
			{ID: "p0", Name: "玩家", Seat: 0},
			{ID: "b1", Name: "BOT-0001", Seat: 1, IsBot: true},
		},
	}), resp)
	return ctrl, resp
}

func TestController_BotRespondsOnItsTurn(t *testing.T) {
	ctrl, resp := setup(t)

	// 真人首手
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgGkMoveMade, msg.GkMoveMadePayload{
		PlayerID: "p0", Row: 7, Col: 7, Color: rule.Black, MoveNumber: 1,
	}), resp)

	// 轮到机器人（白）
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgGkTurn, msg.GkTurnPayload{
		PlayerID: "b1", Timeout: 30, MoveNumber: 2,
	}), resp)

	waitUntil(t, func() bool { _, ok := resp.lastMove("b1"); return ok })
	mv, _ := resp.lastMove("b1")
	if !rule.InBounds(mv.Row, mv.Col) {
		t.Fatalf("机器人落子越界 (%d,%d)", mv.Row, mv.Col)
	}
	if mv.Row == 7 && mv.Col == 7 {
		t.Fatal("机器人不应落在已有棋子处")
	}
}

func TestController_IgnoresHumanTurn(t *testing.T) {
	ctrl, resp := setup(t)

	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgGkTurn, msg.GkTurnPayload{
		PlayerID: "p0", Timeout: 30, MoveNumber: 1,
	}), resp)

	time.Sleep(100 * time.Millisecond)
	if _, ok := resp.lastMove("b1"); ok {
		t.Fatal("真人回合机器人不应行动")
	}
	if _, ok := resp.lastMove("p0"); ok {
		t.Fatal("控制器不应代真人行动")
	}
}

func TestController_TracksBoardAcrossTurns(t *testing.T) {
	ctrl, resp := setup(t)

	// 黑 7 行四连，逼白棋必须堵 (7,4)/(7,9)
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgGkMoveMade, msg.GkMoveMadePayload{PlayerID: "p0", Row: 7, Col: 5, Color: rule.Black, MoveNumber: 1}), resp)
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgGkMoveMade, msg.GkMoveMadePayload{PlayerID: "b1", Row: 0, Col: 0, Color: rule.White, MoveNumber: 2}), resp)
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgGkMoveMade, msg.GkMoveMadePayload{PlayerID: "p0", Row: 7, Col: 6, Color: rule.Black, MoveNumber: 3}), resp)
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgGkMoveMade, msg.GkMoveMadePayload{PlayerID: "b1", Row: 0, Col: 1, Color: rule.White, MoveNumber: 4}), resp)
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgGkMoveMade, msg.GkMoveMadePayload{PlayerID: "p0", Row: 7, Col: 7, Color: rule.Black, MoveNumber: 5}), resp)
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgGkMoveMade, msg.GkMoveMadePayload{PlayerID: "b1", Row: 0, Col: 2, Color: rule.White, MoveNumber: 6}), resp)
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgGkMoveMade, msg.GkMoveMadePayload{PlayerID: "p0", Row: 7, Col: 8, Color: rule.Black, MoveNumber: 7}), resp)

	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgGkTurn, msg.GkTurnPayload{
		PlayerID: "b1", Timeout: 30, MoveNumber: 8,
	}), resp)

	waitUntil(t, func() bool { _, ok := resp.lastMove("b1"); return ok })
	mv, _ := resp.lastMove("b1")
	// 决策基于跟踪的完整棋盘（含 b1 自己的三手 0 行棋 + 黑四连），应优先堵四
	if !(mv.Row == 7 && (mv.Col == 4 || mv.Col == 9)) {
		t.Fatalf("应堵黑棋四连 (7,4)/(7,9)，实际 (%d,%d)", mv.Row, mv.Col)
	}
}

func TestController_CleanupOnGameOver(t *testing.T) {
	ctrl, resp := setup(t)

	ctrl.OnBroadcast("R1", protocol.NewMessage(protocol.MsgGameOver, protocol.GameOverPayload{WinnerID: "p0"}), resp)

	// 对局清理后回合消息不再触发动作
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgGkTurn, msg.GkTurnPayload{
		PlayerID: "b1", Timeout: 30, MoveNumber: 2,
	}), resp)

	time.Sleep(100 * time.Millisecond)
	if _, ok := resp.lastMove("b1"); ok {
		t.Fatal("对局结束后机器人不应行动")
	}
}
