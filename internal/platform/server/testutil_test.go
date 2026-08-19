package server_test

import (
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"xgames/internal/games"
	"xgames/internal/games/ddz"
	"xgames/internal/games/ddz/bot"
	"xgames/internal/games/chess"
	"xgames/internal/games/gomoku"
	"xgames/internal/games/mahjong"
	"xgames/internal/testutil"
)

var registerGamesOnce sync.Once

// TestMain 将工作目录切到项目根：go test 的 cwd 是包目录，而复盘报告等
// 相对路径（data/replays）按项目根解析；不切换会把报告写到包目录的 data/ 下
func TestMain(m *testing.M) {
	testutil.ChdirProjectRoot()
	os.Exit(m.Run())
}

// registerTestGames 为测试注册全部游戏插件。
// 平台零游戏依赖，游戏由入口装配注册；测试进程内多个用例共享注册表，
// 重复注册会 panic，故用 Once 保证只注册一次。
func registerTestGames() {
	registerGamesOnce.Do(func() {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		ddz.Register(logger, true)
		gomoku.Register(logger, true)
		chess.Register(logger, true)
		mahjong.Register(logger, true)
	})
}

// setDDZBotDelay 缩短斗地主机器人决策延迟（加速测试；需先调用 registerTestGames）
func setDDZBotDelay(t *testing.T, d time.Duration) {
	t.Helper()
	g, ok := games.Get(ddz.ID)
	require.True(t, ok, "斗地主游戏应已注册")
	ctrl, ok := g.NewBotController().(*bot.Controller)
	require.True(t, ok, "机器人控制器应可用（机器人未启用？）")
	ctrl.SetDelay(func() time.Duration { return d })
}
