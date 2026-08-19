package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/msg"
	"xgames/internal/games/ddz/rule"
	"xgames/internal/infra/config"
	"xgames/internal/infra/store"
	"xgames/internal/platform/protocol"
	"xgames/internal/platform/server"
)

// --- 测试用 WS 客户端 ---

type itClient struct {
	t     *testing.T
	conn  *websocket.Conn
	id    string
	name  string
	token string
	inbox chan protocol.Message
}

func dialClient(t *testing.T, ctx context.Context, wsURL string, machineID ...string) *itClient {
	t.Helper()
	// 如果提供了 machineID，添加到 URL 参数中
	dialURL := wsURL
	if len(machineID) > 0 && machineID[0] != "" {
		dialURL = wsURL + "?machine_id=" + machineID[0]
	}
	conn, _, err := websocket.Dial(ctx, dialURL, nil)
	require.NoError(t, err, "WS 连接失败")

	c := &itClient{t: t, conn: conn, inbox: make(chan protocol.Message, 512)}
	go c.readLoop(ctx)

	m := c.waitFor(10*time.Second, protocol.MsgConnected)
	p, err := protocol.ParsePayload[protocol.ConnectedPayload](m)
	require.NoError(t, err)
	require.NotEmpty(t, p.PlayerID)
	require.NotEmpty(t, p.ReconnectToken)
	c.id, c.name, c.token = p.PlayerID, p.PlayerName, p.ReconnectToken
	return c
}

func (c *itClient) readLoop(ctx context.Context) {
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		var m protocol.Message
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		c.inbox <- m
	}
}

func (c *itClient) send(mt protocol.MessageType, payload any) {
	c.t.Helper()
	data, err := json.Marshal(protocol.NewMessage(mt, payload))
	require.NoError(c.t, err)
	err = c.conn.Write(context.Background(), websocket.MessageText, data)
	require.NoError(c.t, err, "发送 %s 失败", mt)
}

// waitFor 等待任一指定类型消息，其余消息丢弃
func (c *itClient) waitFor(within time.Duration, types ...protocol.MessageType) protocol.Message {
	c.t.Helper()
	deadline := time.After(within)
	for {
		select {
		case m := <-c.inbox:
			for _, want := range types {
				if m.Type == want {
					return m
				}
			}
		case <-deadline:
			c.t.Fatalf("等待消息 %v 超时", types)
		}
	}
}

func (c *itClient) close() { _ = c.conn.Close(websocket.StatusNormalClosure, "bye") }

// --- 出牌决策（贪心：最小单张/对子/三张/炸弹/火箭） ---

func pickMove(hand, last []card.Card) ([]card.Card, bool) {
	if len(last) == 0 {
		return hand[:1:1], true // 新一轮：出任意单张
	}
	lh, err := rule.ParseHand(last)
	if err != nil {
		return nil, false
	}
	beat := func(candidate []card.Card) bool {
		ch, perr := rule.ParseHand(candidate)
		return perr == nil && rule.CanBeat(ch, lh)
	}
	for i := range hand { // 单张
		if beat([]card.Card{hand[i]}) {
			return []card.Card{hand[i]}, true
		}
	}
	byRank := map[card.Rank][]card.Card{}
	for _, c := range hand {
		byRank[c.Rank] = append(byRank[c.Rank], c)
	}
	for n := 2; n <= 4; n++ { // 对子 / 三张 / 炸弹
		for _, group := range byRank {
			if len(group) >= n && beat(group[:n:n]) {
				return group[:n:n], true
			}
		}
	}
	var jokers []card.Card // 火箭
	for _, c := range hand {
		if c.Suit == card.Joker {
			jokers = append(jokers, c)
		}
	}
	if len(jokers) == 2 && beat(jokers) {
		return jokers, true
	}
	return nil, false
}

func removeCards(hand, played []card.Card) []card.Card {
	out := append([]card.Card(nil), hand...)
	for _, p := range played {
		for i, c := range out {
			if c == p {
				out = append(out[:i], out[i+1:]...)
				break
			}
		}
	}
	return out
}

// --- 测试装配 ---

func testConfig() *config.Config {
	return &config.Config{
		Game: config.GameConfig{
			TurnTimeout:        10,
			BidTimeout:         10,
			RoomTimeout:        1,
			ShutdownTimeout:    1,
			OfflineWaitTimeout: 10,
		},
		Security: config.SecurityConfig{
			AllowedOrigins: []string{"*"},
			RateLimit:      config.RateLimitConfig{MaxPerSecond: 100, MaxPerMinute: 1000, BanDuration: 1},
			MessageLimit:   config.MessageLimitConfig{MaxPerSecond: 100},
		},
	}
}

// TestIntegrationFullGameOverWS M3 验收：3 个 WS 客户端建房 → 准备 → 打完一整局 → 结算落库 → 断线重连
func TestIntegrationFullGameOverWS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	dbPath := fmt.Sprintf("%s/it-%d.db", t.TempDir(), time.Now().UnixNano())
	st, err := store.Open(dbPath)
	require.NoError(t, err)
	defer st.Close()

	registerTestGames()
	app := server.NewApp(testConfig(), st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	app.Restore(ctx)

	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	defer app.Shutdown()

	// 1. 三个客户端连接（使用不同的 machineID 确保不同的玩家 ID）
	c0 := dialClient(t, ctx, wsURL, "test-machine-0")
	c1 := dialClient(t, ctx, wsURL, "test-machine-1")
	c2 := dialClient(t, ctx, wsURL, "test-machine-2")
	defer c0.close()
	defer c1.close()
	defer c2.close()
	clients := []*itClient{c0, c1, c2}
	byID := map[string]*itClient{c0.id: c0, c1.id: c1, c2.id: c2}

	// 2. 建房 / 加入
	c0.send(protocol.MsgCreateRoom, struct{}{})
	created, err := protocol.ParsePayload[protocol.RoomCreatedPayload](c0.waitFor(5*time.Second, protocol.MsgRoomCreated))
	require.NoError(t, err)
	roomCode := created.RoomCode
	require.Len(t, roomCode, 6)

	c1.send(protocol.MsgJoinRoom, protocol.JoinRoomPayload{RoomCode: roomCode})
	_, _ = protocol.ParsePayload[protocol.RoomJoinedPayload](c1.waitFor(5*time.Second, protocol.MsgRoomJoined))
	c2.send(protocol.MsgJoinRoom, protocol.JoinRoomPayload{RoomCode: roomCode})
	joined2, err := protocol.ParsePayload[protocol.RoomJoinedPayload](c2.waitFor(5*time.Second, protocol.MsgRoomJoined))
	require.NoError(t, err)
	assert.Len(t, joined2.Players, 3)

	// 4. 全员准备 → 自动开局
	for _, c := range clients {
		c.send(protocol.MsgReady, struct{}{})
	}
	for _, c := range clients {
		c.waitFor(5*time.Second, protocol.MsgGameStart)
	}

	// 5. 收牌
	hands := map[string][]card.Card{}
	for _, c := range clients {
		deal, err := protocol.ParsePayload[msg.DealCardsPayload](c.waitFor(5*time.Second, msg.MsgDealCards))
		require.NoError(t, err)
		require.Len(t, deal.Cards, 17)
		hands[c.id] = msg.InfosToCards(deal.Cards)
	}

	// 6. 叫分：第一人叫 1 分，其余不叫
	bidDecisions := 0
	var landlordID string
	var bottomCards []card.Card
	for landlordID == "" {
		m := c0.waitFor(10*time.Second, msg.MsgBidTurn, msg.MsgLandlord)
		if m.Type == msg.MsgLandlord {
			lp, perr := protocol.ParsePayload[msg.LandlordPayload](m)
			require.NoError(t, perr)
			landlordID = lp.PlayerID
			bottomCards = msg.InfosToCards(lp.BottomCards)
			assert.Equal(t, 1, lp.Multiplier)
			break
		}
		turn, perr := protocol.ParsePayload[msg.BidTurnPayload](m)
		require.NoError(t, perr)
		require.Equal(t, "call", turn.Phase)
		score := 0
		if bidDecisions == 0 {
			score = 1
		}
		byID[turn.PlayerID].send(msg.MsgBid, msg.BidPayload{Score: score})
		bidDecisions++
	}
	require.NotEmpty(t, landlordID)
	// 地主收底牌
	hands[landlordID] = append(hands[landlordID], bottomCards...)
	require.Len(t, hands[landlordID], 20)

	// 6.5 加倍阶段：两名农民均不加倍
	for i := 0; i < 2; i++ {
		m := c0.waitFor(10*time.Second, msg.MsgBidTurn)
		turn, perr := protocol.ParsePayload[msg.BidTurnPayload](m)
		require.NoError(t, perr)
		require.Equal(t, "double", turn.Phase)
		byID[turn.PlayerID].send(msg.MsgDouble, msg.DoublePayload{Double: false})
	}

	// 7. 出牌直到终局（中央驱动器：等 play_turn → 决策 → 等出牌结果）
	var lastPlayed []card.Card
	lastPlayerID := ""
	gameOver := false
	var over protocol.GameOverPayload
	for i := 0; i < 300 && !gameOver; i++ {
		m := c0.waitFor(30*time.Second, msg.MsgPlayTurn, protocol.MsgGameOver)
		if m.Type == protocol.MsgGameOver {
			over, _ = protocol.ParsePayload[protocol.GameOverPayload](m)
			gameOver = true
			break
		}
		turn, perr := protocol.ParsePayload[msg.PlayTurnPayload](m)
		require.NoError(t, perr)
		tc := byID[turn.PlayerID]

		ref := lastPlayed
		if lastPlayerID == turn.PlayerID {
			ref = nil // 两家都不要，本轮由上家重新领出
		}
		if move, ok := pickMove(hands[turn.PlayerID], ref); ok {
			tc.send(msg.MsgPlayCards, msg.PlayCardsPayload{Cards: msg.CardsToInfos(move)})
		} else {
			require.False(t, turn.MustPlay, "必须出牌回合却无牌可出")
			tc.send(msg.MsgPass, struct{}{})
		}

		// 等待本步结果
		r := c0.waitFor(30*time.Second, msg.MsgCardPlayed, msg.MsgPlayerPass, protocol.MsgGameOver)
		switch r.Type {
		case msg.MsgCardPlayed:
			cp, perr := protocol.ParsePayload[msg.CardPlayedPayload](r)
			require.NoError(t, perr)
			played := msg.InfosToCards(cp.Cards)
			hands[cp.PlayerID] = removeCards(hands[cp.PlayerID], played)
			assert.Equal(t, len(hands[cp.PlayerID]), cp.CardsLeft)
			lastPlayed, lastPlayerID = played, cp.PlayerID
		case msg.MsgPlayerPass:
			// 状态不变
		case protocol.MsgGameOver:
			over, _ = protocol.ParsePayload[protocol.GameOverPayload](r)
			gameOver = true
		}
	}
	require.True(t, gameOver, "对局未在限定步数内结束")

	// 8. 结算口径：总分和为零
	require.Len(t, over.Scores, 3)
	sum := 0
	for _, s := range over.Scores {
		sum += s.Score
	}
	assert.Zero(t, sum, "结算总分和应为零")

	// 9. 结算落库（resultSink 异步写，轮询统计）
	landlordClient := byID[landlordID]
	var stats *protocol.StatsResultPayload
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		landlordClient.send(protocol.MsgGetStats, struct{}{})
		m := landlordClient.waitFor(5*time.Second, protocol.MsgStatsResult)
		s, perr := protocol.ParsePayload[protocol.StatsResultPayload](m)
		require.NoError(t, perr)
		if s.TotalGames > 0 {
			stats = &s
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotNil(t, stats, "结算未落库")
	assert.Equal(t, 1, stats.TotalGames)

	// 10. 断线重连：关闭连接 → 新连接凭旧令牌恢复身份
	c2.close()
	time.Sleep(200 * time.Millisecond) // 等 onDisconnect 完成

	c2b := dialClient(t, ctx, wsURL) // 握手时获得临时身份
	defer c2b.close()
	require.NotEqual(t, c2.id, c2b.id)

	c2b.send(protocol.MsgReconnect, protocol.ReconnectPayload{PlayerID: c2.id, Token: c2.token})
	rc, err := protocol.ParsePayload[protocol.ReconnectedPayload](c2b.waitFor(10*time.Second, protocol.MsgReconnected))
	require.NoError(t, err)
	assert.Equal(t, c2.id, rc.PlayerID)
	assert.Equal(t, roomCode, rc.RoomCode, "对局结束房间复位 Waiting，重连应回到原房间")
	assert.NotEmpty(t, rc.ReconnectToken)
	assert.NotEqual(t, c2.token, rc.ReconnectToken, "重连成功后令牌应轮换")

	// 旧令牌已失效
	c3 := dialClient(t, ctx, wsURL)
	defer c3.close()
	c3.send(protocol.MsgReconnect, protocol.ReconnectPayload{PlayerID: c2.id, Token: c2.token})
	errMsg := c3.waitFor(5*time.Second, protocol.MsgError)
	ep, perr := protocol.ParsePayload[protocol.ErrorPayload](errMsg)
	require.NoError(t, perr)
	assert.NotZero(t, ep.Code)
}
