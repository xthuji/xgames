package bot

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/msg"
	"xgames/internal/games/ddz/rule"
	"xgames/internal/platform/protocol"
)

// fakeSession 记录机器人动作的 BotResponder 桩（解析协议消息归档动作）
type fakeSession struct {
	mu      sync.Mutex
	bids    map[string]int
	doubles map[string]bool
	plays   map[string][]protocol.CardInfo
	passes  map[string]int
}

func newFakeSession() *fakeSession {
	return &fakeSession{
		bids:    make(map[string]int),
		doubles: make(map[string]bool),
		plays:   make(map[string][]protocol.CardInfo),
		passes:  make(map[string]int),
	}
}

func (f *fakeSession) SubmitBotAction(playerID string, m protocol.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch m.Type {
	case msg.MsgBid:
		p, _ := protocol.ParsePayload[msg.BidPayload](m)
		f.bids[playerID] = p.Score
	case msg.MsgDouble:
		p, _ := protocol.ParsePayload[msg.DoublePayload](m)
		f.doubles[playerID] = p.Double
	case msg.MsgPlayCards:
		p, _ := protocol.ParsePayload[msg.PlayCardsPayload](m)
		f.plays[playerID] = p.Cards
	case msg.MsgPass:
		f.passes[playerID]++
	}
	return nil
}

// waitUntil 轮询等待条件成立（异步决策落地）。
// 全量并行测试时 CPU 被抢占会拉长决策落地耗时，3s 会偶发超时；
// 本断言只验证“机器人会响应”，不约束响应时长，故放宽到 10s
func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待机器人动作超时")
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// mkCard 构造指定点数的卡牌（花色按序号轮换避免重复）
func mkCard(i int, r card.Rank) card.Card {
	return card.Card{Suit: card.Suit(i % 4), Rank: r}
}

// setupController 构造带 2 机器人的对局跟踪（p0 真人地主，b1/b2 机器人）
func setupController(t *testing.T, engine DecisionEngine) (*Controller, *fakeSession) {
	t.Helper()
	ctrl := NewController(engine, discardLogger())
	ctrl.SetDelay(func() time.Duration { return 0 })
	resp := newFakeSession()

	ctrl.OnBroadcast("R1", protocol.NewMessage(protocol.MsgGameStart, protocol.GameStartPayload{
		Options: map[string]string{"difficulty": "hard"}, // 全量记牌视图：普通点数（如 3）也被跟踪
		Players: []protocol.PlayerInfo{
			{ID: "p0", Name: "玩家", Seat: 0},
			{ID: "b1", Name: "BOT-0001", Seat: 1, IsBot: true},
			{ID: "b2", Name: "BOT-0002", Seat: 2, IsBot: true},
		},
	}), resp)

	// 发牌：b1 手牌 3,4,5,6,7（可组顺子），b2 手牌 10,J
	hand1 := []card.Card{mkCard(0, card.Rank3), mkCard(1, card.Rank4), mkCard(2, card.Rank5), mkCard(3, card.Rank6), mkCard(0, card.Rank7)}
	hand2 := []card.Card{mkCard(1, card.Rank10), mkCard(2, card.RankJ)}
	ctrl.OnTargeted("R1", "b1", protocol.NewMessage(msg.MsgDealCards, msg.DealCardsPayload{Cards: msg.CardsToInfos(hand1)}), resp)
	ctrl.OnTargeted("R1", "b2", protocol.NewMessage(msg.MsgDealCards, msg.DealCardsPayload{Cards: msg.CardsToInfos(hand2)}), resp)

	// 确定地主：p0（真人），底牌不影响机器人手牌
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgLandlord, msg.LandlordPayload{
		PlayerID: "p0",
	}), resp)
	return ctrl, resp
}

// TestControllerBid 机器人收到叫分轮次后自动叫分
func TestControllerBid(t *testing.T) {
	ctrl, resp := setupController(t, NewEngine(discardLogger()))
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgBidTurn, msg.BidTurnPayload{PlayerID: "b1", Timeout: 5, Phase: "call"}), resp)

	waitUntil(t, func() bool {
		resp.mu.Lock()
		defer resp.mu.Unlock()
		_, ok := resp.bids["b1"]
		return ok
	})
}

// TestControllerDouble 机器人收到加倍轮次后自动表态
func TestControllerDouble(t *testing.T) {
	ctrl, resp := setupController(t, NewEngine(discardLogger()))
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgBidTurn, msg.BidTurnPayload{PlayerID: "b1", Timeout: 5, Phase: "double"}), resp)

	waitUntil(t, func() bool {
		resp.mu.Lock()
		defer resp.mu.Unlock()
		_, ok := resp.doubles["b1"]
		return ok
	})
}

// TestControllerBidIgnoresHuman 真人轮次不触发动作
func TestControllerBidIgnoresHuman(t *testing.T) {
	ctrl, resp := setupController(t, NewEngine(discardLogger()))
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgBidTurn, msg.BidTurnPayload{PlayerID: "p0", Timeout: 5, Phase: "call"}), resp)
	time.Sleep(50 * time.Millisecond)
	resp.mu.Lock()
	defer resp.mu.Unlock()
	if len(resp.bids) != 0 || len(resp.doubles) != 0 {
		t.Fatalf("真人轮次不应产生动作: bids=%v doubles=%v", resp.bids, resp.doubles)
	}
}

// TestControllerPlayMust 领出回合机器人自动出牌
func TestControllerPlayMust(t *testing.T) {
	ctrl, resp := setupController(t, NewEngine(discardLogger()))
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgPlayTurn, msg.PlayTurnPayload{
		PlayerID: "b1", MustPlay: true, CanBeat: true,
	}), resp)

	waitUntil(t, func() bool {
		resp.mu.Lock()
		defer resp.mu.Unlock()
		return len(resp.plays["b1"]) > 0
	})
}

// TestControllerPlayPass 跟不动时机器人选择不出
func TestControllerPlayPass(t *testing.T) {
	ctrl, resp := setupController(t, NewEngine(discardLogger()))
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgPlayTurn, msg.PlayTurnPayload{
		PlayerID: "b2", MustPlay: false, CanBeat: false,
	}), resp)

	waitUntil(t, func() bool {
		resp.mu.Lock()
		defer resp.mu.Unlock()
		return resp.passes["b2"] > 0
	})
}

// TestControllerCardPlayedUpdatesHand 出牌广播后机器人手牌被正确扣减
func TestControllerCardPlayedUpdatesHand(t *testing.T) {
	ctrl, resp := setupController(t, NewEngine(discardLogger()))

	// b1 出顺子 34567 → 手牌清空
	cards := []card.Card{mkCard(0, card.Rank3), mkCard(1, card.Rank4), mkCard(2, card.Rank5), mkCard(3, card.Rank6), mkCard(0, card.Rank7)}
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgCardPlayed, msg.CardPlayedPayload{
		PlayerID: "b1", PlayerName: "BOT-0001", Cards: msg.CardsToInfos(cards), CardsLeft: 0,
	}), resp)

	ctrl.mu.Lock()
	gt := ctrl.games["R1"]
	left := len(gt.hands["b1"])
	bomb := gt.bombNum
	seqLen := len(gt.actionSeq)
	ctrl.mu.Unlock()

	if left != 0 {
		t.Fatalf("b1 剩余手牌 = %d, want 0", left)
	}
	if bomb != 0 || seqLen != 1 {
		t.Fatalf("bombNum=%d seqLen=%d", bomb, seqLen)
	}
}

// TestControllerBombTracking 炸弹出牌累计 bombNum，pass 记录进序列
func TestControllerBombTracking(t *testing.T) {
	ctrl, resp := setupController(t, NewEngine(discardLogger()))

	bomb := []card.Card{mkCard(0, card.Rank9), mkCard(1, card.Rank9), mkCard(2, card.Rank9), mkCard(3, card.Rank9)}
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgCardPlayed, msg.CardPlayedPayload{
		PlayerID: "p0", PlayerName: "玩家", Cards: msg.CardsToInfos(bomb), CardsLeft: 16,
	}), resp)
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgPlayerPass, msg.PlayerPassPayload{PlayerID: "b1"}), resp)

	ctrl.mu.Lock()
	gt := ctrl.games["R1"]
	if gt.bombNum != 1 {
		t.Fatalf("bombNum = %d, want 1", gt.bombNum)
	}
	if len(gt.actionSeq) != 2 || gt.actionSeq[1] != nil {
		t.Fatalf("actionSeq 应含 pass: %v", gt.actionSeq)
	}
	// 过牌推理：b1 对炸弹过牌，炸弹压不了任何同路牌，但参照牌应被记录（供预估层过滤）
	pi, ok := gt.passes["b1"]
	if !ok || pi.Type != rule.Bomb || pi.KeyRank != card.Rank9 {
		t.Fatalf("b1 过牌参照记录错误: %+v ok=%v", pi, ok)
	}
	// p0 是地主 → 最近一手记录为地主的炸弹且为四个 9
	r := gt.recent[0]
	if !r.IsLandlord || r.Played.Type != rule.Bomb || r.Played.KeyRank != card.Rank9 {
		t.Fatalf("最近一手记录错误: %+v", r)
	}
	ctrl.mu.Unlock()
}

// TestControllerGameOverCleanup 对局结束后跟踪状态清理
func TestControllerGameOverCleanup(t *testing.T) {
	ctrl, resp := setupController(t, NewEngine(discardLogger()))
	ctrl.OnBroadcast("R1", protocol.NewMessage(protocol.MsgGameOver, protocol.GameOverPayload{WinnerID: "p0"}), resp)

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	if _, ok := ctrl.games["R1"]; ok {
		t.Fatal("对局结束后跟踪状态应清理")
	}
}

// TestControllerPassInference 过牌推理：记录参照牌 → 再次出牌失效 → 连续两过后开新轮全部失效
func TestControllerPassInference(t *testing.T) {
	ctrl, resp := setupController(t, NewEngine(discardLogger()))

	single8 := []card.Card{mkCard(0, card.Rank8)}
	played := func(id string, cs []card.Card, left int) {
		ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgCardPlayed, msg.CardPlayedPayload{
			PlayerID: id, PlayerName: id, Cards: msg.CardsToInfos(cs), CardsLeft: left,
		}), resp)
	}
	pass := func(id string) {
		ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgPlayerPass, msg.PlayerPassPayload{PlayerID: id}), resp)
	}

	// p0 出单 8 → b2 过：记录参照牌为单张 8（b2 没有更大的单张）
	played("p0", single8, 19)
	pass("b2")

	ctrl.mu.Lock()
	gt := ctrl.games["R1"]
	pi, ok := gt.passes["b2"]
	if !ok || pi.Type != rule.Single || pi.KeyRank != card.Rank8 {
		t.Fatalf("b2 过牌参照记录错误: %+v ok=%v", pi, ok)
	}
	ctrl.mu.Unlock()

	// b2 再次出牌 → 旧过牌记录失效；随后 b2 过牌，参照牌更新为 b1 的新出牌（单 9）
	single9 := []card.Card{mkCard(1, card.Rank9)}
	played("b2", single9, 1)
	played("b1", single9, 4)
	pass("b2")

	ctrl.mu.Lock()
	pi = gt.passes["b2"]
	if !ok || pi.KeyRank != card.Rank9 {
		t.Fatalf("b2 过牌参照应更新为单 9: %+v", pi)
	}
	ctrl.mu.Unlock()

	// 连续两过后 b1 出牌开启新一轮 → 全部过牌记录作废
	pass("p0")
	played("b1", single8, 3)

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	if len(gt.passes) != 0 {
		t.Fatalf("新一轮开始后过牌记录应清空: %v", gt.passes)
	}
}

// TestBuildContext buildContextLocked 填充记牌器与角色字段
func TestBuildContext(t *testing.T) {
	ctrl, _ := setupController(t, NewEngine(discardLogger()))

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	gt := ctrl.games["R1"]
	gctx := ctrl.buildContextLocked(gt, "b1", true, true)

	// p0(seat0) 为地主：b1(seat1)=地主下家（上家是地主），b2(seat2)=地主上家（下家是地主）
	if !gctx.UpIsLandlord || gctx.DownIsLandlord {
		t.Fatalf("b1 角色字段错误: up=%v down=%v", gctx.UpIsLandlord, gctx.DownIsLandlord)
	}
	if gctx.IsLandlord {
		t.Fatal("b1 不应是地主")
	}
	if gctx.PlayerCounts != [2]int{20, 17} { // 地主 17+3；b2 初始 17（发牌事件不更新计数）
		t.Fatalf("上下家剩余牌数 = %v, want [20 17]", gctx.PlayerCounts)
	}
	if gctx.RemainingCards[card.Rank3] != 4 {
		t.Fatalf("记牌器 3 = %d, want 4", gctx.RemainingCards[card.Rank3])
	}
	if len(gctx.Hand) != 5 {
		t.Fatalf("b1 手牌 = %d, want 5", len(gctx.Hand))
	}
}

// TestBuildContextPassInfos buildContextLocked 按上/下家座位填充过牌推理信息：
// b1(seat1) 视角：上家=seat0，下家=seat2
func TestBuildContextPassInfos(t *testing.T) {
	ctrl, resp := setupController(t, NewEngine(discardLogger()))

	// p0 出单 8 → b2 过：b1 视角下家（b2）的过牌参照为单 8
	single8 := []card.Card{mkCard(0, card.Rank8)}
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgCardPlayed, msg.CardPlayedPayload{
		PlayerID: "p0", PlayerName: "玩家", Cards: msg.CardsToInfos(single8), CardsLeft: 19,
	}), resp)
	ctrl.OnBroadcast("R1", protocol.NewMessage(msg.MsgPlayerPass, msg.PlayerPassPayload{PlayerID: "b2"}), resp)

	ctrl.mu.Lock()
	gt := ctrl.games["R1"]
	gctx := ctrl.buildContextLocked(gt, "b1", false, true)
	ctrl.mu.Unlock()

	if !gctx.PassInfos[1].Valid || gctx.PassInfos[1].Target.KeyRank != card.Rank8 {
		t.Fatalf("b1 下家过牌参照错误: %+v", gctx.PassInfos[1])
	}
	if gctx.PassInfos[0].Valid {
		t.Fatalf("b1 上家无过牌记录，不应有效: %+v", gctx.PassInfos[0])
	}
}
