package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"

	"xgames/internal/platform/protocol"
)

const (
	writeWait      = 10 * time.Second    // 写入超时
	pongWait       = 60 * time.Second    // 读超时（心跳丢失判定掉线）
	pingPeriod     = (pongWait * 9) / 10 // 服务端 WS ping 间隔
	maxMessageSize = 4096                // 单条消息上限
	sendQueueSize  = 256                 // 发送队列容量
)

// Conn 单个 WebSocket 连接：读 goroutine 解码分发，写 goroutine 串行发送
type Conn struct {
	// ConnID 连接级唯一 ID（每次握手新生成，与玩家 ID 不同）
	ConnID string
	IP     string

	playerID   string // 绑定的玩家身份（受 mu 保护，重连时变更）
	playerName string

	ws   *websocket.Conn
	send chan []byte

	// OnMessage 消息分发回调（读 goroutine 内调用）
	OnMessage func(c *Conn, msg protocol.Message)
	// OnDisconnect 连接断开回调（只触发一次）
	OnDisconnect func(c *Conn)

	msgLimiter *MessageLimiter
	logger     *slog.Logger

	mu     sync.Mutex
	closed bool
}

// NewConn 包装底层 websocket.Conn
func NewConn(connID, ip string, wsconn *websocket.Conn, limiter *MessageLimiter, logger *slog.Logger) *Conn {
	wsconn.SetReadLimit(maxMessageSize)
	return &Conn{
		ConnID:     connID,
		IP:         ip,
		ws:         wsconn,
		send:       make(chan []byte, sendQueueSize),
		msgLimiter: limiter,
		logger:     logger,
	}
}

// Run 启动读写 goroutine 并阻塞直到连接关闭
func (c *Conn) Run(ctx context.Context) {
	done := make(chan struct{})
	go c.writePump(ctx, done)
	c.readPump(ctx)
	// 标记连接已关闭，防止其他 goroutine 在 close(c.send) 后仍向 send 通道写入导致 panic
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	close(c.send)
	<-done
}

// readPump 读循环：解码 JSON 信封后分发；读超时/错误退出
func (c *Conn) readPump(ctx context.Context) {
	defer func() {
		if c.OnDisconnect != nil {
			c.OnDisconnect(c)
		}
		_ = c.ws.CloseNow()
	}()

	for {
		readCtx, cancel := context.WithTimeout(ctx, pongWait)
		typ, data, err := c.ws.Read(readCtx)
		cancel()
		if err != nil {
			if websocket.CloseStatus(err) == -1 && ctx.Err() == nil {
				c.logger.Debug("连接读取结束", "conn", c.ConnID, "err", err)
			}
			return
		}
		if typ != websocket.MessageText && typ != websocket.MessageBinary {
			continue
		}

		// per-conn 消息限速
		if !c.msgLimiter.AllowMessage(c.ConnID) {
			c.SendError(protocol.ErrCodeRateLimit, "消息发送过于频繁")
			if c.msgLimiter.WarningCount(c.ConnID) > 5 {
				c.logger.Warn("连接因多次超速被断开", "conn", c.ConnID, "ip", c.IP)
				return
			}
			continue
		}

		var msg protocol.Message
		if err := json.Unmarshal(data, &msg); err != nil || msg.Type == "" {
			c.SendError(protocol.ErrCodeInvalidMsg, "")
			continue
		}
		if c.OnMessage != nil {
			c.OnMessage(c, msg)
		}
	}
}

// writePump 写循环：消费发送队列；定期发送 WS ping 保活
func (c *Conn) writePump(ctx context.Context, done chan struct{}) {
	defer close(done)

	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case data, ok := <-c.send:
			if !ok {
				_ = c.ws.Close(websocket.StatusNormalClosure, "")
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, writeWait)
			err := c.ws.Write(writeCtx, websocket.MessageText, data)
			cancel()
			if err != nil {
				return
			}
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, writeWait)
			err := c.ws.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		case <-ctx.Done():
			_ = c.ws.Close(websocket.StatusGoingAway, "server shutdown")
			return
		}
	}
}

// Send 发送协议消息；队列满则断开（慢消费者保护）
func (c *Conn) Send(msg protocol.Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		c.logger.Error("消息编码失败", "type", msg.Type, "err", err)
		return
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	select {
	case c.send <- data:
	default:
		c.logger.Warn("发送队列已满，断开连接", "conn", c.ConnID)
		c.ws.Close(websocket.StatusPolicyViolation, "send queue full")
	}
}

// SendError 发送错误消息（错误码 + 可选自定义文案）
func (c *Conn) SendError(code int, message string) {
	if message == "" {
		message = protocol.ErrorMessages[code]
	}
	c.Send(protocol.NewMessage(protocol.MsgError, protocol.ErrorPayload{
		Code:    code,
		Message: message,
	}))
}

// Close 主动关闭连接（触发 OnDisconnect）
func (c *Conn) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	_ = c.ws.Close(websocket.StatusNormalClosure, "")
}

// BindPlayer 绑定玩家身份（握手/重连时由 Hub 调用）
func (c *Conn) BindPlayer(playerID, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.playerID = playerID
	c.playerName = name
}

// PlayerID 绑定的玩家 ID
func (c *Conn) PlayerID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.playerID
}

// PlayerName 绑定的玩家昵称
func (c *Conn) PlayerName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.playerName
}
