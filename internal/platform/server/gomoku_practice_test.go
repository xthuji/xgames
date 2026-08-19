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

	"xgames/internal/games"
	gomokugame "xgames/internal/games/gomoku"
	gomokubot "xgames/internal/games/gomoku/bot"
	gkmsg "xgames/internal/games/gomoku/msg"
	"xgames/internal/infra/store"
	"xgames/internal/platform/protocol"
	"xgames/internal/platform/server"
)

// setGomokuBotDelay 缩短五子棋机器人决策延迟（加速测试；需先调用 registerTestGames）
func setGomokuBotDelay(t *testing.T, d time.Duration) {
	t.Helper()
	g, ok := games.Get(gomokugame.ID)
	require.True(t, ok, "五子棋游戏应已注册")
	ctrl, ok := g.NewBotController().(*gomokubot.Controller)
	require.True(t, ok, "机器人控制器应可用（机器人未启用？）")
	ctrl.SetDelay(func() time.Duration { return d })
}

// TestGomokuPracticeBotResponds 人机练习：真人落子后机器人必须在秒级内应答，
// 验证 Room.Broadcast → 机器人控制器 → SubmitBotAction 全链路（不依赖回合超时兜底）。
func TestGomokuPracticeBotResponds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	cfg := testConfig()
	cfg.Bot.Enabled = true

	dbPath := fmt.Sprintf("%s/gk-practice-%d.db", t.TempDir(), time.Now().UnixNano())
	st, err := store.Open(dbPath)
	require.NoError(t, err)
	defer st.Close()

	registerTestGames()
	app := server.NewApp(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	setGomokuBotDelay(t, 5*time.Millisecond)
	app.Restore(ctx)

	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	defer app.Shutdown()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	c := dialClient(t, ctx, wsURL)
	defer c.close()

	// 1. 人机练习：建房 + 机器人补位
	c.send(protocol.MsgPracticeMatch, protocol.PracticeMatchPayload{GameID: gomokugame.ID})
	joined, err := protocol.ParsePayload[protocol.RoomJoinedPayload](c.waitFor(10*time.Second, protocol.MsgRoomJoined))
	require.NoError(t, err)
	require.Len(t, joined.Players, 2)
	var botID string
	for _, p := range joined.Players {
		if p.IsBot {
			botID = p.ID
		}
	}
	require.NotEmpty(t, botID, "应有 1 个机器人")

	// 2. 真人准备 → 开局
	c.send(protocol.MsgReady, struct{}{})
	c.waitFor(10*time.Second, protocol.MsgGameStart)
	turn, err := protocol.ParsePayload[gkmsg.GkTurnPayload](c.waitFor(10*time.Second, gkmsg.MsgGkTurn))
	require.NoError(t, err)
	require.Equal(t, c.id, turn.PlayerID, "黑先：首回合应为真人")

	// 3. 真人落子（中心）
	c.send(gkmsg.MsgGkMove, gkmsg.GkMovePayload{Row: 7, Col: 7})
	mine, err := protocol.ParsePayload[gkmsg.GkMoveMadePayload](c.waitFor(5*time.Second, gkmsg.MsgGkMoveMade))
	require.NoError(t, err)
	assert.Equal(t, c.id, mine.PlayerID)

	// 4. 机器人回合：必须在 5s 内应答（远小于回合超时，证明是控制器驱动而非超时兜底）
	botTurn, err := protocol.ParsePayload[gkmsg.GkTurnPayload](c.waitFor(5*time.Second, gkmsg.MsgGkTurn))
	require.NoError(t, err)
	require.Equal(t, botID, botTurn.PlayerID)

	botMove, err := protocol.ParsePayload[gkmsg.GkMoveMadePayload](c.waitFor(5*time.Second, gkmsg.MsgGkMoveMade))
	require.NoError(t, err, "机器人未在 5s 内落子（控制器链路失效）")
	assert.Equal(t, botID, botMove.PlayerID)
	require.True(t, botMove.Row >= 0 && botMove.Row < 15 && botMove.Col >= 0 && botMove.Col < 15, "机器人落子应在棋盘内")
	assert.False(t, botMove.Row == mine.Row && botMove.Col == mine.Col, "机器人不应落在已占用位置")
}

// TestNoAutoRoomAfterLeavingPractice 人机练习建房的真人直接离开后，残留的"仅机器人占席"房间
// 必须被解散，否者在单房间约束下会被误认为"自动创建/自动存在"的房间，阻塞后续建房/人机练习。
// 覆盖场景：真人未被分配为机器人、建房后未准备即离开（LeaveRoom → 剩余仅机器人 → 解散）。
func TestNoAutoRoomAfterLeavingPractice(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	cfg := testConfig()
	cfg.Bot.Enabled = true

	dbPath := fmt.Sprintf("%s/gk-auto-%d.db", t.TempDir(), time.Now().UnixNano())
	st, err := store.Open(dbPath)
	require.NoError(t, err)
	defer st.Close()

	registerTestGames()
	app := server.NewApp(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	setGomokuBotDelay(t, 5*time.Millisecond)
	app.Restore(ctx)

	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	defer app.Shutdown()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	c := dialClient(t, ctx, wsURL)
	defer c.close()

	// 1. 人机练习建房（真人 + 1 机器人，房间等待中；真人未准备）
	c.send(protocol.MsgPracticeMatch, protocol.PracticeMatchPayload{GameID: gomokugame.ID})
	joined, err := protocol.ParsePayload[protocol.RoomJoinedPayload](c.waitFor(10*time.Second, protocol.MsgRoomJoined))
	require.NoError(t, err)
	require.Len(t, joined.Players, 2)

	// 2. 真人直接离开房间（Waiting 状态可离开；不触发对局）
	c.send(protocol.MsgLeaveRoom, struct{}{})

	// 3. 房间列表应已清空：残留机器人占席房间必须被解散
	c.send(protocol.MsgGetRoomList, protocol.GetRoomListPayload{GameID: gomokugame.ID})
	list, err := protocol.ParsePayload[protocol.RoomListResultPayload](c.waitFor(5*time.Second, protocol.MsgRoomListResult))
	require.NoError(t, err)
	require.Empty(t, list.Rooms, "真人离开后不应残留仅机器人占席的房间（会被误认为自动存在的房间）")

	// 4. 可再次人机练习（不应触发单房间冲突 ErrRoomExists）
	c.send(protocol.MsgPracticeMatch, protocol.PracticeMatchPayload{GameID: gomokugame.ID})
	_, err = protocol.ParsePayload[protocol.RoomJoinedPayload](c.waitFor(10*time.Second, protocol.MsgRoomJoined))
	require.NoError(t, err, "再次人机练习应成功，证明旧房间已真正解散")
}
