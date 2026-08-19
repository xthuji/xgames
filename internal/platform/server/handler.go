package server

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"xgames/internal/games"
	"xgames/internal/infra/ws"
	"xgames/internal/platform/protocol"
	"xgames/internal/platform/room"
)

// dispatch 消息分发入口（ws.Conn 读 goroutine 内调用）
func (h *Hub) dispatch(c *ws.Conn, msg protocol.Message) {
	switch msg.Type {
	// 连接
	case protocol.MsgPing:
		h.handlePing(c, msg)
	case protocol.MsgReconnect:
		h.handleReconnect(c, msg)

	// 房间
	case protocol.MsgCreateRoom:
		h.handleCreateRoom(c, msg)
	case protocol.MsgJoinRoom:
		h.handleJoinRoom(c, msg)
	case protocol.MsgLeaveRoom:
		h.handleLeaveRoom(c)
	case protocol.MsgCloseRoom:
		h.handleCloseRoom(c)
	case protocol.MsgQuickMatch:
		h.handleQuickMatch(c, msg)
	case protocol.MsgCancelMatch:
		h.handleCancelMatch(c)
	case protocol.MsgPracticeMatch:
		h.handlePracticeMatch(c, msg)
	case protocol.MsgAddBot:
		h.handleAddBot(c, msg)
	case protocol.MsgRecreateRoom:
		h.handleRecreateRoom(c, msg)
	case protocol.MsgReady:
		h.handleReady(c, true)
	case protocol.MsgCancelReady:
		h.handleReady(c, false)

	// 对局（平台级）
	case protocol.MsgLeaveGame:
		h.handleLeaveGame(c)

	// 查询与聊天
	case protocol.MsgGetStats:
		h.handleGetStats(c, msg)
	case protocol.MsgGetLeaderboard:
		h.handleGetLeaderboard(c, msg)
	case protocol.MsgGetRoomList:
		h.handleGetRoomList(c, msg)
	case protocol.MsgGetOnlineCount:
		h.handleGetOnlineCount(c)
	case protocol.MsgGetMaintenanceStatus:
		h.handleGetMaintenanceStatus(c)
	case protocol.MsgUpdateProfile:
		h.handleUpdateProfile(c, msg)
	case protocol.MsgUpdateReplaySetting:
		h.handleUpdateReplaySetting(c, msg)
	case protocol.MsgGameSync:
		h.handleGameSync(c)
	case protocol.MsgRequestGameState:
		h.handleRequestGameState(c)
	case protocol.MsgClearUserData:
		h.handleClearUserData(c)

	default:
		// 未命中的平台消息一律视为游戏私有消息，转给玩家所在房间的当前对局会话
		h.routeGameMessage(c, msg)
	}
}

// routeGameMessage 游戏私有消息路由：转给玩家所在房间的当前对局会话。
// 无房间/无对局/会话返回错误时，按携带的协议错误码回给客户端。
func (h *Hub) routeGameMessage(c *ws.Conn, msg protocol.Message) {
	r := h.rooms.RoomByPlayer(c.PlayerID())
	if r == nil {
		c.SendError(protocol.ErrCodeNotInRoom, "")
		return
	}
	if err := r.RouteGameMessage(c.PlayerID(), msg); err != nil {
		c.SendError(games.CodeOf(err), err.Error())
	}
}

// defaultGameID 缺省 game_id 兼容（旧客户端不携带时按斗地主处理）
func defaultGameID(id string) string {
	if id == "" {
		return "ddz"
	}
	return id
}

// validBotDifficulty 是否为合法的机器人难度档位（easy/normal/hard，随房间选项下发，
// 由各对战游戏的机器人控制器自行消费）
func validBotDifficulty(d string) bool {
	return d == "easy" || d == "normal" || d == "hard"
}

// --- 连接 ---

func (h *Hub) handlePing(c *ws.Conn, msg protocol.Message) {
	payload, err := protocol.ParsePayload[protocol.PingPayload](msg)
	if err != nil {
		return
	}
	c.Send(protocol.NewMessage(protocol.MsgPong, protocol.PongPayload{
		ClientTimestamp: payload.Timestamp,
		ServerTimestamp: time.Now().UnixMilli(),
	}))
}

// handleReconnect 断线重连：校验令牌 → 替换身份 → 恢复房间引用 → 轮换令牌
func (h *Hub) handleReconnect(c *ws.Conn, msg protocol.Message) {
	payload, err := protocol.ParsePayload[protocol.ReconnectPayload](msg)
	if err != nil {
		c.SendError(protocol.ErrCodeInvalidMsg, "")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	ok, err := h.store.ValidateReconnectToken(ctx, payload.PlayerID, payload.Token)
	cancel()
	if err != nil || !ok {
		c.SendError(protocol.ErrCodeUnknown, "重连令牌无效或已过期")
		return
	}

	h.mu.Lock()
	state := h.players[payload.PlayerID]
	if state == nil {
		h.mu.Unlock()
		c.SendError(protocol.ErrCodeUnknown, "会话不存在，请重新连接")
		return
	}
	// 丢弃握手时创建的临时身份，接管旧身份
	if tempID := c.PlayerID(); tempID != payload.PlayerID {
		delete(h.conns, tempID)
		delete(h.players, tempID)
	}
	c.BindPlayer(payload.PlayerID, state.Name)
	if old := h.conns[payload.PlayerID]; old != nil && old != c {
		old.Close() // 顶掉残留旧连接
	}
	h.conns[payload.PlayerID] = c
	h.mu.Unlock()

	// 令牌轮换
	newToken := h.issueToken(payload.PlayerID)

	resp := protocol.ReconnectedPayload{
		PlayerID:       payload.PlayerID,
		PlayerName:     state.Name,
		ReconnectToken: newToken,
	}

	// 积分/排名按玩家所在房间的游戏查询（无房间按缺省游戏）
	h.rooms.CleanupPlayerStaleRooms(payload.PlayerID)
	gameID := ""
	if r := h.rooms.RoomByPlayer(payload.PlayerID); r != nil {
		gameID = r.GameID()
	}
	score, rank := h.lookupScoreRank(gameID, payload.PlayerID)
	resp.Score = score
	resp.Rank = rank

	// 恢复房间引用与对局状态（GameState 为游戏私有状态 JSON）
	if r := h.rooms.RoomByPlayer(payload.PlayerID); r != nil {
		r.NotifyOnline(payload.PlayerID)
		resp.RoomCode = r.Code()
		if g := r.Game(); g != nil {
			resp.GameState = g.GameStateJSON(payload.PlayerID)
		}
	}

	c.Send(protocol.NewMessage(protocol.MsgReconnected, resp))
	h.logger.Info("玩家重连成功", "player", state.Name, "room", resp.RoomCode)
}

// --- 房间 ---

func (h *Hub) handleCreateRoom(c *ws.Conn, msg protocol.Message) {
	var p protocol.CreateRoomPayload
	if msg.Payload != nil {
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			c.SendError(protocol.ErrCodeInvalidMsg, "")
			return
		}
	}
	gameID := defaultGameID(p.GameID)
	if _, ok := games.Get(gameID); !ok {
		c.SendError(protocol.ErrCodeInvalidMsg, "未知游戏: "+gameID)
		return
	}
	r, err := h.rooms.CreateRoom(gameID, c.PlayerID(), c.PlayerName(), false)
	if err != nil {
		h.sendRoomExistsError(c, err)
		return
	}
	c.Send(protocol.NewMessage(protocol.MsgRoomCreated, protocol.RoomCreatedPayload{
		RoomCode: r.Code(),
		GameID:   gameID,
		Player:   r.SeatInfo(c.PlayerID()),
	}))
}

// sendRoomExistsError 单房间约束冲突时下发带已有房间信息的错误（含 creator_is_self），
// 前端据此弹窗允许用户关闭自己创建的旧房间；其他错误按原逻辑映射错误码。
func (h *Hub) sendRoomExistsError(c *ws.Conn, err error) {
	if errors.Is(err, room.ErrRoomExists) {
		if existing := h.rooms.ExistingRoom(); existing != nil {
			data, _ := json.Marshal(protocol.RoomExistsData{
				RoomCode:      existing.Code(),
				GameID:        existing.GameID(),
				CreatorIsSelf: existing.CreatorID() == c.PlayerID(),
			})
			c.Send(protocol.NewMessage(protocol.MsgError, protocol.ErrorPayload{
				Code:    protocol.ErrCodeRoomExists,
				Message: err.Error(),
				Data:    data,
			}))
			return
		}
	}
	c.SendError(mapRoomError(err), err.Error())
}

func (h *Hub) handleJoinRoom(c *ws.Conn, msg protocol.Message) {
	payload, err := protocol.ParsePayload[protocol.JoinRoomPayload](msg)
	if err != nil {
		c.SendError(protocol.ErrCodeInvalidMsg, "")
		return
	}

	r, err := h.rooms.JoinRoom(c.PlayerID(), c.PlayerName(), payload.RoomCode)
	if err != nil {
		c.SendError(mapRoomError(err), err.Error())
		return
	}
	c.Send(protocol.NewMessage(protocol.MsgRoomJoined, protocol.RoomJoinedPayload{
		RoomCode:  r.Code(),
		GameID:    r.GameID(),
		CreatorID: r.CreatorID(),
		Player:    r.SeatInfo(c.PlayerID()),
		Players:   r.AllSeatsInfo(),
	}))
}

func (h *Hub) handleLeaveRoom(c *ws.Conn) {
	h.rooms.LeaveRoom(c.PlayerID())
}

func (h *Hub) handleCloseRoom(c *ws.Conn) {
	if err := h.rooms.CloseRoom(c.PlayerID()); err != nil {
		c.SendError(mapRoomError(err), err.Error())
	}
}

func (h *Hub) handleQuickMatch(c *ws.Conn, msg protocol.Message) {
	var p protocol.QuickMatchPayload
	if msg.Payload != nil {
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			c.SendError(protocol.ErrCodeInvalidMsg, "")
			return
		}
	}
	gameID := defaultGameID(p.GameID)
	if _, ok := games.Get(gameID); !ok {
		c.SendError(protocol.ErrCodeInvalidMsg, "未知游戏: "+gameID)
		return
	}
	h.matcher.AddToQueue(gameID, c.PlayerID(), c.PlayerName())
}

func (h *Hub) handleCancelMatch(c *ws.Conn) {
	h.matcher.RemoveFromQueue(c.PlayerID())
}

// handlePracticeMatch 人机练习：建房 + 机器人填满席位（技术设计 6.2）。
// 流程与手动建房一致：机器人已自动准备，玩家准备后触发开局，不在此处自动开局。
func (h *Hub) handlePracticeMatch(c *ws.Conn, msg protocol.Message) {
	var p protocol.PracticeMatchPayload
	if msg.Payload != nil {
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			c.SendError(protocol.ErrCodeInvalidMsg, "")
			return
		}
	}
	gameID := defaultGameID(p.GameID)
	options := map[string]string{}
	if validBotDifficulty(p.Difficulty) {
		options["difficulty"] = p.Difficulty // 机器人难度（easy/normal/hard）
	}
	r, err := h.rooms.PracticeRoom(gameID, c.PlayerID(), c.PlayerName(), options)
	if err != nil {
		h.sendRoomExistsError(c, err)
		return
	}
	c.Send(protocol.NewMessage(protocol.MsgRoomJoined, protocol.RoomJoinedPayload{
		RoomCode:  r.Code(),
		GameID:    gameID,
		CreatorID: r.CreatorID(),
		Player:    r.SeatInfo(c.PlayerID()),
		Players:   r.AllSeatsInfo(),
	}))
}

// handleRecreateRoom 关闭已有房间并新建（单请求完成，优化 7.2）：
// 仅房间创建人可关闭旧房；关闭流程彻底清理（停止对局/删除快照/关闭房间记录/验证）后
// 新建指定游戏房间，practice=true 时机器人填满剩余席位，响应与原建/人机练习一致。
func (h *Hub) handleRecreateRoom(c *ws.Conn, msg protocol.Message) {
	var p protocol.RecreateRoomPayload
	if msg.Payload != nil {
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			c.SendError(protocol.ErrCodeInvalidMsg, "")
			return
		}
	}
	gameID := defaultGameID(p.GameID)
	if _, ok := games.Get(gameID); !ok {
		c.SendError(protocol.ErrCodeInvalidMsg, "未知游戏: "+gameID)
		return
	}
	options := map[string]string{}
	if validBotDifficulty(p.Difficulty) {
		options["difficulty"] = p.Difficulty // 机器人难度（easy/normal/hard）
	}
	r, err := h.rooms.RecreateRoom(gameID, c.PlayerID(), c.PlayerName(), p.Practice, options)
	if err != nil {
		h.sendRoomExistsError(c, err)
		return
	}
	if p.Practice {
		c.Send(protocol.NewMessage(protocol.MsgRoomJoined, protocol.RoomJoinedPayload{
			RoomCode:  r.Code(),
			GameID:    gameID,
			CreatorID: r.CreatorID(),
			Player:    r.SeatInfo(c.PlayerID()),
			Players:   r.AllSeatsInfo(),
		}))
		return
	}
	c.Send(protocol.NewMessage(protocol.MsgRoomCreated, protocol.RoomCreatedPayload{
		RoomCode: r.Code(),
		GameID:   gameID,
		Player:   r.SeatInfo(c.PlayerID()),
	}))
}

// handleAddBot 手动添加机器人：向玩家所在房间的空席位添加机器人（入座即准备）。
// 满席位且全员就绪时自动开局。
func (h *Hub) handleAddBot(c *ws.Conn, msg protocol.Message) {
	options := map[string]string{}
	if msg.Payload != nil {
		var p protocol.AddBotPayload
		if err := json.Unmarshal(msg.Payload, &p); err == nil && validBotDifficulty(p.Difficulty) {
			options["difficulty"] = p.Difficulty // 机器人难度（easy/normal/hard）
		}
	}
	if err := h.rooms.AddBot(c.PlayerID(), options); err != nil {
		c.SendError(mapRoomError(err), err.Error())
	}
}

func (h *Hub) handleReady(c *ws.Conn, ready bool) {
	if err := h.rooms.SetReady(c.PlayerID(), ready); err != nil {
		c.SendError(mapRoomError(err), err.Error())
	}
}

// --- 对局 ---

// handleLeaveGame 客户端 UI 离开对战页面：强制终止对局，确保后台不与前端脱节
func (h *Hub) handleLeaveGame(c *ws.Conn) {
	h.cleanupPlayerState(c.PlayerID())
	h.logger.Info("玩家离开对局", "player", c.PlayerID())
}

// handleGameSync 对局同步检测：返回玩家当前在服务端的真实状态（active/waiting/none）。
// 客户端据此判断 UI 是否与后台同步，不一致时自动回退大厅。
func (h *Hub) handleGameSync(c *ws.Conn) {
	r := h.rooms.RoomByPlayer(c.PlayerID())
	if r == nil {
		c.Send(protocol.NewMessage(protocol.MsgGameSyncResult, protocol.GameSyncResultPayload{
			Status: "none",
		}))
		return
	}
	if r.Game() != nil {
		c.Send(protocol.NewMessage(protocol.MsgGameSyncResult, protocol.GameSyncResultPayload{
			Status:   "active",
			RoomCode: r.Code(),
		}))
		return
	}
	c.Send(protocol.NewMessage(protocol.MsgGameSyncResult, protocol.GameSyncResultPayload{
		Status:   "waiting",
		RoomCode: r.Code(),
	}))
}

// handleRequestGameState 客户端请求完整对局状态（场景恢复用）。
// 当游戏场景通过同步检测进入但缺少私有状态数据时，主动拉取服务端完整状态。
func (h *Hub) handleRequestGameState(c *ws.Conn) {
	r := h.rooms.RoomByPlayer(c.PlayerID())
	if r == nil {
		c.Send(protocol.NewMessage(protocol.MsgGameState, protocol.GameStatePayload{Available: false}))
		return
	}
	g := r.Game()
	if g == nil {
		c.Send(protocol.NewMessage(protocol.MsgGameState, protocol.GameStatePayload{Available: false}))
		return
	}
	// 游戏自行组装私有状态消息（不可用则回 available=false）
	if sm, ok := g.StateMessage(c.PlayerID()); ok {
		c.Send(sm)
		return
	}
	c.Send(protocol.NewMessage(protocol.MsgGameState, protocol.GameStatePayload{Available: false}))
}

// handleClearUserData 清理当前用户的房间和对战数据（用户主动请求或 UI 检测到不一致时调用）
func (h *Hub) handleClearUserData(c *ws.Conn) {
	playerID := c.PlayerID()
	h.cleanupPlayerState(playerID)

	// 通知客户端清理成功
	c.Send(protocol.NewMessage(protocol.MsgUserDataCleared, protocol.UserDataClearedPayload{
		PlayerID: playerID,
	}))
	h.logger.Info("用户数据清理完成", "player", playerID)
}

// cleanupPlayerState 统一清理玩家状态：终止对局 + 离开房间 + 移除匹配队列
// 供 handleLeaveGame、handleClearUserData 等复用，避免逻辑分散
func (h *Hub) cleanupPlayerState(playerID string) {
	// 1. 终止对局（如果在进行中）并离开房间
	if r := h.rooms.RoomByPlayer(playerID); r != nil {
		if r.State() == room.StatePlaying {
			r.ForceEnd()
		}
		h.rooms.LeaveRoom(playerID)
	}
	// 2. 从匹配队列移除（全部游戏队列）
	if h.matcher != nil {
		h.matcher.RemoveFromQueue(playerID)
	}
}

// --- 查询与聊天 ---

func (h *Hub) handleGetStats(c *ws.Conn, msg protocol.Message) {
	var p protocol.GetStatsPayload
	if msg.Payload != nil {
		_ = json.Unmarshal(msg.Payload, &p) // 解析失败按缺省游戏处理（旧客户端无 payload）
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	gameID := defaultGameID(p.GameID)
	playerID := c.PlayerID()
	st, err := h.store.GetPlayerStats(ctx, gameID, playerID)
	if err != nil {
		c.SendError(protocol.ErrCodeUnknown, err.Error())
		return
	}
	rank, _ := h.store.PlayerRank(ctx, gameID, playerID)

	var winRate float64
	if st.TotalGames > 0 {
		winRate = float64(st.Wins) / float64(st.TotalGames)
	}
	c.Send(protocol.NewMessage(protocol.MsgStatsResult, protocol.StatsResultPayload{
		PlayerID:      playerID,
		PlayerName:    c.PlayerName(),
		TotalGames:    st.TotalGames,
		Wins:          st.Wins,
		Losses:        st.Losses,
		WinRate:       winRate,
		LandlordGames: st.LandlordGames,
		LandlordWins:  st.LandlordWins,
		FarmerGames:   st.FarmerGames,
		FarmerWins:    st.FarmerWins,
		Score:         st.Score,
		Rank:          rank,
		CurrentStreak: st.CurrentStreak,
		MaxWinStreak:  st.MaxWinStreak,
	}))
}

func (h *Hub) handleGetLeaderboard(c *ws.Conn, msg protocol.Message) {
	payload, err := protocol.ParsePayload[protocol.GetLeaderboardPayload](msg)
	if err != nil {
		c.SendError(protocol.ErrCodeInvalidMsg, "")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	entries, err := h.store.Leaderboard(ctx, defaultGameID(payload.GameID), payload.Type, payload.Offset, payload.Limit)
	if err != nil {
		c.SendError(protocol.ErrCodeUnknown, err.Error())
		return
	}

	out := make([]protocol.LeaderboardEntry, len(entries))
	for i, e := range entries {
		var winRate float64
		if e.Games > 0 {
			winRate = float64(e.Wins) / float64(e.Games)
		}
		out[i] = protocol.LeaderboardEntry{
			Rank:       payload.Offset + i + 1,
			PlayerID:   e.PlayerID,
			PlayerName: e.Name,
			Score:      e.Score,
			Wins:       e.Wins,
			WinRate:    winRate,
		}
	}
	c.Send(protocol.NewMessage(protocol.MsgLeaderboardResult, protocol.LeaderboardResultPayload{
		Type:    payload.Type,
		Entries: out,
	}))
}

func (h *Hub) handleGetRoomList(c *ws.Conn, msg protocol.Message) {
	var p protocol.GetRoomListPayload
	if msg.Payload != nil {
		_ = json.Unmarshal(msg.Payload, &p) // 解析失败按缺省游戏处理
	}
	c.Send(protocol.NewMessage(protocol.MsgRoomListResult, protocol.RoomListResultPayload{
		// 大厅实时展示所有已创建房间：game_id 为空时不过滤（跨游戏），不再强制默认斗地主
		Rooms: h.rooms.RoomList(p.GameID),
	}))
}

func (h *Hub) handleGetOnlineCount(c *ws.Conn) {
	c.Send(protocol.NewMessage(protocol.MsgOnlineCount, protocol.OnlineCountPayload{
		Count: h.OnlineCount(),
	}))
}

func (h *Hub) handleGetMaintenanceStatus(c *ws.Conn) {
	c.Send(protocol.NewMessage(protocol.MsgMaintenancePull, protocol.MaintenanceStatusPayload{
		Maintenance: h.maintenance.Load(),
	}))
}

func (h *Hub) handleUpdateProfile(c *ws.Conn, msg protocol.Message) {
	payload, err := protocol.ParsePayload[protocol.UpdateProfilePayload](msg)
	if err != nil {
		c.SendError(protocol.ErrCodeInvalidMsg, "")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	newName, err := h.store.UpdatePlayerName(ctx, c.PlayerID(), payload.Name)
	cancel()
	if err != nil {
		c.SendError(protocol.ErrCodeInvalidMsg, err.Error())
		return
	}

	c.BindPlayer(c.PlayerID(), newName)

	h.mu.Lock()
	if ps := h.players[c.PlayerID()]; ps != nil {
		ps.Name = newName
	}
	h.mu.Unlock()

	c.Send(protocol.NewMessage(protocol.MsgProfileUpdated, protocol.ProfileUpdatedPayload{
		PlayerID:   c.PlayerID(),
		PlayerName: newName,
	}))

	if r := h.rooms.RoomByPlayer(c.PlayerID()); r != nil {
		r.BroadcastNameChange(c.PlayerID(), newName)
	}

	h.logger.Info("玩家修改昵称", "player", c.PlayerID(), "name", newName)
}

func (h *Hub) handleUpdateReplaySetting(c *ws.Conn, msg protocol.Message) {
	payload, err := protocol.ParsePayload[protocol.UpdateReplaySettingPayload](msg)
	if err != nil {
		c.SendError(protocol.ErrCodeInvalidMsg, "")
		return
	}
	
	// 保存玩家的复盘功能设置到session中
	h.mu.Lock()
	if ps := h.players[c.PlayerID()]; ps != nil {
		ps.ReplayEnabled = payload.ReplayEnabled
	}
	h.mu.Unlock()
	
	// 发送确认消息给客户端
	c.Send(protocol.NewMessage(protocol.MsgReplaySettingUpdated, protocol.ReplaySettingUpdatedPayload{
		PlayerID:      c.PlayerID(),
		ReplayEnabled: payload.ReplayEnabled,
	}))
	
	h.logger.Info("玩家更新复盘功能设置", "player", c.PlayerID(), "replay_enabled", payload.ReplayEnabled)
}

// --- 错误映射 ---

func mapRoomError(err error) int {
	switch {
	case errors.Is(err, room.ErrRoomNotFound):
		return protocol.ErrCodeRoomNotFound
	case errors.Is(err, room.ErrRoomFull):
		return protocol.ErrCodeRoomFull
	case errors.Is(err, room.ErrNotInRoom):
		return protocol.ErrCodeNotInRoom
	case errors.Is(err, room.ErrGameStarted):
		return protocol.ErrCodeGameStarted
	case errors.Is(err, room.ErrNotRoomCreator):
		return protocol.ErrCodeNotRoomCreator
	case errors.Is(err, room.ErrGameNotFound):
		return protocol.ErrCodeInvalidMsg
	case errors.Is(err, room.ErrRoomExists):
		return protocol.ErrCodeRoomExists
	default:
		return protocol.ErrCodeUnknown
	}
}
