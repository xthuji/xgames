// Package match 快速匹配队列（技术设计 6.2）：按游戏分队列，FIFO，
// 满员（游戏 MaxPlayers）即创建内部房间开局；
// 等待超过 bot_fill_timeout 时用机器人补位（需游戏支持机器人）。
package match

import (
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"xgames/internal/games"
	"xgames/internal/platform/protocol"
	"xgames/internal/platform/room"
)

// queuedPlayer 队列中的等待玩家
type queuedPlayer struct {
	ID   string
	Name string
	at   time.Time // 入队时间（机器人补位计时）
}

// Matcher 快速匹配器（按游戏分队列）
type Matcher struct {
	mu          sync.Mutex
	queues      map[string][]queuedPlayer // gameID → 等待队列
	rooms       *room.Manager
	sender      room.Sender
	logger      *slog.Logger
	fillTimeout time.Duration // >0 且游戏支持机器人时，超时补位
	stopC       chan struct{}
}

// NewMatcher 创建匹配器；fillTimeout<=0 表示不启用机器人补位
func NewMatcher(rm *room.Manager, sender room.Sender, logger *slog.Logger, fillTimeout time.Duration) *Matcher {
	m := &Matcher{
		queues:      make(map[string][]queuedPlayer),
		rooms:       rm,
		sender:      sender,
		logger:      logger,
		fillTimeout: fillTimeout,
		stopC:       make(chan struct{}),
	}
	if fillTimeout > 0 {
		go m.fillLoop()
	}
	return m
}

// Stop 停止补位协程
func (m *Matcher) Stop() {
	select {
	case <-m.stopC:
	default:
		close(m.stopC)
	}
}

// AddToQueue 加入指定游戏的匹配队列；满员立即组局。未知游戏直接忽略。
func (m *Matcher) AddToQueue(gameID, playerID, name string) {
	gdef, ok := games.Get(gameID)
	if !ok {
		m.logger.Warn("匹配请求的游戏未注册", "game", gameID, "player", name)
		return
	}
	need := gdef.MaxPlayers()

	m.mu.Lock()
	queue := m.queues[gameID]
	for _, p := range queue {
		if p.ID == playerID {
			m.mu.Unlock()
			return // 已在队列
		}
	}
	queue = append(queue, queuedPlayer{ID: playerID, Name: name, at: time.Now()})
	m.queues[gameID] = queue
	m.logger.Info("加入匹配队列", "game", gameID, "player", name, "queue", len(queue))

	if len(queue) < need {
		m.mu.Unlock()
		return
	}

	players := queue[:need]
	m.queues[gameID] = queue[need:]
	m.mu.Unlock()

	go m.createMatchRoom(gameID, gdef, players, 0)
}

// RemoveFromQueue 从全部游戏队列移除（掉线/取消）
func (m *Matcher) RemoveFromQueue(playerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for gameID, queue := range m.queues {
		for i, p := range queue {
			if p.ID == playerID {
				m.queues[gameID] = append(queue[:i], queue[i+1:]...)
				m.logger.Info("离开匹配队列", "game", gameID, "player", p.Name, "queue", len(m.queues[gameID]))
				return
			}
		}
	}
}

// fillLoop 每秒检查各游戏队列：队首等待超时且人数不足 → 机器人补位开局（仅支持机器人的游戏）
func (m *Matcher) fillLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopC:
			return
		case <-ticker.C:
			m.fillOnce()
		}
	}
}

// fillOnce 一轮补位检查（调用方不持锁）
func (m *Matcher) fillOnce() {
	m.mu.Lock()
	for gameID, queue := range m.queues {
		if len(queue) == 0 || time.Since(queue[0].at) < m.fillTimeout {
			continue
		}
		gdef, ok := games.Get(gameID)
		if !ok || len(queue) >= gdef.MaxPlayers() || !gdef.SupportsBots() {
			continue
		}
		players := queue
		delete(m.queues, gameID)
		m.mu.Unlock()

		m.logger.Info("匹配等待超时，机器人补位", "game", gameID, "humans", len(players))
		go m.createMatchRoom(gameID, gdef, players, gdef.MaxPlayers()-len(players))
		m.mu.Lock()
	}
	m.mu.Unlock()
}

// createMatchRoom 匹配成功：创建内部房间 → 真人入座 → 机器人补位 → 自动准备 → 开局
// 所有加座操作静默（broadcast=false），由 match_found 一次性告知完整玩家列表，
// 避免提前的 player_joined 破坏客户端状态。
func (m *Matcher) createMatchRoom(gameID string, gdef games.Game, players []queuedPlayer, botCount int) {
	r, err := m.rooms.CreateRoom(gameID, players[0].ID, players[0].Name, false, false)
	if err != nil {
		m.logger.Error("匹配创建房间失败", "game", gameID, "err", err)
		// 通知匹配中的玩家（单房间约束冲突时带已有房间信息，前端可弹窗关闭旧房间）
		for _, p := range players {
			m.sender.SendToPlayer(p.ID, matchRoomError(m.rooms, p.ID, err))
		}
		return
	}

	for _, p := range players[1:] {
		if _, err := m.rooms.JoinRoom(p.ID, p.Name, r.Code(), false); err != nil {
			m.logger.Error("匹配加入房间失败", "player", p.Name, "err", err)
		}
	}
	if botCount > 0 {
		if err := m.rooms.FillBots(r.Code(), botCount, false); err != nil {
			m.logger.Error("匹配机器人补位失败", "err", err)
			return
		}
	}

	m.logger.Info("匹配成功", "room", r.Code(), "game", gameID, "humans", len(players), "bots", botCount)

	// 给每位真人玩家发送匹配成功 + 房间信息（机器人无连接，SendToPlayer 静默丢弃）
	for _, p := range players {
		m.sender.SendToPlayer(p.ID, protocol.NewMessage(protocol.MsgMatchFound, protocol.RoomJoinedPayload{
			RoomCode:  r.Code(),
			GameID:    gameID,
			CreatorID: r.CreatorID(),
			Player:    r.SeatInfo(p.ID),
			Players:   r.AllSeatsInfo(),
		}))
	}

	r.SetAllReady()
	if err := r.StartGame(); err != nil {
		m.logger.Error("匹配开局失败", "room", r.Code(), "err", err)
	}
}

// matchRoomError 匹配建房失败的错误消息：单房间约束冲突时附带已有房间信息
// （含 creator_is_self，前端据此弹窗允许用户关闭自己创建的旧房间）。
func matchRoomError(rm *room.Manager, playerID string, err error) protocol.Message {
	if errors.Is(err, room.ErrRoomExists) {
		if existing := rm.ExistingRoom(); existing != nil {
			data, _ := json.Marshal(protocol.RoomExistsData{
				RoomCode:      existing.Code(),
				GameID:        existing.GameID(),
				CreatorIsSelf: existing.CreatorID() == playerID,
			})
			return protocol.NewMessage(protocol.MsgError, protocol.ErrorPayload{
				Code:    protocol.ErrCodeRoomExists,
				Message: err.Error(),
				Data:    data,
			})
		}
	}
	return protocol.NewMessage(protocol.MsgError, protocol.ErrorPayload{
		Code:    protocol.ErrCodeUnknown,
		Message: "匹配失败，请稍后再试",
	})
}
