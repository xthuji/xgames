// Package games 游戏平台的游戏插件抽象与注册表。
//
// 薄平台 + 异构游戏插件模型：平台只提供连接、房间、匹配、用户、存储、
// 协议信封等基础设施，不提供统一回合制框架；每个游戏自持对局会话
// （Session，Actor 模型）并全权处理自己的消息。
//
// 消息路由规则：平台 dispatch 只识别平台消息；未命中的一律转
// room.RouteGameMessage → 房间当前 Session.OnMessage（无对局则回错误）。
// 游戏消息类型无需命名空间前缀，不同游戏的类型集天然不冲突。
package games

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"xgames/internal/platform/protocol"
)

// Player 房间传给对局会话的玩家描述
type Player struct {
	ID    string
	Name  string
	Seat  int
	IsBot bool
}

// Timeouts 对局超时参数（平台从配置提供缺省值，各游戏自行取用）
type Timeouts struct {
	Turn        time.Duration // 出牌回合
	Bid         time.Duration // 叫分/加倍
	OfflineWait time.Duration // 离线暂停等待
}

// Broadcaster 输出端口：对局会话对房间 / WS 层的唯一出口
type Broadcaster interface {
	Broadcast(msg protocol.Message)               // 广播给房间全部玩家
	SendTo(playerID string, msg protocol.Message) // 定向发送
}

// PlayerResult 单玩家本局结果
type PlayerResult struct {
	PlayerID   string
	PlayerName string
	IsLandlord bool // 斗地主专属，其他游戏填 false
	IsBot      bool
	Score      int
}

// GameResult 一局结算结果（斗地主专属字段保留，新游戏填零值）
type GameResult struct {
	RoomCode    string
	LandlordID  string // 斗地主专属，其他游戏为空
	LandlordWin bool   // 斗地主专属
	Multiplier  int    // 斗地主专属，其他游戏为 0
	Players     []PlayerResult
}

// ResultSink 结算输出端口（平台房间实现：落库并复位房间）
type ResultSink interface {
	OnGameOver(result GameResult)
}

// SessionEnv 房间提供给游戏会话的能力端口
type SessionEnv struct {
	RoomCode    string
	Players     []Player
	Broadcaster Broadcaster
	ResultSink  ResultSink
	Timeouts    Timeouts
	Options     map[string]string // 房间选项（如斗地主机器人难度 difficulty）
	Scores      []int             // 玩家平台积分（与 Players 索引对齐；查询不可用时为 nil）
}

// Session 对局会话：各游戏自管生命周期与消息（Actor 模型）
type Session interface {
	// Run 启动会话事件循环（每局一个 goroutine）
	Run(ctx context.Context)
	// Stop 停止会话（优雅关闭 / 房间销毁）
	Stop()
	// OnMessage 游戏私有消息入口；未知类型返回携带错误码的 CodedError
	OnMessage(playerID string, msg protocol.Message) error
	// PlayerOffline / PlayerOnline 玩家掉线 / 重连通知
	PlayerOffline(playerID string)
	PlayerOnline(playerID string)
	// Snapshot 进行中对局全量快照（JSON 字节；不支持快照返回 nil）
	Snapshot() []byte
	// GameStateJSON 玩家视角的完整对局状态（重连恢复用；不可用返回 nil）
	GameStateJSON(playerID string) []byte
	// StateMessage request_game_state 的响应消息；无对局返回 false
	StateMessage(playerID string) (protocol.Message, bool)
}

// BotResponder 机器人动作回调端口：以协议消息形式提交给对局会话
// （会话结构上满足；避免机器人与具体会话实现互相依赖）
type BotResponder interface {
	SubmitBotAction(playerID string, msg protocol.Message) error
}

// BotController 游戏机器人控制器：房间把对局消息转发给它，
// 由它驱动机器人在合适时机经 BotResponder 提交动作。
type BotController interface {
	OnBroadcast(roomCode string, msg protocol.Message, resp BotResponder)
	OnTargeted(roomCode, playerID string, msg protocol.Message, resp BotResponder)
}

// Game 游戏插件描述符（进程启动时静态注册）
type Game interface {
	ID() string   // 游戏标识，如 "ddz"
	Name() string // 展示名
	// MinPlayers / MaxPlayers 房间席位约束（满员即全员就绪可开局）
	MinPlayers() int
	MaxPlayers() int
	// SupportsBots 是否支持机器人（人机练习 / 补位 / 手动添加）
	SupportsBots() bool
	// NewBotController 机器人控制器；返回 nil 表示机器人由会话内部驱动
	NewBotController() BotController
	// NewSession 创建并返回未启动的对局会话
	NewSession(env SessionEnv) Session
	// RestoreSession 从快照恢复对局会话（未启动）
	RestoreSession(env SessionEnv, snapshot []byte) (Session, error)
}

// --- 错误码 ---

// CodedError 携带协议错误码的错误（平台据此向客户端回错误消息）
type CodedError interface {
	error
	ErrorCode() int
}

type codedError struct {
	code int
	msg  string
}

func (e *codedError) Error() string  { return e.msg }
func (e *codedError) ErrorCode() int { return e.code }

// NewCodedError 构造携带协议错误码的错误
func NewCodedError(code int, msg string) error { return &codedError{code: code, msg: msg} }

// CodeOf 提取错误码（不携带时回退 ErrCodeUnknown）
func CodeOf(err error) int {
	var ce CodedError
	if errors.As(err, &ce) {
		return ce.ErrorCode()
	}
	return protocol.ErrCodeUnknown
}

// --- 注册表 ---

var (
	regMu    sync.RWMutex
	registry = map[string]Game{}
)

// Register 注册游戏；重复注册 panic（属程序错误，应在启动期暴露）
func Register(g Game) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, exists := registry[g.ID()]; exists {
		panic(fmt.Sprintf("games: 游戏重复注册 %q", g.ID()))
	}
	registry[g.ID()] = g
}

// Get 按 ID 查询游戏描述符
func Get(id string) (Game, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	g, ok := registry[id]
	return g, ok
}

// List 全部已注册游戏（按 ID 排序，展示顺序稳定）
func List() []Game {
	regMu.RLock()
	defer regMu.RUnlock()
	list := make([]Game, 0, len(registry))
	for _, g := range registry {
		list = append(list, g)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID() < list[j].ID() })
	return list
}

// RegisterErrorMessages 登记游戏私有错误码的默认文案（并入平台错误文案表）
func RegisterErrorMessages(msgs map[int]string) {
	for code, text := range msgs {
		protocol.ErrorMessages[code] = text
	}
}
