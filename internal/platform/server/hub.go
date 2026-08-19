// Package server 接口层：WS Hub、消息分发、HTTP 路由（技术设计第 5 章）。
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"xgames/internal/infra/config"
	"xgames/internal/infra/store"
	"xgames/internal/infra/ws"
	"xgames/internal/platform/match"
	"xgames/internal/platform/protocol"
	"xgames/internal/platform/room"
)

const (
	reconnectTokenTTL = 24 * time.Hour // 重连令牌有效期
)

// generateStablePlayerID 基于 IP + 机器码生成稳定的玩家 ID（SHA256 前 16 字节 hex）
func generateStablePlayerID(ip, machineID string) string {
	key := ip + "|" + machineID
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:16])
}

// PlayerState 玩家身份注册表条目（掉线后保留，供重连恢复）
type PlayerState struct {
	ID            string
	Name          string
	ReplayEnabled bool // 复盘功能启用状态
}

// Hub WS 连接中枢：玩家注册表 + 消息下发端口（实现 room.Sender）
type Hub struct {
	cfg    *config.Config
	store  store.Store
	logger *slog.Logger

	rooms   *room.Manager  // 装配后注入
	matcher *match.Matcher // 装配后注入

	ipLimiter  *ws.IPRateLimiter
	msgLimiter *ws.MessageLimiter

	mu      sync.RWMutex
	conns   map[string]*ws.Conn     // playerID → 活跃连接
	players map[string]*PlayerState // playerID → 身份（含掉线玩家）

	maintenance atomic.Bool
}

// NewHub 创建 Hub（rooms/matcher 由 App 装配时注入）
func NewHub(cfg *config.Config, st store.Store, logger *slog.Logger) *Hub {
	return &Hub{
		cfg:    cfg,
		store:  st,
		logger: logger,
		ipLimiter: ws.NewIPRateLimiter(
			cfg.Security.RateLimit.MaxPerSecond,
			cfg.Security.RateLimit.MaxPerMinute,
			cfg.Security.RateLimit.BanDurationTime(),
		),
		msgLimiter: ws.NewMessageLimiter(cfg.Security.MessageLimit.MaxPerSecond),
		conns:      make(map[string]*ws.Conn),
		players:    make(map[string]*PlayerState),
	}
}

// Bind 装配房间管理器与匹配器（解决构造循环依赖）
func (h *Hub) Bind(rm *room.Manager, m *match.Matcher) {
	h.rooms = rm
	h.matcher = m
}

// --- room.Sender 实现（Actor / 房间 / 匹配器的输出端口） ---

// SendToPlayer 定向下发；离线玩家静默丢弃
func (h *Hub) SendToPlayer(playerID string, msg protocol.Message) {
	h.mu.RLock()
	c := h.conns[playerID]
	h.mu.RUnlock()
	if c != nil {
		c.Send(msg)
	}
}

// BroadcastAll 广播给全部在线连接（大厅聊天/维护通知）
func (h *Hub) BroadcastAll(msg protocol.Message) {
	h.mu.RLock()
	conns := make([]*ws.Conn, 0, len(h.conns))
	for _, c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.RUnlock()
	for _, c := range conns {
		c.Send(msg)
	}
}

// OnlineCount 当前在线连接数
func (h *Hub) OnlineCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}

// SetMaintenance 维护模式开关（M6 优雅关闭使用）
func (h *Hub) SetMaintenance(on bool) { h.maintenance.Store(on) }

// --- 连接接入 ---

// HandleWS WebSocket 握手入口
func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	if h.maintenance.Load() {
		http.Error(w, "server is under maintenance", http.StatusServiceUnavailable)
		return
	}

	ip := clientIP(r)
	if !h.ipLimiter.Allow(ip) {
		http.Error(w, "too many connections", http.StatusTooManyRequests)
		return
	}

	opts := &websocket.AcceptOptions{}
	if len(h.cfg.Security.AllowedOrigins) == 1 && h.cfg.Security.AllowedOrigins[0] == "*" {
		opts.InsecureSkipVerify = true // 本地 App：默认放行任意来源
	} else {
		opts.OriginPatterns = h.cfg.Security.AllowedOrigins
	}

	wsconn, err := websocket.Accept(w, r, opts)
	if err != nil {
		h.logger.Warn("WS 握手失败", "ip", ip, "err", err)
		return
	}

	// 从请求头或 URL 参数获取机器码（客户端通过 X-Machine-ID header 或 machine_id query 发送）
	machineID := r.Header.Get("X-Machine-ID")
	if machineID == "" {
		// 尝试从 URL 参数获取
		machineID = r.URL.Query().Get("machine_id")
	}
	if machineID == "" {
		// 无机器码时使用 IP 作为后备标识
		machineID = ip
	}

	// 基于 IP + 机器码生成稳定的玩家 ID（同一设备始终获得相同 ID）
	playerID := generateStablePlayerID(ip, machineID)

	// 查找或创建玩家身份
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	player, err := h.store.GetOrCreatePlayer(ctx, playerID, machineID)
	if err != nil {
		h.logger.Error("玩家身份获取失败", "err", err)
		wsconn.Close(websocket.StatusInternalError, "server error")
		return
	}

	name := player.Name
	token := h.issueToken(playerID)

	c := ws.NewConn(store.NewID(), ip, wsconn, h.msgLimiter, h.logger)
	c.BindPlayer(playerID, name)
	c.OnMessage = h.dispatch
	c.OnDisconnect = h.onDisconnect

	h.mu.Lock()
	h.conns[playerID] = c
	h.players[playerID] = &PlayerState{ID: playerID, Name: name}
	h.mu.Unlock()

	score, rank := h.lookupScoreRank("", playerID)
	c.Send(protocol.NewMessage(protocol.MsgConnected, protocol.ConnectedPayload{
		PlayerID:       playerID,
		PlayerName:     name,
		ReconnectToken: token,
		Score:          score,
		Rank:           rank,
	}))
	h.logger.Info("玩家连接", "player", name, "ip", ip, "score", score, "rank", rank)

	c.Run(r.Context()) // 阻塞直到连接关闭
}

// onDisconnect 连接断开：注销连接，保留身份；移除匹配队列；通知房间掉线
func (h *Hub) onDisconnect(c *ws.Conn) {
	playerID := c.PlayerID()

	h.mu.Lock()
	if h.conns[playerID] == c {
		delete(h.conns, playerID)
	}
	h.mu.Unlock()
	h.msgLimiter.Remove(c.ConnID)

	if h.matcher != nil {
		h.matcher.RemoveFromQueue(playerID)
	}
	if h.rooms != nil {
		h.rooms.NotifyOffline(playerID) // 锁外调用，避免与房间广播互锁
	}
	h.logger.Info("玩家断开", "player", playerID)
}

// issueToken 签发重连令牌（32 字节随机 hex，存 SQLite 带过期时间）
func (h *Hub) issueToken(playerID string) string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	token := hex.EncodeToString(buf)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.store.SaveReconnectToken(ctx, playerID, token, time.Now().Add(reconnectTokenTTL)); err != nil {
		h.logger.Error("重连令牌落库失败", "player", playerID, "err", err)
	}
	return token
}

// clientIP 提取客户端 IP（本地部署直连，取 RemoteAddr 即可）
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return strings.TrimSpace(host)
}

// lookupScoreRank 查询玩家指定游戏的积分和排名（失败时返回 0,0，不阻塞连接；gameID 空按缺省游戏）
func (h *Hub) lookupScoreRank(gameID, playerID string) (int, int) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	st, err := h.store.GetPlayerStats(ctx, gameID, playerID)
	if err != nil || st == nil {
		return 0, 0
	}
	rank, _ := h.store.PlayerRank(ctx, gameID, playerID)
	return st.Score, rank
}
