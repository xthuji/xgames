package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"xgames/internal/games"
	mahjonggame "xgames/internal/games/mahjong"
	mahjongbot "xgames/internal/games/mahjong/bot"
	mahjongsrv "xgames/internal/games/mahjong/session"
	"xgames/internal/games/mahjong/rule"
	mjmsg "xgames/internal/games/mahjong/msg"
	"xgames/internal/infra/store"
	"xgames/internal/platform/protocol"
	"xgames/internal/platform/server"
)

// setMahjongBotDelay 缩短麻将机器人决策延迟（加速测试；需先调用 registerTestGames）
func setMahjongBotDelay(t *testing.T, d time.Duration) {
	t.Helper()
	g, ok := games.Get(mahjonggame.ID)
	require.True(t, ok, "麻将游戏应已注册")
	ctrl, ok := g.NewBotController().(*mahjongbot.Controller)
	require.True(t, ok, "机器人控制器应可用（机器人未启用？）")
	ctrl.SetDelay(func() time.Duration { return d })
	// 同步关闭语音节奏预算（1.5s/拍的托管/bot 自动动作等待会拖慢无 UI 测试）
	restorePacing := mahjongsrv.SetMinActionGapForTest(0)
	t.Cleanup(restorePacing)
}

// TestMahjongPracticeFullGame 人机练习打完一整局：真人 + 3 机器人。
// 真人策略：轮到自己就打第一张；可胡就胡；可碰就碰。
// 验证：建房补位 → 发牌 → 回合轮转 → 碰/胡 → 结算（胡牌或流局）。
func TestMahjongPracticeFullGame(t *testing.T) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// 确定性骰子：让座位 0（真人）稳定当庄（后续断言依赖庄家先摸第 14 张）
	rule.DiceRoller = func() int { return 6 }
	defer func() { rule.DiceRoller = nil }()

	cfg := testConfig()
	cfg.Bot.Enabled = true

	// 会话 Actor 经 slog.Default() 输出：路由到 t.Log 便于定位停顿/拒绝
	logW := &testLogWriter{t: t}
	prevDefault := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logW, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer func() {
		logW.disable()
		slog.SetDefault(prevDefault)
	}()

	dbPath := fmt.Sprintf("%s/mj-practice-%d.db", t.TempDir(), time.Now().UnixNano())
	st, err := store.Open(dbPath)
	require.NoError(t, err)
	defer st.Close()

	registerTestGames()
	app := server.NewApp(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	setMahjongBotDelay(t, 5*time.Millisecond)
	app.Restore(ctx)

	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	defer app.Shutdown()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	c := dialClient(t, ctx, wsURL)
	defer c.close()

	// 1. 人机练习：建房 + 机器人补满 4 席
	c.send(protocol.MsgPracticeMatch, protocol.PracticeMatchPayload{GameID: mahjonggame.ID})
	joined, err := protocol.ParsePayload[protocol.RoomJoinedPayload](c.waitFor(10*time.Second, protocol.MsgRoomJoined))
	require.NoError(t, err)
	require.Len(t, joined.Players, 4, "麻将人机练习应为 1 真人 + 3 机器人")
	bots := 0
	for _, p := range joined.Players {
		if p.IsBot {
			bots++
		}
	}
	require.Equal(t, 3, bots, "应有 3 个机器人")

	// 2. 真人准备 → 开局 → 收到手牌
	c.send(protocol.MsgReady, struct{}{})
	c.waitFor(10*time.Second, protocol.MsgGameStart)

	stateMsg := c.waitFor(10*time.Second, protocol.MsgGameState)
	stateP, err := protocol.ParsePayload[protocol.GameStatePayload](stateMsg)
	require.NoError(t, err)
	require.True(t, stateP.Available)

	var handState struct {
		Hand       []int `json:"hand"`
		WallRemain int   `json:"wall_remain"`
	}
	require.NoError(t, json.Unmarshal(stateP.State, &handState))
	require.Len(t, handState.Hand, 14, "开局手牌应为 14 张（庄家先摸一张）")
	require.Equal(t, rule.TotalTiles-4*13-1, handState.WallRemain, "发牌后牌墙余量")
	myHand := handState.Hand

	removeTiles := func(tile, n int) {
		for k := 0; k < n; k++ {
			for i, h := range myHand {
				if h == tile {
					myHand = append(myHand[:i], myHand[i+1:]...)
					break
				}
			}
		}
	}

	// 3. 对局主循环
	firstTurn := ""
	selfDraw := false // 最近一次摸牌是我（自摸胡判定用）
	myTurns := 0
	myPongs := 0
	ended := false

	for i := 0; i < 3000 && !ended; i++ {
		m := c.waitFor(30*time.Second,
			mjmsg.MsgMjTurn, mjmsg.MsgMjDraw, mjmsg.MsgMjActionAvail,
			mjmsg.MsgMjDiscarded, mjmsg.MsgMjPongMade,
			protocol.MsgGameOver, protocol.MsgError)

		switch m.Type {
		case protocol.MsgError:
			ep, _ := protocol.ParsePayload[protocol.ErrorPayload](m)
			t.Fatalf("收到服务端错误: code=%d msg=%s", ep.Code, ep.Message)

		case protocol.MsgGameOver:
			gop, err := protocol.ParsePayload[protocol.GameOverPayload](m)
			require.NoError(t, err)
			require.Len(t, gop.Scores, 4, "结算应包含全部 4 名玩家")
			ended = true

		case mjmsg.MsgMjTurn:
			p, err := protocol.ParsePayload[mjmsg.MjTurnPayload](m)
			require.NoError(t, err)
			if firstTurn == "" {
				firstTurn = p.PlayerID
			}
			if p.PlayerID != c.id {
				continue
			}
			require.NotEmpty(t, myHand, "轮到我出牌时手牌不应为空")
			tile := myHand[0]
			myHand = myHand[1:]
			selfDraw = false
			myTurns++
			c.send(mjmsg.MsgMjDiscard, mjmsg.MjDiscardPayload{Tile: tile})

		case mjmsg.MsgMjDraw:
			p, err := protocol.ParsePayload[mjmsg.MjDrawPayload](m)
			require.NoError(t, err)
			myHand = append(myHand, p.Tile)
			selfDraw = true

		case mjmsg.MsgMjActionAvail:
			p, err := protocol.ParsePayload[mjmsg.MjActionAvailPayload](m)
			require.NoError(t, err)
			switch {
			case p.CanWin:
				c.send(mjmsg.MsgMjWin, mjmsg.MjWinPayload{IsSelfDraw: selfDraw})
			case p.CanPong:
				removeTiles(p.Tile, 2)
				myPongs++
				c.send(mjmsg.MsgMjPong, mjmsg.MjPongPayload{Tile: p.Tile})
			default:
				c.send(mjmsg.MsgMjPass, mjmsg.MjPassPayload{})
			}

		case mjmsg.MsgMjDiscarded, mjmsg.MsgMjPongMade:
			// 他人行为广播，本地无需跟踪（手牌镜像只受自己的摸/打/碰影响）
		}
	}

	require.True(t, ended, "对局应在消息上限内结束")
	assert.Equal(t, c.id, firstTurn, "庄家（座位 0）应为房间创建人，首回合即真人")
	assert.GreaterOrEqual(t, myTurns, 1, "真人至少应出牌一次")
	t.Logf("对局完成: 真人出牌 %d 次, 碰 %d 次, 耗时 %s", myTurns, myPongs, time.Since(start).Round(time.Millisecond))

	// 整局应在远小于"全靠回合超时兜底"的时间内完成（超时兜底 = 每回合 10s），
	// 证明机器人由控制器驱动而非超时驱动。
	assert.Less(t, time.Since(start), 2*time.Minute, "整局耗时过长，疑似依赖回合超时兜底")
}

// buildDeterministicWall 构造一面确定性牌墙，使真人通过机器人弃牌（点炮）胡牌。
//
// 牌墙布局（庄家先摸一张修复后）：
//   - 位置 0-12: 真人初始 13 张（七对听牌，听牌 3）
//   - 位置 13-25: bot0 手牌
//   - 位置 26-38: bot1 手牌
//   - 位置 39-51: bot2 手牌
//   - 位置 52: 庄家第 14 张（牌 6，真人首回合弃掉）
//   - 位置 53+: 填充牌（按类型升序）
//
// 摸牌顺序（真人首弃后）：bot0 摸 fill[0]=0, bot1 摸 fill[1]=1, bot2 摸 fill[2]=3。
// bot2 弃出牌 3 → 真人点炮胡（七对）。
func buildDeterministicWall() []int {
	// 真人：6 对 + 单张 3（听牌 3）。所有牌型 0-26 合法。
	// 用掉 0:2, 1:2, 2:2, 3:1, 9:2, 18:2, 19:2
	humanHand := []int{0, 0, 1, 1, 2, 2, 3, 9, 9, 18, 18, 19, 19}

	// 庄家第 14 张：牌 6（真人首回合弃掉，保持听牌）
	dealerDraw := []int{6}

	// bot0：贡献 0:1, 1:1, 2:2 使 fill 中 0/1 各 1 张、2 为 0 张，
	// 从而 fill[2] 恰好是牌 3（真人赢牌）。
	// 摸到 0 后弃出牌 1（真人可碰但测试中选择过）。
	bot0 := []int{0, 1, 2, 2, 5, 5, 7, 7, 11, 11, 12, 12, 13}

	// bot1：6 对 + 单张 20。摸到 1 后弃出牌 1（真人可碰但选择过）。
	bot1 := []int{4, 4, 8, 8, 14, 14, 15, 15, 16, 16, 17, 17, 20}

	// bot2：6 对 + 单张 10。摸到牌 3 后弃出（usefulness=0，最先被弃）。
	// 真人点炮胡。
	bot2 := []int{10, 21, 21, 22, 22, 23, 23, 24, 24, 25, 25, 26, 26}

	used := make(map[int]int)
	count := func(tiles []int) {
		for _, t := range tiles {
			used[t]++
		}
	}
	count(humanHand)
	count(dealerDraw)
	count(bot0)
	count(bot1)
	count(bot2)

	var fill []int
	for t := 0; t < rule.NumTypes; t++ {
		remaining := rule.Copies - used[t]
		for c := 0; c < remaining; c++ {
			fill = append(fill, t)
		}
	}

	wall := make([]int, 0, rule.TotalTiles)
	wall = append(wall, humanHand...)
	wall = append(wall, bot0...)
	wall = append(wall, bot1...)
	wall = append(wall, bot2...)
	wall = append(wall, dealerDraw...)
	wall = append(wall, fill...)
	return wall
}

// TestMahjongWinPath 验证胡牌路径：真人通过机器人弃牌（点炮）完成胡牌。
// 使用确定性牌墙，确保第一个机器人弃出的牌恰好是真人听的牌。
func TestMahjongWinPath(t *testing.T) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 确定性骰子：让座位 0（真人）稳定当庄（确定性牌墙布局依赖庄家=真人，
	// 庄家先摸走位置 52 的牌 6 作为第 14 张）
	rule.DiceRoller = func() int { return 6 }
	defer func() { rule.DiceRoller = nil }()

	rule.WallGenerator = buildDeterministicWall
	defer func() { rule.WallGenerator = nil }()

	cfg := testConfig()
	cfg.Bot.Enabled = true

	logW := &testLogWriter{t: t}
	prevDefault := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logW, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer func() {
		logW.disable()
		slog.SetDefault(prevDefault)
	}()

	dbPath := fmt.Sprintf("%s/mj-win-%d.db", t.TempDir(), time.Now().UnixNano())
	st, err := store.Open(dbPath)
	require.NoError(t, err)
	defer st.Close()

	registerTestGames()
	app := server.NewApp(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	setMahjongBotDelay(t, 5*time.Millisecond)
	app.Restore(ctx)

	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	defer app.Shutdown()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	c := dialClient(t, ctx, wsURL)
	defer c.close()

	// 建房
	c.send(protocol.MsgPracticeMatch, protocol.PracticeMatchPayload{GameID: mahjonggame.ID})
	_, err = protocol.ParsePayload[protocol.RoomJoinedPayload](c.waitFor(10*time.Second, protocol.MsgRoomJoined))
	require.NoError(t, err)

	// 准备 → 开局
	c.send(protocol.MsgReady, struct{}{})
	c.waitFor(10*time.Second, protocol.MsgGameStart)

	stateMsg := c.waitFor(10*time.Second, protocol.MsgGameState)
	stateP, err := protocol.ParsePayload[protocol.GameStatePayload](stateMsg)
	require.NoError(t, err)
	require.True(t, stateP.Available)

	var handState struct {
		Hand []int `json:"hand"`
	}
	require.NoError(t, json.Unmarshal(stateP.State, &handState))
	require.Len(t, handState.Hand, 14, "开局手牌应为 14 张（庄家先摸一张）")
	t.Logf("真人手牌: %v", handState.Hand)

	// 对局循环：真人听牌后等机器人弃出赢牌 → 胡
	myHand := handState.Hand
	ended := false
	wonByHuman := false
	selfDraw := false // tracks whether the last draw was mine (for self-draw win)

	for i := 0; i < 200 && !ended; i++ {
		m := c.waitFor(15*time.Second,
			mjmsg.MsgMjTurn, mjmsg.MsgMjDraw, mjmsg.MsgMjActionAvail,
			mjmsg.MsgMjDiscarded, mjmsg.MsgMjPongMade,
			protocol.MsgGameOver, protocol.MsgError)

		switch m.Type {
		case protocol.MsgError:
			ep, _ := protocol.ParsePayload[protocol.ErrorPayload](m)
			t.Fatalf("收到服务端错误: code=%d msg=%s", ep.Code, ep.Message)

		case protocol.MsgGameOver:
			gop, err := protocol.ParsePayload[protocol.GameOverPayload](m)
			require.NoError(t, err)
			ended = true
			if gop.WinnerID == c.id {
				wonByHuman = true
				t.Logf("真人胡牌! 得分: ")
				for _, s := range gop.Scores {
					t.Logf("  %s: %d", s.PlayerName, s.Score)
				}
			} else {
				t.Logf("对局结束，赢家: %s", gop.WinnerName)
			}

		case mjmsg.MsgMjTurn:
			p, err := protocol.ParsePayload[mjmsg.MjTurnPayload](m)
			require.NoError(t, err)
			if p.PlayerID != c.id {
				continue
			}
			require.NotEmpty(t, myHand)
			// 弃掉庄家摸到的牌 6，保持七对听牌（听牌 3）
			tile := -1
			for idx, h := range myHand {
				if h == 6 {
					tile = 6
					myHand = append(myHand[:idx], myHand[idx+1:]...)
					break
				}
			}
			if tile == -1 {
				tile = myHand[0]
				myHand = myHand[1:]
			}
			c.send(mjmsg.MsgMjDiscard, mjmsg.MjDiscardPayload{Tile: tile})

		case mjmsg.MsgMjDraw:
			p, err := protocol.ParsePayload[mjmsg.MjDrawPayload](m)
			require.NoError(t, err)
			myHand = append(myHand, p.Tile)
			selfDraw = true

		case mjmsg.MsgMjActionAvail:
			p, err := protocol.ParsePayload[mjmsg.MjActionAvailPayload](m)
			require.NoError(t, err)
			// 只胡不碰：碰会破坏七对子手牌结构
			if p.CanWin {
				c.send(mjmsg.MsgMjWin, mjmsg.MjWinPayload{IsSelfDraw: selfDraw})
			} else {
				c.send(mjmsg.MsgMjPass, mjmsg.MjPassPayload{})
			}

		case mjmsg.MsgMjDiscarded, mjmsg.MsgMjPongMade:
		}
	}

	require.True(t, ended, "对局应在消息上限内结束")
	require.True(t, wonByHuman, "真人应通过点炮胡牌")
	t.Logf("胡牌路径验证通过，耗时 %s", time.Since(start).Round(time.Millisecond))
}

// testLogWriter 把 slog 输出路由到 t.Log（加锁；测试结束后禁用，避免延迟协程写入已结束的测试）
type testLogWriter struct {
	mu       sync.Mutex
	t        *testing.T
	disabled bool
}

func (w *testLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.disabled {
		w.t.Log(strings.TrimSpace(string(p)))
	}
	return len(p), nil
}

func (w *testLogWriter) disable() {
	w.mu.Lock()
	w.disabled = true
	w.mu.Unlock()
}
