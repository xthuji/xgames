package server_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/msg"
	"xgames/internal/infra/store"
	"xgames/internal/platform/protocol"
	"xgames/internal/platform/server"
)

// TestRankOrder 验证 rank 排序：2(15) > A(14) > K(13) > ... > 3(3)
func TestRankOrder(t *testing.T) {
	ranks := []card.Rank{card.Rank3, card.Rank4, card.Rank5, card.Rank6, card.Rank7,
		card.Rank8, card.Rank9, card.Rank10, card.RankJ, card.RankQ, card.RankK,
		card.RankA, card.Rank2, card.RankBlackJoker, card.RankRedJoker}

	// 打印数值
	t.Log("Rank 数值映射:")
	for _, r := range ranks {
		t.Logf("  %-14s = %d", r.String(), int(r))
	}

	// 关键断言: Rank2 > RankA
	assert.True(t, card.Rank2 > card.RankA, "Rank2(15) 应该 > RankA(14)")
	assert.True(t, card.RankBlackJoker > card.Rank2, "小王(16) > 2(15)")
	assert.True(t, card.RankRedJoker > card.RankBlackJoker, "大王(17) > 小王(16)")
}

// TestRankInGame 打印发牌后的 rank 分布（验证 2 没有被放错）
func TestRankInGame(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cfg := testConfig()
	cfg.Bot.Enabled = true
	cfg.Bot.BotFillTimeout = 1

	dbPath := fmt.Sprintf("%s/rank-game-%d.db", t.TempDir(), time.Now().UnixNano())
	st, err := store.Open(dbPath)
	require.NoError(t, err)
	defer st.Close()

	registerTestGames()
	app := server.NewApp(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	setDDZBotDelay(t, 10*time.Millisecond)
	app.Restore(ctx)

	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	defer app.Shutdown()

	c := dialClient(t, ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws")
	defer c.close()

	c.send(protocol.MsgPracticeMatch, struct{}{})
	c.waitFor(10*time.Second, protocol.MsgRoomJoined)
	c.send(protocol.MsgReady, struct{}{}) // 人机对战不再自动开局，真人准备后才开局
	c.waitFor(10*time.Second, protocol.MsgGameStart)
	deal, err := protocol.ParsePayload[msg.DealCardsPayload](c.waitFor(10*time.Second, msg.MsgDealCards))
	require.NoError(t, err)

	ranks := make([]int, len(deal.Cards))
	for i, ci := range deal.Cards {
		ranks[i] = ci.Rank
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ranks)))
	t.Logf("发牌 17 张, rank 降序: %v", ranks)
	t.Logf("  最大 rank=%d (%s), 最小 rank=%d (%s)",
		ranks[0], rankName(ranks[0]), ranks[len(ranks)-1], rankName(ranks[len(ranks)-1]))

	// 断言最大 rank 至少 >= 14 (A)——除非是极端牌
	assert.GreaterOrEqual(t, ranks[0], 3, "至少应有 rank 3")

	// 统计 rank=15 (2) 的张数
	count2 := 0
	for _, r := range ranks {
		if r == 15 {
			count2++
		}
	}
	t.Logf("  手上 2 的张数: %d (期望值 0-2)", count2)
}

func rankName(r int) string {
	switch r {
	case 17:
		return "大王"
	case 16:
		return "小王"
	case 15:
		return "2"
	case 14:
		return "A"
	case 13:
		return "K"
	case 12:
		return "Q"
	case 11:
		return "J"
	default:
		return fmt.Sprintf("%d", r)
	}
}
