package server_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
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

// TestPracticeMatchWithBots M4 验收：人机练习（1 真人 + 2 机器人）打完一整局
func TestPracticeMatchWithBots(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cfg := testConfig()
	cfg.Bot.Enabled = true

	dbPath := fmt.Sprintf("%s/practice-%d.db", t.TempDir(), time.Now().UnixNano())
	st, err := store.Open(dbPath)
	require.NoError(t, err)
	defer st.Close()

	registerTestGames()
	app := server.NewApp(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	setDDZBotDelay(t, 5*time.Millisecond) // 加速测试
	app.Restore(ctx)

	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	defer app.Shutdown()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	c := dialClient(t, ctx, wsURL)
	defer c.close()

	// 1. 人机练习：建房 + 机器人补位（机器人已就绪），真人准备后开局（与普通房间流程一致）
	c.send(protocol.MsgPracticeMatch, struct{}{})
	joined, err := protocol.ParsePayload[protocol.RoomJoinedPayload](c.waitFor(10*time.Second, protocol.MsgRoomJoined))
	require.NoError(t, err)
	require.Len(t, joined.Players, 3)
	botCount := 0
	for _, p := range joined.Players {
		if p.IsBot {
			botCount++
			assert.Contains(t, p.Name, "BOT-")
		}
	}
	assert.Equal(t, 2, botCount, "应有 2 个机器人")

	c.send(protocol.MsgReady, struct{}{}) // 真人准备 → 全员就绪自动开局

	c.waitFor(10*time.Second, protocol.MsgGameStart)
	deal, err := protocol.ParsePayload[msg.DealCardsPayload](c.waitFor(10*time.Second, msg.MsgDealCards))
	require.NoError(t, err)
	hand := msg.InfosToCards(deal.Cards)
	require.Len(t, hand, 17)

	// 2. 叫分：轮到真人则应答，机器人自动决策
	var landlordID string
	var bottomCards []card.Card
	bidTimes := 0
	for landlordID == "" {
		m := c.waitFor(30*time.Second, msg.MsgBidTurn, msg.MsgLandlord)
		if m.Type == msg.MsgLandlord {
			lp, perr := protocol.ParsePayload[msg.LandlordPayload](m)
			require.NoError(t, perr)
			landlordID = lp.PlayerID
			bottomCards = msg.InfosToCards(lp.BottomCards)
			break
		}
		turn, perr := protocol.ParsePayload[msg.BidTurnPayload](m)
		require.NoError(t, perr)
		if turn.PlayerID == c.id {
			score := 0
			if bidTimes == 0 {
				score = 1 // 第一次叫 1 分，其余不叫
			}
			c.send(msg.MsgBid, msg.BidPayload{Score: score})
			bidTimes++
		}
		// 机器人回合：等下一条消息即可
	}
	require.NotEmpty(t, landlordID)
	if landlordID == c.id {
		hand = append(hand, bottomCards...)
	}

	// 3. 出牌直到终局：真人回合决策，机器人回合自动（加倍轮次真人一律不加倍）
	var lastPlayed []card.Card
	gameOver := false
	var over protocol.GameOverPayload
	for i := 0; i < 300 && !gameOver; i++ {
		m := c.waitFor(30*time.Second, msg.MsgPlayTurn, protocol.MsgGameOver, msg.MsgBidTurn)
		if m.Type == protocol.MsgGameOver {
			over, _ = protocol.ParsePayload[protocol.GameOverPayload](m)
			gameOver = true
			break
		}
		if m.Type == msg.MsgBidTurn {
			// 加倍阶段：真人回合应答不加倍，机器人自动决策
			turn, perr := protocol.ParsePayload[msg.BidTurnPayload](m)
			require.NoError(t, perr)
			if turn.PlayerID == c.id {
				c.send(msg.MsgDouble, msg.DoublePayload{Double: false})
			}
			continue
		}
		turn, perr := protocol.ParsePayload[msg.PlayTurnPayload](m)
		require.NoError(t, perr)

		if turn.PlayerID == c.id {
			// 领出回合（MustPlay）无参照牌，其余跟牌
			var ref []card.Card
			if !turn.MustPlay {
				ref = lastPlayed
			}
			if move, ok := pickMove(hand, ref); ok {
				c.send(msg.MsgPlayCards, msg.PlayCardsPayload{Cards: msg.CardsToInfos(move)})
			} else {
				require.False(t, turn.MustPlay, "必须出牌回合却无牌可出")
				c.send(msg.MsgPass, struct{}{})
			}
		}
		// 机器人回合无需动作，直接等本步结果

		r := c.waitFor(30*time.Second, msg.MsgCardPlayed, msg.MsgPlayerPass, protocol.MsgGameOver, protocol.MsgError)
		switch r.Type {
		case protocol.MsgError:
			ep, perr := protocol.ParsePayload[protocol.ErrorPayload](r)
			require.NoError(t, perr)
			t.Fatalf("服务端错误: code=%d message=%s", ep.Code, ep.Message)
		case msg.MsgCardPlayed:
			cp, perr := protocol.ParsePayload[msg.CardPlayedPayload](r)
			require.NoError(t, perr)
			if cp.PlayerID == c.id {
				hand = removeCards(hand, msg.InfosToCards(cp.Cards))
				assert.Len(t, hand, cp.CardsLeft)
			}
			lastPlayed = msg.InfosToCards(cp.Cards)
		case msg.MsgPlayerPass:
			// 状态不变（领出由 turn.MustPlay 判定）
		case protocol.MsgGameOver:
			over, _ = protocol.ParsePayload[protocol.GameOverPayload](r)
			gameOver = true
		}
	}
	require.True(t, gameOver, "人机对局未在限定步数内结束")

	// 4. 结算口径：总分和为零
	require.Len(t, over.Scores, 3)
	sum := 0
	for _, s := range over.Scores {
		sum += s.Score
	}
	assert.Zero(t, sum, "结算总分和应为零")
}
