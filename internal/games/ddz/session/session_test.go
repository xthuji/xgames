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

	"xgames/internal/games/ddz/card"
	ddzmsg "xgames/internal/games/ddz/msg"
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
	targeted   map[string][]protocol.Message
}

func newTestBroadcaster() *testBroadcaster {
	return &testBroadcaster{targeted: make(map[string][]protocol.Message)}
}

func (b *testBroadcaster) Broadcast(msg protocol.Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.broadcasts = append(b.broadcasts, msg)
}

func (b *testBroadcaster) SendTo(playerID string, msg protocol.Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.targeted[playerID] = append(b.targeted[playerID], msg)
}

// nextBroadcast 从 fromIdx 起等待下一条指定类型广播，返回消息与下一条起始索引
func (b *testBroadcaster) nextBroadcast(t *testing.T, mt protocol.MessageType, fromIdx int, within time.Duration) (protocol.Message, int) {
	t.Helper()
	if fromIdx < 0 {
		fromIdx = 0
	}
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

// countBroadcasts 统计指定类型广播数
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

func testTimeouts() Timeouts {
	return Timeouts{Turn: 3 * time.Second, Bid: 3 * time.Second, OfflineWait: 3 * time.Second}
}

func newHarness(t *testing.T, to Timeouts) (*Session, *testBroadcaster) {
	t.Helper()
	players := []*Player{
		{ID: "A", Name: "玩家A", Seat: 0},
		{ID: "B", Name: "玩家B", Seat: 1},
		{ID: "C", Name: "玩家C", Seat: 2},
	}
	bc := newTestBroadcaster()
	gs := New("000000", players, bc, nil, to)
	go gs.Run(context.Background())
	t.Cleanup(gs.Stop)
	return gs, bc
}

// tableTracker 在测试侧镜像跟踪各家手牌
type tableTracker struct {
	hands map[string][]card.Card
}

func newTableTracker() *tableTracker { return &tableTracker{hands: map[string][]card.Card{}} }

func (tt *tableTracker) setHand(playerID string, cards []card.Card) {
	hand := make([]card.Card, len(cards))
	copy(hand, cards)
	tt.hands[playerID] = hand
}

func (tt *tableTracker) removeCards(playerID string, cards []card.Card) {
	tt.hands[playerID] = card.RemoveCards(tt.hands[playerID], cards)
}

func (tt *tableTracker) smallestSingle(playerID string) card.Card {
	hand := tt.hands[playerID]
	min := hand[0]
	for _, c := range hand[1:] {
		if c.Rank < min.Rank {
			min = c
		}
	}
	return min
}

// collectDealtHands 收集开局发牌（3 条 deal_cards 定向消息）
func collectDealtHands(t *testing.T, bc *testBroadcaster, tt *tableTracker) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		bc.mu.Lock()
		ready := len(bc.targeted["A"]) > 0 && len(bc.targeted["B"]) > 0 && len(bc.targeted["C"]) > 0
		bc.mu.Unlock()
		if ready {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	bc.mu.Lock()
	defer bc.mu.Unlock()
	for _, pid := range []string{"A", "B", "C"} {
		require.NotEmpty(t, bc.targeted[pid], "玩家 %s 未收到发牌", pid)
		msg := bc.targeted[pid][len(bc.targeted[pid])-1]
		require.Equal(t, ddzmsg.MsgDealCards, msg.Type)
		p := decode[ddzmsg.DealCardsPayload](t, msg)
		tt.setHand(pid, ddzmsg.InfosToCards(p.Cards))
	}
}

// driveToPlaying 驱动对局进入出牌阶段：首叫者叫 1 分，其余不叫，两农民不加倍。返回地主 ID 与底倍。
func driveToPlaying(t *testing.T, gs *Session, bc *testBroadcaster) (string, int) {
	t.Helper()
	msg, next := bc.nextBroadcast(t, ddzmsg.MsgBidTurn, 0, 2*time.Second)
	first := decode[ddzmsg.BidTurnPayload](t, msg)
	require.Equal(t, "call", first.Phase)
	require.NoError(t, gs.Bid(first.PlayerID, 1))

	// 一圈叫分结束定地主
	var landlordID string
	multiplier := 0
	for landlordID == "" {
		m, idx := bc.nextBroadcastFrom(t, ddzmsg.MsgBidTurn, ddzmsg.MsgLandlord, next, 2*time.Second)
		next = idx
		if m.Type == ddzmsg.MsgLandlord {
			lp := decode[ddzmsg.LandlordPayload](t, m)
			landlordID = lp.PlayerID
			multiplier = lp.Multiplier
			continue
		}
		turn := decode[ddzmsg.BidTurnPayload](t, m)
		require.NoError(t, gs.Bid(turn.PlayerID, 0))
	}

	// 加倍阶段：两名农民均不加倍
	for i := 0; i < 2; i++ {
		m, idx := bc.nextBroadcast(t, ddzmsg.MsgBidTurn, next, 2*time.Second)
		next = idx
		turn := decode[ddzmsg.BidTurnPayload](t, m)
		require.Equal(t, "double", turn.Phase)
		require.NoError(t, gs.Double(turn.PlayerID, false))
	}
	return landlordID, multiplier
}

// gameOverMultiplier 从 game_over 扩展数据提取最终倍数
func gameOverMultiplier(t *testing.T, over protocol.GameOverPayload) int {
	t.Helper()
	var extra ddzmsg.GameOverExtra
	require.NoError(t, json.Unmarshal(over.Extra, &extra))
	return extra.Multiplier
}

// waitForGameOver 按「必须出牌出最小单张，否则 pass」策略打到结束
func waitForGameOver(t *testing.T, gs *Session, bc *testBroadcaster, landlordID string) protocol.GameOverPayload {
	t.Helper()
	tt := newTableTracker()
	collectDealtHands(t, bc, tt)
	// 地主手牌以第二次定向推送（含底牌 20 张）为准
	bc.mu.Lock()
	for _, m := range bc.targeted[landlordID] {
		if m.Type == ddzmsg.MsgDealCards {
			p := decode[ddzmsg.DealCardsPayload](t, m)
			if len(p.Cards) == 20 {
				tt.setHand(landlordID, ddzmsg.InfosToCards(p.Cards))
			}
		}
	}
	bc.mu.Unlock()

	nextTurn, nextPlayed := 0, 0
	for i := 0; i < 200; i++ {
		msg, idx := bc.nextBroadcast(t, ddzmsg.MsgPlayTurn, nextTurn, 5*time.Second)
		nextTurn = idx
		turn := decode[ddzmsg.PlayTurnPayload](t, msg)

		if !turn.MustPlay {
			require.NoError(t, gs.Pass(turn.PlayerID))
			continue
		}

		c := tt.smallestSingle(turn.PlayerID)
		require.NoError(t, gs.PlayCards(turn.PlayerID, ddzmsg.CardsToInfos([]card.Card{c})),
			"出牌失败 player=%s", turn.PlayerID)

		// 等待本次出牌结果：card_played（同步手牌镜像）或 game_over
		m, idx2 := bc.nextBroadcastAny(t, nextPlayed, 5*time.Second, ddzmsg.MsgCardPlayed, protocol.MsgGameOver)
		nextPlayed = idx2
		if m.Type == protocol.MsgGameOver {
			return decode[protocol.GameOverPayload](t, m)
		}
		p := decode[ddzmsg.CardPlayedPayload](t, m)
		tt.removeCards(p.PlayerID, ddzmsg.InfosToCards(p.Cards))

		// 最后一手牌：card_played(CardsLeft=0) 之后紧跟 game_over
		if p.CardsLeft == 0 {
			goMsg, idx3 := bc.nextBroadcast(t, protocol.MsgGameOver, nextPlayed, 2*time.Second)
			nextPlayed = idx3
			return decode[protocol.GameOverPayload](t, goMsg)
		}
	}
	t.Fatal("200 回合内未结束")
	return protocol.GameOverPayload{}
}

// nextBroadcastAny 从 from 起等待任一指定类型广播
func (b *testBroadcaster) nextBroadcastAny(t *testing.T, from int, within time.Duration, types ...protocol.MessageType) (protocol.Message, int) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		for i := from; i < len(b.broadcasts); i++ {
			for _, mt := range types {
				if b.broadcasts[i].Type == mt {
					msg := b.broadcasts[i]
					b.mu.Unlock()
					return msg, i + 1
				}
			}
		}
		b.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待广播 %v 超时", types)
	return protocol.Message{}, from
}

// --- 测试用例 ---

// 完整对局：叫分 → 出牌到结束 → 结算正确 → Actor 退出
func TestFullGameScripted(t *testing.T) {
	gs, bc := newHarness(t, testTimeouts())

	landlordID, bidMult := driveToPlaying(t, gs, bc)

	over := waitForGameOver(t, gs, bc, landlordID)

	// 结算口径校验：地主 ±2×倍数，农民 ∓倍数，总和为零（倍数/手牌在扩展数据中）
	require.Len(t, over.Scores, 3)
	var extra ddzmsg.GameOverExtra
	require.NoError(t, json.Unmarshal(over.Extra, &extra))
	landlordWin := over.WinnerID == landlordID
	sum := 0
	for _, s := range over.Scores {
		sum += s.Score
		if s.IsLandlord {
			assert.Equal(t, landlordID, s.PlayerID)
			if landlordWin {
				assert.Equal(t, 2*extra.Multiplier, s.Score)
			} else {
				assert.Equal(t, -2*extra.Multiplier, s.Score)
			}
		}
	}
	assert.Zero(t, sum)
	assert.GreaterOrEqual(t, extra.Multiplier, bidMult)

	// Actor 应在结算后自行退出
	select {
	case <-gs.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Actor 未在结算后退出")
	}
}

// 叫分全路径：一圈无人叫 → 流局重发
func TestBidAllPassRedeal(t *testing.T) {
	gs, bc := newHarness(t, testTimeouts())

	// 首轮 3 人不叫
	next := 0
	for i := 0; i < 3; i++ {
		msg, idx := bc.nextBroadcast(t, ddzmsg.MsgBidTurn, next, 2*time.Second)
		next = idx
		turn := decode[ddzmsg.BidTurnPayload](t, msg)
		require.NoError(t, gs.Bid(turn.PlayerID, 0))
	}

	// 流局后重新发牌：每个玩家会再收到一条 deal_cards
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		bc.mu.Lock()
		n := len(bc.targeted["A"])
		bc.mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	bc.mu.Lock()
	assert.GreaterOrEqual(t, len(bc.targeted["A"]), 2, "流局后应重新发牌")
	bc.mu.Unlock()

	// 第二轮叫分开始
	_, _ = bc.nextBroadcast(t, ddzmsg.MsgBidTurn, next, 2*time.Second)
}

// 叫分：叫 3 分立即定地主（底倍 3），随后进入加倍阶段
func TestBidThreeImmediateLandlord(t *testing.T) {
	gs, bc := newHarness(t, testTimeouts())

	msg, next := bc.nextBroadcast(t, ddzmsg.MsgBidTurn, 0, 2*time.Second)
	first := decode[ddzmsg.BidTurnPayload](t, msg)
	require.NoError(t, gs.Bid(first.PlayerID, 3))

	lmsg, _ := bc.nextBroadcast(t, ddzmsg.MsgLandlord, 0, 2*time.Second)
	lp := decode[ddzmsg.LandlordPayload](t, lmsg)
	assert.Equal(t, 3, lp.Multiplier, "叫 3 分底倍应为 3")
	assert.Equal(t, first.PlayerID, lp.PlayerID, "叫 3 分者立即成为地主")

	// 两名农民依次表态后进入出牌阶段
	for i := 0; i < 2; i++ {
		m, idx := bc.nextBroadcast(t, ddzmsg.MsgBidTurn, next, 2*time.Second)
		next = idx
		turn := decode[ddzmsg.BidTurnPayload](t, m)
		assert.Equal(t, "double", turn.Phase)
		require.NoError(t, gs.Double(turn.PlayerID, false))
	}
	_, _ = bc.nextBroadcast(t, ddzmsg.MsgPlayTurn, 0, 2*time.Second)
}

// 加倍：两名农民都加倍 → 倍数底倍×4，结算口径生效
func TestDoubleMultiplierApplies(t *testing.T) {
	gs, bc := newHarness(t, testTimeouts())

	msg, next := bc.nextBroadcast(t, ddzmsg.MsgBidTurn, 0, 2*time.Second)
	first := decode[ddzmsg.BidTurnPayload](t, msg)
	require.NoError(t, gs.Bid(first.PlayerID, 3))

	lmsg, _ := bc.nextBroadcast(t, ddzmsg.MsgLandlord, 0, 2*time.Second)
	lp := decode[ddzmsg.LandlordPayload](t, lmsg)

	// 两名农民均加倍，验证广播倍数递增
	for i := 0; i < 2; i++ {
		m, idx := bc.nextBroadcast(t, ddzmsg.MsgBidTurn, next, 2*time.Second)
		next = idx
		turn := decode[ddzmsg.BidTurnPayload](t, m)
		require.Equal(t, "double", turn.Phase)
		require.NoError(t, gs.Double(turn.PlayerID, true))

		rmsg, ridx := bc.nextBroadcast(t, ddzmsg.MsgBidResult, next, 2*time.Second)
		next = ridx
		rp := decode[ddzmsg.BidResultPayload](t, rmsg)
		assert.Equal(t, "double", rp.Phase)
		assert.True(t, rp.Double)
		assert.Equal(t, 3*(1<<(i+1)), rp.Multiplier, "第 %d 次加倍后倍数", i+1)
	}

	// 加倍结束进入出牌，最终结算倍数应为 12 的 2 的幂倍（炸弹/春天）
	over := waitForGameOver(t, gs, bc, lp.PlayerID)
	require.Zero(t, gameOverMultiplier(t, over)%12, "结算倍数应包含加倍后的底倍 12")
}

// 非法叫分分数被拒绝（回合不推进）
func TestInvalidBidScore(t *testing.T) {
	gs, bc := newHarness(t, testTimeouts())

	msg, _ := bc.nextBroadcast(t, ddzmsg.MsgBidTurn, 0, 2*time.Second)
	turn := decode[ddzmsg.BidTurnPayload](t, msg)

	assert.ErrorIs(t, gs.Bid(turn.PlayerID, 4), ErrInvalidBid, "超过 3 分应被拒绝")
	assert.ErrorIs(t, gs.Bid(turn.PlayerID, -1), ErrInvalidBid, "负分应被拒绝")

	// 叫 2 分后，下家叫 ≤2 分应被拒绝
	require.NoError(t, gs.Bid(turn.PlayerID, 2))
	m, _ := bc.nextBroadcast(t, ddzmsg.MsgBidTurn, 1, 2*time.Second)
	turn2 := decode[ddzmsg.BidTurnPayload](t, m)
	assert.Equal(t, 2, turn2.HighBid, "应携带当前最高叫分")
	assert.ErrorIs(t, gs.Bid(turn2.PlayerID, 1), ErrInvalidBid, "不高于最高分应被拒绝")
	assert.ErrorIs(t, gs.Bid(turn2.PlayerID, 2), ErrInvalidBid, "等于最高分应被拒绝")
	require.NoError(t, gs.Bid(turn2.PlayerID, 0), "不叫始终合法")
}

// nextBroadcastFrom 从 from 起等待任一指定类型消息
func (b *testBroadcaster) nextBroadcastFrom(t *testing.T, mt1, mt2 protocol.MessageType, from int, within time.Duration) (protocol.Message, int) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		for i := from; i < len(b.broadcasts); i++ {
			if b.broadcasts[i].Type == mt1 || b.broadcasts[i].Type == mt2 {
				msg := b.broadcasts[i]
				b.mu.Unlock()
				return msg, i + 1
			}
		}
		b.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待广播 %s/%s 超时", mt1, mt2)
	return protocol.Message{}, from
}

// 叫分超时自动不叫
func TestBidTimeoutAutoPass(t *testing.T) {
	to := testTimeouts()
	to.Bid = 80 * time.Millisecond
	gs, bc := newHarness(t, to)
	_ = gs

	// 不发送任何 bid，应自动产生 bid_result{score:0}
	msg, _ := bc.nextBroadcast(t, ddzmsg.MsgBidResult, 0, 2*time.Second)
	result := decode[ddzmsg.BidResultPayload](t, msg)
	assert.Equal(t, "call", result.Phase)
	assert.Zero(t, result.Score)
}

// 出牌超时托管：自动出最小可压牌或 pass
func TestPlayTimeoutAutoplay(t *testing.T) {
	to := testTimeouts()
	to.Turn = 100 * time.Millisecond
	gs, bc := newHarness(t, to)

	landlordID, _ := driveToPlaying(t, gs, bc)
	_ = landlordID

	// 不响应 play_turn，应自动产生 card_played 或 player_pass
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bc.countBroadcasts(ddzmsg.MsgCardPlayed) > 0 || bc.countBroadcasts(ddzmsg.MsgPlayerPass) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("出牌超时后未自动托管")
}

// 离线暂停计时 + 重连恢复
func TestOfflinePauseAndResume(t *testing.T) {
	to := testTimeouts()
	to.Bid = 500 * time.Millisecond
	to.OfflineWait = 5 * time.Second
	gs, bc := newHarness(t, to)

	msg, _ := bc.nextBroadcast(t, ddzmsg.MsgBidTurn, 0, 2*time.Second)
	turn := decode[ddzmsg.BidTurnPayload](t, msg)

	// 当前叫分者掉线 → 计时暂停
	gs.PlayerOffline(turn.PlayerID)
	time.Sleep(700 * time.Millisecond) // 超过 bid 超时
	assert.Equal(t, 1, bc.countBroadcasts(ddzmsg.MsgBidTurn), "离线期间不应推进回合")

	// 重连 → 恢复计时，剩余时间耗尽后自动不叫
	gs.PlayerOnline(turn.PlayerID)
	msg, _ = bc.nextBroadcast(t, ddzmsg.MsgBidResult, 0, 3*time.Second)
	result := decode[ddzmsg.BidResultPayload](t, msg)
	assert.Zero(t, result.Score)
}

// 离线等待超时 → 自动托管
func TestOfflineTimeoutAutoplay(t *testing.T) {
	to := testTimeouts()
	to.Bid = 10 * time.Second
	to.OfflineWait = 100 * time.Millisecond
	gs, bc := newHarness(t, to)

	msg, _ := bc.nextBroadcast(t, ddzmsg.MsgBidTurn, 0, 2*time.Second)
	turn := decode[ddzmsg.BidTurnPayload](t, msg)
	gs.PlayerOffline(turn.PlayerID)

	msg, _ = bc.nextBroadcast(t, ddzmsg.MsgBidResult, 0, 2*time.Second)
	result := decode[ddzmsg.BidResultPayload](t, msg)
	assert.Zero(t, result.Score, "离线超时应自动不叫")
}

// 非法操作被拒绝（回合不推进）
func TestInvalidActions(t *testing.T) {
	gs, bc := newHarness(t, testTimeouts())

	// 非当前回合玩家叫分
	msg, _ := bc.nextBroadcast(t, ddzmsg.MsgBidTurn, 0, 2*time.Second)
	turn := decode[ddzmsg.BidTurnPayload](t, msg)
	wrongID := "A"
	if turn.PlayerID == "A" {
		wrongID = "B"
	}
	assert.ErrorIs(t, gs.Bid(wrongID, 1), ErrNotYourTurn)

	// 进入出牌阶段
	landlordID, _ := driveToPlaying(t, gs, bc)

	pmsg, _ := bc.nextBroadcast(t, ddzmsg.MsgPlayTurn, 0, 2*time.Second)
	pt := decode[ddzmsg.PlayTurnPayload](t, pmsg)

	if pt.MustPlay {
		// 新一轮不能 pass
		assert.ErrorIs(t, gs.Pass(pt.PlayerID), ErrMustPlay)
	}

	// 出不在手中的牌
	assert.ErrorIs(t, gs.PlayCards(pt.PlayerID, []protocol.CardInfo{
		{Suit: 0, Rank: 99, Color: 0},
	}), ErrInvalidCards)

	// 错误玩家出牌
	wrong := "A"
	if pt.PlayerID == "A" {
		wrong = "B"
	}
	assert.ErrorIs(t, gs.PlayCards(wrong, nil), ErrNotYourTurn)
	_ = landlordID
}

// 炸弹翻倍 + 连续两 pass 开新轮（规则引擎已覆盖牌型，这里验证会话级累计）
func TestBombDoublesMultiplier(t *testing.T) {
	gs, bc := newHarness(t, testTimeouts())
	landlordID, bidMult := driveToPlaying(t, gs, bc)
	_ = landlordID

	over := waitForGameOver(t, gs, bc, landlordID)
	// 最终倍数 = 底倍 × 2^炸弹数（×春天），必然为 底倍 的 2 的幂倍
	mult := gameOverMultiplier(t, over)
	require.Zero(t, mult%bidMult)
	ratio := mult / bidMult
	assert.True(t, ratio > 0 && (ratio&(ratio-1)) == 0, "倍数应为底倍乘以 2 的幂: %d/%d", mult, bidMult)
}

// 快照序列化/反序列化往返 + 恢复后继续对局
func TestSnapshotRoundtrip(t *testing.T) {
	gs, bc := newHarness(t, testTimeouts())
	landlordID, _ := driveToPlaying(t, gs, bc)

	// 出一手牌制造上家记录
	pmsg, _ := bc.nextBroadcast(t, ddzmsg.MsgPlayTurn, 0, 2*time.Second)
	pt := decode[ddzmsg.PlayTurnPayload](t, pmsg)
	require.True(t, pt.MustPlay, "地主首出应为新一轮")

	tt := newTableTracker()
	collectDealtHands(t, bc, tt)
	bc.mu.Lock()
	for _, m := range bc.targeted[landlordID] {
		if m.Type == ddzmsg.MsgDealCards {
			p := decode[ddzmsg.DealCardsPayload](t, m)
			if len(p.Cards) == 20 {
				tt.setHand(landlordID, ddzmsg.InfosToCards(p.Cards))
			}
		}
	}
	bc.mu.Unlock()

	first := tt.smallestSingle(pt.PlayerID)
	require.NoError(t, gs.PlayCards(pt.PlayerID, ddzmsg.CardsToInfos([]card.Card{first})))

	// 导出快照 → JSON 往返 → 恢复
	snap := gs.SnapshotState()
	require.NotNil(t, snap)
	data, err := json.Marshal(snap)
	require.NoError(t, err)
	var decoded Snapshot
	require.NoError(t, json.Unmarshal(data, &decoded))

	bc2 := newTestBroadcaster()
	gs2, err := RestoreFromSnapshot(&decoded, bc2, nil, testTimeouts())
	require.NoError(t, err)
	go gs2.Run(context.Background())
	defer gs2.Stop()

	// 恢复后应广播 play_turn 给下家，且可继续出牌
	pmsg2, _ := bc2.nextBroadcast(t, ddzmsg.MsgPlayTurn, 0, 2*time.Second)
	pt2 := decode[ddzmsg.PlayTurnPayload](t, pmsg2)
	assert.NotEqual(t, pt.PlayerID, pt2.PlayerID, "恢复后应轮到下家")
}

// 恢复局必须设置 gameStartTime：endGame 生成复盘报告以非零为前提，
// 缺失会导致恢复的对局打完后完全不生成复盘报告
func TestRestoreFromSnapshotSetsGameStartTime(t *testing.T) {
	gs, bc := newHarness(t, testTimeouts())
	driveToPlaying(t, gs, bc)

	snap := gs.SnapshotState()
	require.NotNil(t, snap)

	bc2 := newTestBroadcaster()
	gs2, err := RestoreFromSnapshot(snap, bc2, nil, testTimeouts())
	require.NoError(t, err)
	go gs2.Run(context.Background())
	defer gs2.Stop()

	// 首条广播意味着 applySnapshot 已在 Run 内执行
	_, _ = bc2.nextBroadcast(t, ddzmsg.MsgPlayTurn, 0, 2*time.Second)
	assert.False(t, gs2.gameStartTime.IsZero(), "恢复局应设置 gameStartTime，否则复盘报告会被跳过")
}

// --- 挂机（显式托管） ---

// 挂机后机器人策略在短延迟内自动行动
func TestAfkAutoAct(t *testing.T) {
	gs, bc := newHarness(t, testTimeouts())
	_, _ = driveToPlaying(t, gs, bc)

	pmsg, _ := bc.nextBroadcast(t, ddzmsg.MsgPlayTurn, 0, 2*time.Second)
	pt := decode[ddzmsg.PlayTurnPayload](t, pmsg)

	require.NoError(t, gs.SetAfk(pt.PlayerID, true))

	afkMsg, _ := bc.nextBroadcast(t, ddzmsg.MsgAfkChanged, 0, 2*time.Second)
	ap := decode[ddzmsg.AfkChangedPayload](t, afkMsg)
	assert.Equal(t, pt.PlayerID, ap.PlayerID)
	assert.True(t, ap.Afk)

	// 挂机后无需手动操作即自动出牌（地主首出 mustPlay）
	_, _ = bc.nextBroadcast(t, ddzmsg.MsgCardPlayed, 0, 3*time.Second)
}

// 在线玩家回合超时：先广播转挂机，之后才由机器人策略接管
func TestTimeoutEntersAfk(t *testing.T) {
	to := testTimeouts()
	to.Turn = 120 * time.Millisecond
	gs, bc := newHarness(t, to)

	_, _ = driveToPlaying(t, gs, bc)

	pmsg, _ := bc.nextBroadcast(t, ddzmsg.MsgPlayTurn, 0, 2*time.Second)
	pt := decode[ddzmsg.PlayTurnPayload](t, pmsg)

	// 不做任何操作：应先收到 afk_changed(afk=true)，再出现自动动作
	afkMsg, afkIdx := bc.nextBroadcast(t, ddzmsg.MsgAfkChanged, 0, 2*time.Second)
	ap := decode[ddzmsg.AfkChangedPayload](t, afkMsg)
	assert.Equal(t, pt.PlayerID, ap.PlayerID)
	assert.True(t, ap.Afk)

	_, actIdx := bc.nextBroadcastAny(t, afkIdx, 2*time.Second, ddzmsg.MsgCardPlayed, ddzmsg.MsgPlayerPass)
	assert.Greater(t, actIdx, afkIdx, "自动动作应在转挂机广播之后")
}

// 挂机玩家主动行动 → 自动取消挂机
func TestActiveActionCancelsAfk(t *testing.T) {
	gs, bc := newHarness(t, testTimeouts())
	landlordID, _ := driveToPlaying(t, gs, bc)

	pmsg, _ := bc.nextBroadcast(t, ddzmsg.MsgPlayTurn, 0, 2*time.Second)
	pt := decode[ddzmsg.PlayTurnPayload](t, pmsg)
	require.True(t, pt.MustPlay)

	// 挂机后立即在托管自动行动（~1s）前手动出牌
	require.NoError(t, gs.SetAfk(pt.PlayerID, true))

	tt := newTableTracker()
	collectDealtHands(t, bc, tt)
	bc.mu.Lock()
	for _, m := range bc.targeted[landlordID] {
		if m.Type == ddzmsg.MsgDealCards {
			p := decode[ddzmsg.DealCardsPayload](t, m)
			if len(p.Cards) == 20 {
				tt.setHand(landlordID, ddzmsg.InfosToCards(p.Cards))
			}
		}
	}
	bc.mu.Unlock()

	c := tt.smallestSingle(pt.PlayerID)
	require.NoError(t, gs.PlayCards(pt.PlayerID, ddzmsg.CardsToInfos([]card.Card{c})))

	// afk_changed(true) 后紧跟 afk_changed(false)
	afkOn, idx := bc.nextBroadcast(t, ddzmsg.MsgAfkChanged, 0, 2*time.Second)
	require.True(t, decode[ddzmsg.AfkChangedPayload](t, afkOn).Afk)
	afkOff, _ := bc.nextBroadcast(t, ddzmsg.MsgAfkChanged, idx, 2*time.Second)
	ap := decode[ddzmsg.AfkChangedPayload](t, afkOff)
	assert.False(t, ap.Afk)
	assert.Equal(t, pt.PlayerID, ap.PlayerID)
}
