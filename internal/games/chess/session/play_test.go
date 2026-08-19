package session

import (
	"testing"
	"time"

	"xgames/internal/games"
	"xgames/internal/games/chess/msg"
	"xgames/internal/games/chess/rule"
	"xgames/internal/platform/protocol"
)

// mockBroadcaster 测试用广播收集器
type mockBroadcaster struct {
	msgs   []protocol.Message
	sentTo map[string][]protocol.Message
}

func (m *mockBroadcaster) Broadcast(msg protocol.Message) { m.msgs = append(m.msgs, msg) }
func (m *mockBroadcaster) SendTo(id string, msg protocol.Message) {
	if m.sentTo == nil {
		m.sentTo = make(map[string][]protocol.Message)
	}
	m.sentTo[id] = append(m.sentTo[id], msg)
}

// newTestSession 构造可直接驱动 Actor 内部方法的测试会话。
// 回合超时设为 1 小时，避免测试期间计时器触发托管走子造成竞态。
func newTestSession(t *testing.T) (*Session, *mockBroadcaster) {
	t.Helper()
	bc := &mockBroadcaster{}
	players := []*Player{
		{ID: "red", Name: "红方", Seat: 0},
		{ID: "black", Name: "黑方", Seat: 1},
	}
	gs := New("test-room", players, bc, nil, Timeouts{Turn: time.Hour})
	gs.startGame()
	return gs, bc
}

// loadCustomBoard 用自定义局面替换初始局面，并重置判和计数（白盒构造测试局面）
func (gs *Session) loadCustomBoard(board []int) {
	gs.board = board
	gs.posHistory = nil
	gs.posCount = map[uint64]int{}
	gs.halfmoveClock = 0
	gs.recordPosition(rule.CampRed)
}

// shuttleBoard 循环走子测试局面：红帅(9,4)+红车(5,0) vs 黑将(0,3)+黑车(5,8)，
// 双方车可在第 5 行来回平移构成重复局面（将帅不同列，不影响将帅安全）。
func shuttleBoard() []int {
	b := make([]int, rule.Rows*rule.Cols)
	b[rule.Idx(9, 4)] = rule.RedGeneral
	b[rule.Idx(5, 0)] = rule.RedChariot
	b[rule.Idx(0, 3)] = rule.BlackGeneral
	b[rule.Idx(5, 8)] = rule.BlackChariot
	return b
}

// move 执行当前行棋方的一步走子并断言成功
func move(t *testing.T, gs *Session, playerID string, fromRow, fromCol, toRow, toCol int) {
	t.Helper()
	if err := gs.handleMove(playerID, fromRow, fromCol, toRow, toCol); err != nil {
		t.Fatalf("handleMove(%s) (%d,%d)→(%d,%d) 失败: %v", playerID, fromRow, fromCol, toRow, toCol, err)
	}
}

// gameOverMsg 取最近一条对局结束广播
func gameOverMsg(t *testing.T, bc *mockBroadcaster) protocol.GameOverPayload {
	t.Helper()
	for i := len(bc.msgs) - 1; i >= 0; i-- {
		if bc.msgs[i].Type == protocol.MsgGameOver {
			p, err := protocol.ParsePayload[protocol.GameOverPayload](bc.msgs[i])
			if err != nil {
				t.Fatalf("解析 GameOverPayload 失败: %v", err)
			}
			return p
		}
	}
	t.Fatal("未广播对局结束消息")
	return protocol.GameOverPayload{}
}

// TestDrawByRepetition 三次重复局面判和：双方来回平移车，第 3 次重复触发不变作和
func TestDrawByRepetition(t *testing.T) {
	gs, bc := newTestSession(t)
	gs.loadCustomBoard(shuttleBoard())

	// 一轮循环 4 ply：红车 (5,0)→(5,1)→(5,0)，黑车 (5,8)→(5,7)→(5,8)
	// 初始局面（红行棋）在 loadCustomBoard 已计 1 次，两轮循环后达 3 次 → 判和
	for round := 0; round < 2; round++ {
		move(t, gs, "red", 5, 0, 5, 1)
		move(t, gs, "black", 5, 8, 5, 7)
		move(t, gs, "red", 5, 1, 5, 0)
		move(t, gs, "black", 5, 7, 5, 8)
	}

	if gs.phase != PhaseEnded {
		t.Fatalf("三次重复后对局应结束, phase=%v", gs.phase)
	}
	if gs.drawReason != rule.DrawRepetition {
		t.Fatalf("判和原因 = %q, want %q", gs.drawReason, rule.DrawRepetition)
	}
	payload := gameOverMsg(t, bc)
	if payload.WinnerID != "" {
		t.Fatalf("判和不应有胜者, got %q", payload.WinnerID)
	}
	if payload.DrawReason != string(rule.DrawRepetition) {
		t.Fatalf("广播 DrawReason = %q, want %q", payload.DrawReason, rule.DrawRepetition)
	}
	for _, s := range payload.Scores {
		if s.Score != 0 {
			t.Fatalf("判和双方得分应为 0, got %+v", payload.Scores)
		}
	}
}

// TestDrawByNaturalLimit 自然限着判和：halfmoveClock=25 时再走一步无吃子 → 13 回合判和
func TestDrawByNaturalLimit(t *testing.T) {
	gs, bc := newTestSession(t)
	gs.loadCustomBoard(shuttleBoard())
	gs.halfmoveClock = 25 // 伪造已 12 回合半未吃子

	move(t, gs, "red", 5, 0, 5, 1)

	if gs.phase != PhaseEnded {
		t.Fatalf("13 回合未吃子后对局应结束, phase=%v", gs.phase)
	}
	if gs.drawReason != rule.DrawNaturalLimit {
		t.Fatalf("判和原因 = %q, want %q", gs.drawReason, rule.DrawNaturalLimit)
	}
	if p := gameOverMsg(t, bc); p.DrawReason != string(rule.DrawNaturalLimit) {
		t.Fatalf("广播 DrawReason = %q, want %q", p.DrawReason, rule.DrawNaturalLimit)
	}
}

// TestDrawByInsufficientMaterial 简单和棋局面判和：双方均无车马炮兵卒
func TestDrawByInsufficientMaterial(t *testing.T) {
	gs, bc := newTestSession(t)
	b := make([]int, rule.Rows*rule.Cols)
	b[rule.Idx(9, 4)] = rule.RedGeneral
	b[rule.Idx(9, 3)] = rule.RedAdvisor
	b[rule.Idx(9, 2)] = rule.RedElephant
	b[rule.Idx(0, 4)] = rule.BlackGeneral
	b[rule.Idx(0, 3)] = rule.BlackAdvisor
	b[rule.Idx(0, 2)] = rule.BlackElephant
	gs.loadCustomBoard(b)

	move(t, gs, "red", 9, 3, 8, 4) // 红仕上仕

	if gs.phase != PhaseEnded {
		t.Fatalf("双方均无取胜可能后对局应结束, phase=%v", gs.phase)
	}
	if gs.drawReason != rule.DrawNoMaterial {
		t.Fatalf("判和原因 = %q, want %q", gs.drawReason, rule.DrawNoMaterial)
	}
	if p := gameOverMsg(t, bc); p.DrawReason != string(rule.DrawNoMaterial) {
		t.Fatalf("广播 DrawReason = %q, want %q", p.DrawReason, rule.DrawNoMaterial)
	}
}

// TestNoFalseDraw 正常走子不误判：无吃子推进计数、对局继续
func TestNoFalseDraw(t *testing.T) {
	gs, _ := newTestSession(t)

	// 红方马二进三（无吃子）
	if err := gs.handleMove("red", 9, 1, 7, 2); err != nil {
		t.Fatalf("开局走子失败: %v", err)
	}
	if gs.phase != PhasePlaying {
		t.Fatalf("正常走子不应结束对局, phase=%v", gs.phase)
	}
	if gs.halfmoveClock != 1 {
		t.Fatalf("无吃子 halfmoveClock 应为 1, got %d", gs.halfmoveClock)
	}
	if gs.currentPlayer != 1 {
		t.Fatalf("应轮到黑方, got %d", gs.currentPlayer)
	}
}

// TestSnapshotDrawState 快照恢复：判和计数与限着计数随快照恢复
func TestSnapshotDrawState(t *testing.T) {
	gs, _ := newTestSession(t)

	// 走两手无吃子（红马、黑马），产生 2 条历史局面 + halfmoveClock=2
	if err := gs.handleMove("red", 9, 1, 7, 2); err != nil {
		t.Fatalf("红方走子失败: %v", err)
	}
	if err := gs.handleMove("black", 0, 1, 2, 2); err != nil {
		t.Fatalf("黑方走子失败: %v", err)
	}

	snap := gs.snapshot()
	if len(snap.PosHistory) != len(gs.posHistory) || snap.HalfmoveClock != gs.halfmoveClock {
		t.Fatalf("快照判和状态不完整: posHistory=%d halfmoveClock=%d", len(snap.PosHistory), snap.HalfmoveClock)
	}

	gs2, _ := newTestSession(t)
	gs2.applySnapshot(snap)
	if gs2.halfmoveClock != gs.halfmoveClock {
		t.Fatalf("恢复后 halfmoveClock = %d, want %d", gs2.halfmoveClock, gs.halfmoveClock)
	}
	if len(gs2.posHistory) != len(gs.posHistory) {
		t.Fatalf("恢复后 posHistory 长度 = %d, want %d", len(gs2.posHistory), len(gs.posHistory))
	}
	for k, v := range gs.posCount {
		if gs2.posCount[k] != v {
			t.Fatalf("恢复后 posCount[%d] = %d, want %d", k, gs2.posCount[k], v)
		}
	}

	// 恢复后继续走子：重复计数应延续（把黑马送回再复原，位置哈希累计正确）
	if err := gs2.handleMove("red", 7, 2, 9, 1); err != nil {
		t.Fatalf("恢复后走子失败: %v", err)
	}
}

// sentToMsg 取最近一条发给指定玩家的指定类型私发消息并解析 payload
func sentToMsg[P any](t *testing.T, bc *mockBroadcaster, playerID string, msgType protocol.MessageType) P {
	t.Helper()
	for i := len(bc.sentTo[playerID]) - 1; i >= 0; i-- {
		if bc.sentTo[playerID][i].Type == msgType {
			p, err := protocol.ParsePayload[P](bc.sentTo[playerID][i])
			if err != nil {
				t.Fatalf("解析私发消息 %s 失败: %v", msgType, err)
			}
			return p
		}
	}
	t.Fatalf("未向 %s 私发 %s 消息", playerID, msgType)
	var zero P
	return zero
}

// TestDrawRequiresOpponentApproval 和棋需对手认可：请求者不能自我批准，无待处理请求不能应答
func TestDrawRequiresOpponentApproval(t *testing.T) {
	gs, bc := newTestSession(t)

	// 无待处理请求时直接应答应被拒绝
	if err := gs.handleDrawResponse("black", true); err != ErrDrawNoPending {
		t.Fatalf("无待处理请求应答应返回 ErrDrawNoPending, got %v", err)
	}

	// 红方请求和棋 → 黑方收到请求通知
	if err := gs.handleDrawRequest("red"); err != nil {
		t.Fatalf("handleDrawRequest 失败: %v", err)
	}
	p := sentToMsg[msg.CcDrawRequestedPayload](t, bc, "black", msg.MsgCcDrawRequested)
	if p.RequesterID != "red" {
		t.Fatalf("请求者 = %q, want %q", p.RequesterID, "red")
	}

	// 请求者自我批准应被拒绝，对局继续
	if err := gs.handleDrawResponse("red", true); err != ErrDrawNoPending {
		t.Fatalf("请求者自我批准应返回 ErrDrawNoPending, got %v", err)
	}
	if gs.phase != PhasePlaying {
		t.Fatalf("自我批准不应结束对局, phase=%v", gs.phase)
	}

	// 黑方拒绝 → 红方收到拒绝通知，对局继续，待处理请求已清空
	if err := gs.handleDrawResponse("black", false); err != nil {
		t.Fatalf("黑方拒绝失败: %v", err)
	}
	r := sentToMsg[msg.CcDrawResultPayload](t, bc, "red", msg.MsgCcDrawResult)
	if r.Accepted {
		t.Fatal("拒绝后 Accepted 应为 false")
	}
	if gs.phase != PhasePlaying {
		t.Fatalf("拒绝后对局应继续, phase=%v", gs.phase)
	}
	if err := gs.handleDrawResponse("black", true); err != ErrDrawNoPending {
		t.Fatalf("拒绝后重复应答应返回 ErrDrawNoPending, got %v", err)
	}

	// 红方再次请求，黑方同意 → 协议判和
	if err := gs.handleDrawRequest("red"); err != nil {
		t.Fatalf("handleDrawRequest 失败: %v", err)
	}
	if err := gs.handleDrawResponse("black", true); err != nil {
		t.Fatalf("黑方同意失败: %v", err)
	}
	if gs.phase != PhaseEnded || gs.drawReason != rule.DrawAgreement {
		t.Fatalf("同意后应判和, phase=%v reason=%v", gs.phase, gs.drawReason)
	}
}

// TestDrawRequestExpiresOnMove 请求者走子后，未应答的和棋请求自动失效
func TestDrawRequestExpiresOnMove(t *testing.T) {
	gs, _ := newTestSession(t)

	if err := gs.handleDrawRequest("red"); err != nil {
		t.Fatalf("handleDrawRequest 失败: %v", err)
	}
	if err := gs.handleMove("red", 9, 1, 7, 2); err != nil {
		t.Fatalf("红方走子失败: %v", err)
	}
	if err := gs.handleDrawResponse("black", true); err != ErrDrawNoPending {
		t.Fatalf("走子后过期请求应答应返回 ErrDrawNoPending, got %v", err)
	}
	if gs.phase != PhasePlaying {
		t.Fatalf("过期请求不应结束对局, phase=%v", gs.phase)
	}
}

// TestBotDrawEval 机器人对手：按注入的评估函数自动接受或拒绝和棋
func TestBotDrawEval(t *testing.T) {
	// 评估函数返回 true → 协议判和
	gs, _ := newTestSession(t)
	gs.players[1].IsBot = true
	gs.SetDrawEvaluator(func(board []int, camp int) bool {
		if camp != rule.CampOfSeat(gs.players[1].Seat) {
			t.Fatalf("评估函数收到错误阵营: %d", camp)
		}
		return true
	})
	if err := gs.handleDrawRequest("red"); err != nil {
		t.Fatalf("handleDrawRequest 失败: %v", err)
	}
	if gs.phase != PhaseEnded || gs.drawReason != rule.DrawAgreement {
		t.Fatalf("机器人接受后应判和, phase=%v reason=%v", gs.phase, gs.drawReason)
	}

	// 评估函数返回 false → 请求者收到拒绝通知，对局继续
	gs2, bc2 := newTestSession(t)
	gs2.players[1].IsBot = true
	gs2.SetDrawEvaluator(func([]int, int) bool { return false })
	if err := gs2.handleDrawRequest("red"); err != nil {
		t.Fatalf("handleDrawRequest 失败: %v", err)
	}
	r := sentToMsg[msg.CcDrawResultPayload](t, bc2, "red", msg.MsgCcDrawResult)
	if r.Accepted {
		t.Fatal("机器人拒绝后 Accepted 应为 false")
	}
	if gs2.phase != PhasePlaying {
		t.Fatalf("机器人拒绝后对局应继续, phase=%v", gs2.phase)
	}
}

// 编译期接口约束：判和测试依赖的 games 包别名保持有效
var _ games.Broadcaster = (*mockBroadcaster)(nil)
