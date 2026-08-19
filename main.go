// XGames 多游戏平台入口。
//
// 用法：
//
//	xgames                    Wails 桌面窗口模式
//	xgames -no-window         无头服务模式（纯 HTTP/WS，可选自动打开浏览器）
//	xgames -dev               开发模式：Go 单进程 + 文件监听自动构建前端
//
// 默认行为：内嵌窗口 + 本地 HTTP 服务。
// -no-window 切换为纯服务模式（无头部署 / 开发调试）。
// -dev 开启开发模式：从 web/dist 文件系统托管前端，监听 web/src 变化自动触发 npm run build。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"xgames/internal/games/bot/replay"
	"xgames/internal/games/chess"
	"xgames/internal/games/ddz"
	"xgames/internal/games/gomoku"
	"xgames/internal/games/mahjong"
	"xgames/internal/infra/config"
	"xgames/internal/infra/store"
	"xgames/internal/platform/server"
	"xgames/web"
)

var Version = "dev"

func main() {
	configPath := flag.String("config", "data/config.yaml", "配置文件路径")
	showVersion := flag.Bool("version", false, "打印版本号并退出")
	noWindow := flag.Bool("no-window", false, "不创建桌面窗口（无头服务模式）")
	noOpen := flag.Bool("no-open", false, "不自动打开浏览器（仅 -no-window 下有效）")
	dev := flag.Bool("dev", false, "开发模式：Go 单进程 + 文件监听自动构建前端（implies -no-window）")
	flag.Parse()

	if *showVersion {
		fmt.Println(Version)
		return
	}

	if *dev {
		*noWindow = true
	}

	appDir := resolveAppDir(*configPath)
	if err := os.Chdir(appDir); err != nil {
		fmt.Fprintf(os.Stderr, "警告: 无法切换到应用目录 %s: %v\n", appDir, err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("XGames", "version", Version, "no_window", *noWindow, "dev", *dev, "app_dir", appDir)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("配置加载失败", "err", err)
		os.Exit(1)
	}

	dbPath := cfg.SQLite.Path
	dataDir := resolveDataDir(appDir)
	if dataDir != "" && !filepath.IsAbs(dbPath) {
		// 打包运行时把数据库等用户数据放到用户持久化目录，避免每次升级/重拷 .app 被清空
		dbPath = filepath.Join(dataDir, dbPath)
	}

	// 复盘报告目录：与数据库同源，打包运行放用户持久化目录，开发模式放项目 data/ 下；
	// 写入（SaveUserReport）与读取（/api/replays）共用同一目录
	replayDir := replayDirFor(appDir, dataDir)
	replay.SetBaseDir(replayDir)

	st, err := store.Open(dbPath)
	if err != nil {
		logger.Error("数据库打开失败", "path", dbPath, "err", err)
		os.Exit(1)
	}
	defer st.Close()

	// 每次 App 启动重置积分，使各游戏头像下的累计积分与排行榜从本会话重新计起
	resetCtx, cancelReset := context.WithTimeout(context.Background(), 5*time.Second)
	if err := st.ResetScores(resetCtx); err != nil {
		logger.Error("重置积分失败", "err", err)
	}
	cancelReset()

	// 游戏插件静态注册（平台零游戏依赖，由入口装配）
	ddz.Register(logger, cfg.Bot.Enabled)
	gomoku.Register(logger, cfg.Bot.Enabled)
	chess.Register(logger, cfg.Bot.Enabled)
	mahjong.Register(logger, cfg.Bot.Enabled)

	app := server.NewApp(cfg, st, logger).WithReplayDir(replayDir)

	var devWatcher *server.DevWatcher
	if *dev {
		webDir := filepath.Join(appDir, "web")
		if err := server.EnsureFrontendBuilt(webDir, logger); err != nil {
			logger.Error("前端构建失败", "err", err)
			os.Exit(1)
		}
		app.WithDev(webDir)
		devWatcher, err = server.NewDevWatcher(webDir, logger)
		if err != nil {
			logger.Error("文件监听器创建失败", "err", err)
			os.Exit(1)
		}
		if err := devWatcher.Start(); err != nil {
			logger.Error("文件监听器启动失败", "err", err)
			os.Exit(1)
		}
	}

	restoreCtx, cancelRestore := context.WithTimeout(context.Background(), 10*time.Second)
	app.Restore(restoreCtx)
	cancelRestore()

	ln, err := findListener(cfg.Server.Host, cfg.Server.Port)
	if err != nil {
		logger.Error("无法分配监听端口", "err", err)
		os.Exit(1)
	}
	// 先记录实际地址再构建路由：/app-config.json 依赖它下发真实 WS 地址
	app.SetListenAddr(ln.Addr().String())
	localAddr := normalizeLocalAddr(ln.Addr().String())

	httpSrv := &http.Server{
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errC := make(chan error, 1)
	go func() {
		logger.Info("HTTP 服务启动", "addr", ln.Addr().String())
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errC <- err
		}
	}()

	if *noWindow {
		if !*noOpen {
			openBrowser("http://" + localAddr)
		}
		runHeadless(httpSrv, app, devWatcher, errC, logger)
		return
	}

	runWails(httpSrv, app, localAddr, logger)
}

func runHeadless(httpSrv *http.Server, app *server.App, devWatcher *server.DevWatcher, errC chan error, logger *slog.Logger) {
	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errC:
		logger.Error("HTTP 服务异常退出", "err", err)
	case sig := <-sigC:
		logger.Info("收到退出信号", "signal", sig.String())
	}

	shutdown(httpSrv, app, devWatcher, logger)
}

func runWails(httpSrv *http.Server, app *server.App, addr string, logger *slog.Logger) {
	wailsApp := NewXGamesApp("http://" + addr)

	distFS, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		logger.Error("前端资源加载失败", "err", err)
		shutdown(httpSrv, app, nil, logger)
		os.Exit(1)
	}

	err = wails.Run(&options.App{
		Title:     "XGames",
		Width:     1400,
		Height:    900,
		MinWidth:  1100,
		MinHeight: 700,
		AssetServer: &assetserver.Options{
			Assets:  distFS,
			Handler: app.Handler(), // 统一：Wails 内部也走同一路由（/ws, /healthz 等）
		},
		BackgroundColour: &options.RGBA{R: 26, G: 26, B: 46, A: 1},
		OnStartup:        wailsApp.OnStartup,
		OnShutdown:       wailsApp.OnShutdown,
		Bind:             []interface{}{wailsApp},
	})

	if err != nil {
		logger.Error("Wails 启动失败", "err", err)
		shutdown(httpSrv, app, nil, logger)
		os.Exit(1)
	}
}

func shutdown(httpSrv *http.Server, app *server.App, devWatcher *server.DevWatcher, logger *slog.Logger) {
	if devWatcher != nil {
		logger.Info("停止前端文件监听...")
		devWatcher.Stop()
	}

	app.ShutdownGracefully()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		logger.Warn("HTTP 关闭超时", "err", err)
	}
	logger.Info("服务已停止")
}

// findListener 从 cfgPort 起探测可用端口并直接持有监听器，
// 避免"探测后释放再重绑"间隙端口被抢占
func findListener(host string, cfgPort int) (net.Listener, error) {
	for port := cfgPort; port < cfgPort+100; port++ {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)))
		if err == nil {
			return ln, nil
		}
	}
	return nil, fmt.Errorf("端口 %d-%d 全部被占用", cfgPort, cfgPort+99)
}

// normalizeLocalAddr 通配监听地址转为本机客户端可用的 localhost 地址
func normalizeLocalAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch stdruntime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

type XGamesApp struct {
	ctx context.Context
	url string
}

func NewXGamesApp(url string) *XGamesApp {
	return &XGamesApp{url: url}
}

func (a *XGamesApp) OnStartup(ctx context.Context) {
	a.ctx = ctx
}

func (a *XGamesApp) OnShutdown(ctx context.Context) {
}

// OpenInBrowser 在系统默认浏览器中打开当前地址
func (a *XGamesApp) OpenInBrowser() {
	if a.ctx != nil {
		wruntime.BrowserOpenURL(a.ctx, a.url)
	} else {
		openBrowser(a.url)
	}
}

// resolveAppDir 确定应用工作目录。
// 优先级：
//  1. 若 -config 为绝对路径 → 使用其所在目录
//  2. 若可执行文件位于 .app 包内 → Contents/Resources/（data/config.yaml 会被脚本拷贝至此）
//  3. 若当前目录存在 data/config.yaml → 当前目录（开发模式）
//  4. 可执行文件所在目录
func resolveAppDir(configPath string) string {
	if filepath.IsAbs(configPath) {
		return filepath.Dir(configPath)
	}

	exe, err := os.Executable()
	var exeDir string
	if err == nil {
		exeDir = filepath.Dir(exe)
		// macOS .app 包结构: XGames.app/Contents/MacOS/XGames
		if filepath.Base(exeDir) == "MacOS" {
			resources := filepath.Join(filepath.Dir(exeDir), "Resources")
			if _, err := os.Stat(filepath.Join(resources, "data", "config.yaml")); err == nil {
				return resources
			}
		}
	}

	// 开发模式：CWD 有 data/config.yaml
	if cwd, err := os.Getwd(); err == nil {
		if _, err := os.Stat(filepath.Join(cwd, configPath)); err == nil {
			return cwd
		}
	}

	if exeDir != "" {
		return exeDir
	}
	return "."
}

// replayDirFor 解析复盘报告根目录：
//   - 打包运行（位于 .app/Contents/Resources 内）→ 用户持久化目录 data/replays
//   - 开发模式 → 应用目录（项目根）data/replays
func replayDirFor(appDir, dataDir string) string {
	if dataDir != "" {
		return filepath.Join(dataDir, "data", "replays")
	}
	return filepath.Join(appDir, "data", "replays")
}

// resolveDataDir 返回用户持久化数据目录（macOS: ~/Library/Application Support/XGames），
// 仅当打包运行（位于 .app/Contents/Resources 内）时生效。
// 数据库等用户数据必须放在此处而不是 .app 包内部，否则每次升级/重新拷贝 .app 都会
// 清空用户数据（玩家昵称、战绩等随之丢失并重新随机生成）。
// 开发模式返回空串，配置中的相对路径按 appDir（项目根目录）解析。
func resolveDataDir(appDir string) string {
	if !strings.Contains(appDir, string(filepath.Separator)+"Contents"+string(filepath.Separator)+"Resources") {
		return ""
	}
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		return ""
	}
	dir := filepath.Join(base, "XGames")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	return dir
}
