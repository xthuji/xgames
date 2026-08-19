package server

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os/exec"
	"path/filepath"
	stdruntime "runtime"
	"sync/atomic"
	"time"

	"xgames/internal/games/ddz/replay"
	"xgames/internal/infra/config"
	"xgames/internal/infra/store"
	"xgames/internal/platform/match"
	"xgames/internal/platform/protocol"
	"xgames/internal/platform/room"
	"xgames/web"
)

// App 应用装配体（技术设计第 5 章）：Hub ← Manager ← Matcher，
// 通过 hub.Bind 打破构造循环依赖。游戏插件经 games 注册表静态注入（见 main）。
type App struct {
	cfg    *config.Config
	store  store.Store
	logger *slog.Logger

	hub     *Hub
	rooms   *room.Manager
	matcher *match.Matcher

	ready atomic.Bool // 快照恢复完成后置位（readyz 用）

	listenAddr atomic.Value // string: 实际监听地址（供 /app-config.json 下发给前端）

	dev    bool   // 开发模式：从文件系统托管前端、自动监听变更构建
	webDir string // 前端项目根目录（dev 模式用）

	replayDir string // 复盘报告根目录（默认相对路径，main 根据运行环境注入）
}

// NewApp 装配全部组件（依赖游戏已通过 games.Register 注册）
func NewApp(cfg *config.Config, st store.Store, logger *slog.Logger) *App {
	a := &App{cfg: cfg, store: st, logger: logger, replayDir: "data/replays"}

	a.hub = NewHub(cfg, st, logger)
	a.rooms = room.NewManager(a.hub, st, cfg.Game, logger)

	// 匹配机器人补位：仅在启用机器人时开启；具体是否补位由各游戏 SupportsBots 决定
	fillTimeout := time.Duration(0)
	if cfg.Bot.Enabled && cfg.Bot.BotFillTimeout > 0 {
		fillTimeout = time.Duration(cfg.Bot.BotFillTimeout) * time.Second
	}
	a.matcher = match.NewMatcher(a.rooms, a.hub, logger, fillTimeout)
	a.hub.Bind(a.rooms, a.matcher)

	return a
}

// WithDev 开启开发模式：从文件系统托管前端、自动监听变更触发 npm run build。
func (a *App) WithDev(webDir string) *App {
	a.dev = true
	a.webDir = webDir
	return a
}

// WithReplayDir 设置复盘报告根目录（main 根据运行环境解析后注入）
func (a *App) WithReplayDir(dir string) *App {
	a.replayDir = dir
	return a
}

// SetListenAddr 记录实际监听地址（端口被占用时会自动上移，前端不能写死）
func (a *App) SetListenAddr(addr string) { a.listenAddr.Store(addr) }

// clientWSURL 把监听地址转为本机客户端可用的 WS 地址（通配地址转 localhost）
func (a *App) clientWSURL() string {
	addr, _ := a.listenAddr.Load().(string)
	if addr == "" {
		addr = "localhost:3030"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "ws://" + addr + "/ws"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return "ws://" + net.JoinHostPort(host, port) + "/ws"
}

// Restore 从 SQLite 快照恢复房间与对局（启动时调用）
func (a *App) Restore(ctx context.Context) {
	a.rooms.RestoreFromStore(ctx)
	// 启动清理：销毁创建者已离线的残留房间（机器人不会重连，创建者尚未连接）
	a.rooms.CleanupStaleRooms()
	a.ready.Store(true)
	a.logger.Info("快照恢复完成")
}

// Handler HTTP 路由：/ws + 健康探测 + pprof + metrics
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()

	// 注册复盘报告 API 路由（目录由 main 按运行环境解析，写入与读取共用同一目录）
	replayHandler := replay.NewReplayHandler(a.replayDir)
	replayHandler.RegisterRoutes(mux)

	mux.HandleFunc("/ws", a.hub.HandleWS)
	// 前端运行时配置：下发真实 WS 地址。
	// Wails 窗口内页面由 AssetServer 提供（无同源端口），且实际端口可能因占用而变化，
	// 前端不能写死 localhost:3030；Wails 内该请求同样经 AssetServer.Handler 路由到这里。
	mux.HandleFunc("/app-config.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ws_url":%q}`, a.clientWSURL())
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if !a.ready.Load() {
			http.Error(w, "restoring", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := a.store.Ping(ctx); err != nil {
			http.Error(w, "store unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})

	// pprof 仅绑定 localhost（技术设计 §10）
	pprofMux := http.NewServeMux()
	pprofMux.HandleFunc("/debug/pprof/", pprof.Index)
	pprofMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	pprofMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	pprofMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	pprofMux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.Handle("/debug/", localhostOnly(pprofMux))

	// /metrics 轻量指标（无需引入 Prometheus client 依赖）
	mux.HandleFunc("/metrics", a.handleMetrics)

	// /api/open-browser 在系统默认浏览器中打开指定 URL
	mux.HandleFunc("/api/open-browser", func(w http.ResponseWriter, r *http.Request) {
		url := r.URL.Query().Get("url")
		if url == "" {
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			url = scheme + "://" + r.Host
		}
		openBrowser(url)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"url":%q}`, url)
	})

	// 前端静态资源
	if a.dev {
		distDir := filepath.Join(a.webDir, "dist")
		mux.Handle("/", http.FileServer(http.Dir(distDir)))
		a.logger.Info("开发模式：从文件系统托管前端", "dir", distDir)
	} else {
		if dist, err := fs.Sub(web.Dist, "dist"); err == nil {
			mux.Handle("/", http.FileServer(http.FS(dist)))
		}
	}
	return mux
}

// openBrowser 跨平台打开默认浏览器：macOS(open) / Linux(xdg-open) / Windows(start)
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

// localhostOnly 仅允许 127.0.0.1 / ::1 / localhost 访问（pprof 安全防护）
func localhostOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if ip != "127.0.0.1" && ip != "::1" && ip != "localhost" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// handleMetrics 轻量 Prometheus 文本格式指标（技术设计 §10）
func (a *App) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "# HELP ddz_online_connections 当前在线连接数\n")
	fmt.Fprintf(w, "# TYPE ddz_online_connections gauge\n")
	fmt.Fprintf(w, "ddz_online_connections %d\n", a.hub.OnlineCount())
	fmt.Fprintf(w, "# HELP ddz_rooms_total 当前房间总数\n")
	fmt.Fprintf(w, "# TYPE ddz_rooms_total gauge\n")
	fmt.Fprintf(w, "ddz_rooms_total %d\n", a.rooms.TotalRooms())
	fmt.Fprintf(w, "# HELP ddz_active_games 进行中对局数\n")
	fmt.Fprintf(w, "# TYPE ddz_active_games gauge\n")
	fmt.Fprintf(w, "ddz_active_games %d\n", a.rooms.ActiveGamesCount())
}

// Shutdown 停止业务组件（房间停止对局、刷最后一轮快照）；
// store 由调用方关闭。
func (a *App) Shutdown() {
	a.hub.SetMaintenance(true)
	a.matcher.Stop()
	a.rooms.Shutdown()
	a.logger.Info("应用组件已停止")
}

// ShutdownGracefully 优雅关闭（技术设计 §10）：
// 1. 进入维护模式 → 拒新连接
// 2. 广播 maintenance_push 给所有在线连接
// 3. 轮询等待进行中对局结束（最长 shutdown_timeout，每 shutdown_check_interval 检测）
// 4. 刷最后一轮快照 → 停止所有组件
func (a *App) ShutdownGracefully() {
	a.hub.SetMaintenance(true)
	a.hub.BroadcastAll(protocol.NewMessage(protocol.MsgMaintenancePush, protocol.MaintenancePayload{Maintenance: true}))
	a.matcher.Stop()

	timeout := a.cfg.Game.ShutdownTimeoutDuration()
	interval := a.cfg.Game.ShutdownCheckIntervalDuration()
	deadline := time.Now().Add(timeout)

	for {
		active := a.rooms.ActiveGamesCount()
		if active == 0 {
			a.logger.Info("全部对局已结束")
			break
		}
		if time.Now().After(deadline) {
			a.logger.Warn("等待对局结束超时，强制关闭", "active_games", active, "timeout", timeout)
			break
		}
		a.logger.Info("等待对局结束", "active_games", active, "remaining", deadline.Sub(time.Now()).Round(time.Second))
		time.Sleep(interval)
	}

	a.rooms.Shutdown()
	a.logger.Info("应用组件已停止")
}
