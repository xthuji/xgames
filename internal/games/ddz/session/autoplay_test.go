package session

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"xgames/internal/games/ddz/bot"
	"xgames/internal/games/ddz/card"
	ddzmsg "xgames/internal/games/ddz/msg"
	"xgames/internal/games/ddz/rule"
	"xgames/internal/platform/protocol"
)

// --- 出牌判定 / 托管回归（修复 F1~F3 的 session 层锚点） ---

// mustParseTestHand 解析测试牌组
func mustParseTestHand(t *testing.T, cards []card.Card) rule.ParsedHand {
	t.Helper()
	ph, err := rule.ParseHand(cards)
	require.NoError(t, err)
	require.NotEqual(t, rule.Invalid, ph.Type)
	return ph
}

// newSilentSession 构造未启动 Actor 的裸 Session（直接调用内部方法做针对性断言）
func newSilentSession(t *testing.T) (*Session, *testBroadcaster) {
	t.Helper()
	players := []*Player{
		{ID: "A", Name: "玩家A", Seat: 0},
		{ID: "B", Name: "玩家B", Seat: 1},
		{ID: "C", Name: "玩家C", Seat: 2},
	}
	bc := newTestBroadcaster()
	gs := New("000000", players, bc, nil, testTimeouts())
	t.Cleanup(gs.stopTurnTimer) // 兜底停掉可能启动的计时器，避免测试结束后触发级联动作
	return gs, bc
}

// lastPlayTurn 最后一条 MsgPlayTurn 广播
func lastPlayTurn(t *testing.T, bc *testBroadcaster) ddzmsg.PlayTurnPayload {
	t.Helper()
	bc.mu.Lock()
	defer bc.mu.Unlock()
	for i := len(bc.broadcasts) - 1; i >= 0; i-- {
		if bc.broadcasts[i].Type == ddzmsg.MsgPlayTurn {
			return decode[ddzmsg.PlayTurnPayload](t, bc.broadcasts[i])
		}
	}
	t.Fatal("未找到 MsgPlayTurn 广播")
	return ddzmsg.PlayTurnPayload{}
}

// TestNotifyPlayTurn_StraightCanBeat 回归（F1/F2）：手牌含可管的 45678（需拆对）
// 面对顺子 34567 时 can_beat=true 且不进入自动过计时器；
// 无牌可压时才 can_beat=false 并进入 timerAutoPass
func TestNotifyPlayTurn_StraightCanBeat(t *testing.T) {
	gs, bc := newSilentSession(t)

	target := mustParseTestHand(t, []card.Card{
		{Rank: card.Rank3}, {Rank: card.Rank4}, {Rank: card.Rank5},
		{Rank: card.Rank6}, {Rank: card.Rank7},
	})
	gs.phase = PhasePlaying
	gs.currentPlayer = 0
	gs.lastPlayerIdx = 1
	gs.lastPlayedHand = target

	// 场景一：手牌含可管顺子 → can_beat=true，正常回合计时
	gs.players[0].Hand = []card.Card{
		{Rank: card.Rank4}, {Rank: card.Rank4}, {Rank: card.Rank5}, {Rank: card.Rank5},
		{Rank: card.Rank6}, {Rank: card.Rank6}, {Rank: card.Rank7}, {Rank: card.Rank7},
		{Rank: card.Rank8},
	}
	gs.notifyPlayTurn()
	turn := lastPlayTurn(t, bc)
	assert.Equal(t, "A", turn.PlayerID)
	assert.False(t, turn.MustPlay)
	assert.True(t, turn.CanBeat, "45678 可压 34567，can_beat 应为 true")
	assert.Equal(t, timerPlay, gs.timerKind, "有牌可压不应进入自动过计时器")
	gs.stopTurnTimer()

	// 场景二：无牌可压 → can_beat=false，进入 2s 自动过计时器
	gs.players[0].Hand = []card.Card{
		{Rank: card.Rank9}, {Rank: card.Rank9}, {Rank: card.Rank9}, {Rank: card.RankK},
	}
	gs.notifyPlayTurn()
	turn = lastPlayTurn(t, bc)
	assert.False(t, turn.CanBeat)
	assert.Equal(t, timerAutoPass, gs.timerKind, "无牌可压应进入自动过计时器")
	gs.stopTurnTimer()
}

// TestAfkLeadPlaysWholePair 回归（F3）：托管领出手牌恰为一对 → 经规则引擎 P0
// 整对打出清空手牌获胜（修复前残局 MCTS 接管拆成单张）
func TestAfkLeadPlaysWholePair(t *testing.T) {
	gs, bc := newSilentSession(t)
	gs.SetEngine(bot.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil))))

	gs.players[0].IsLandlord = true
	gs.players[0].Afk = true
	gs.players[0].Hand = []card.Card{{Rank: card.Rank9}, {Rank: card.Rank9}}
	gs.phase = PhasePlaying
	gs.currentPlayer = 0
	gs.lastPlayerIdx = 0 // 新一轮领出

	gs.autoplayFor(0)

	bc.mu.Lock()
	var played *ddzmsg.CardPlayedPayload
	for i := range bc.broadcasts {
		if bc.broadcasts[i].Type == ddzmsg.MsgCardPlayed {
			p := decode[ddzmsg.CardPlayedPayload](t, bc.broadcasts[i])
			played = &p
		}
	}
	gameOver := false
	for i := range bc.broadcasts {
		if bc.broadcasts[i].Type == protocol.MsgGameOver {
			gameOver = true
		}
	}
	bc.mu.Unlock()

	require.NotNil(t, played, "托管领出应自动出牌")
	require.Len(t, played.Cards, 2, "手牌恰为一对应整对打出，而非拆成单张")
	assert.Equal(t, "对子", played.HandType)
	assert.True(t, gameOver, "整对打出后手牌清空，应立即结算")
}
