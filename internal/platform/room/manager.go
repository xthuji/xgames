package room

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"xgames/internal/games"
	"xgames/internal/infra/config"
	"xgames/internal/infra/store"
	"xgames/internal/platform/protocol"
)

const (
	roomCodeLength = 6            // 房间号长度
	roomCodeChars  = "0123456789" // 房间号字符集
)

// Manager 房间管理器：房间生命周期 + 清理协程 + 快照防抖持久化（技术设计 6.1）
type Manager struct {
	mu      sync.RWMutex
	rooms   map[string]*Room
	sender  Sender
	store   store.Store
	gameCfg config.GameConfig
	logger  *slog.Logger
	stopC   chan struct{}
}

// NewManager 创建房间管理器并启动清理/持久化协程（游戏经 games 注册表查找，无需注入）
func NewManager(sender Sender, st store.Store, gameCfg config.GameConfig, logger *slog.Logger) *Manager {
	m := &Manager{
		rooms:   make(map[string]*Room),
		sender:  sender,
		store:   st,
		gameCfg: gameCfg,
		logger:  logger,
		stopC:   make(chan struct{}),
	}
	go m.cleanupLoop()
	go m.persistLoop()
	return m
}

// --- 房间操作 ---

// CreateRoom 创建指定游戏的房间并让创建者入座。broadcast 控制是否广播 player_joined（PracticeRoom/匹配房静默加座时传 false）。
// 第一个非机器人玩家自动成为房间创建者（创建者不可被分配为机器人）。
// 约束：服务端同时仅允许存在一个房间（无论游戏类型），已有房间时返回 ErrRoomExists。
func (m *Manager) CreateRoom(gameID, playerID, name string, isBot bool, broadcast ...bool) (*Room, error) {
	gdef, ok := games.Get(gameID)
	if !ok {
		return nil, ErrGameNotFound
	}
	doBroadcast := true
	if len(broadcast) > 0 {
		doBroadcast = broadcast[0]
	}

	m.mu.Lock()
	// 单房间约束：已有房间时拒绝创建
	if len(m.rooms) > 0 {
		m.mu.Unlock()
		return nil, ErrRoomExists
	}
	code := m.generateRoomCode()
	r := newRoom(code, gdef, m.sender, m.store, GameConfigTimeouts(m.gameCfg), m.logger)
	if !isBot {
		r.SetCreatorID(playerID)
	}
	if _, err := r.addSeat(playerID, name, isBot, doBroadcast); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.rooms[code] = r
	m.mu.Unlock()

	m.logger.Info("房间创建", "room", code, "game", gameID, "player", name, "creator", playerID)
	return r, nil
}

// FillBots 用机器人填满房间空位（不设置准备状态，由调用方决定）。broadcast 控制是否广播 player_joined。
func (m *Manager) FillBots(code string, n int, broadcast ...bool) error {
	doBroadcast := true
	if len(broadcast) > 0 {
		doBroadcast = broadcast[0]
	}

	m.mu.RLock()
	r, exists := m.rooms[code]
	m.mu.RUnlock()
	if !exists {
		return ErrRoomNotFound
	}
	for i := 0; i < n; i++ {
		if _, err := r.addSeat(store.NewID(), genBotName(), true, doBroadcast); err != nil {
			return err
		}
	}
	return nil
}

// genBotName 机器人名 BOT-xxxx（技术设计 8.5）
func genBotName() string {
	return fmt.Sprintf("BOT-%04d", rand.IntN(10000))
}

// PracticeRoom 人机练习：建房 + 机器人填满剩余席位（技术设计 6.2）。
// 流程与手动建房一致：机器人入座即准备，真人需自行准备触发开局，
// 本方法不自动开局。所有玩家（真人+机器人）静默入座，由 room_joined
// 一次性告知客户端完整玩家列表，避免 addSeat 的 player_joined 广播
// 在 room_joined 之前到达导致前端状态混乱。options 为房间选项（如斗地主 difficulty）。
func (m *Manager) PracticeRoom(gameID, playerID, name string, options map[string]string) (*Room, error) {
	gdef, ok := games.Get(gameID)
	if !ok {
		return nil, ErrGameNotFound
	}
	if !gdef.SupportsBots() {
		return nil, ErrBotsDisabled
	}
	r, err := m.CreateRoom(gameID, playerID, name, false, false)
	if err != nil {
		return nil, err
	}
	if err := m.FillBots(r.code, gdef.MaxPlayers()-1, false); err != nil {
		m.destroyRoom(r.code)
		return nil, err
	}
	for k, v := range options {
		r.SetOption(k, v)
	}
	r.SetBotsReady()
	m.logger.Info("人机练习房间已创建（等待玩家准备开局）", "room", r.code, "game", gameID, "player", name)
	return r, nil
}

// RecreateRoom 关闭创建者的现有房间并新建指定游戏房间（单请求完成关闭+新建并返回最终结果）。
// 关闭流程彻底清理：广播通知 → 停止对局 Actor → 删除快照 → 关闭房间记录 → 验证内存房间已移除；
// 随后创建新房间（practice=true 时机器人填满剩余席位并自动准备）。无已有房间时直接新建。
func (m *Manager) RecreateRoom(gameID, playerID, name string, practice bool, options map[string]string) (*Room, error) {
	if target := m.creatorRoom(playerID); target != nil {
		code := target.Code()
		target.BroadcastError(protocol.ErrCodeUnknown, "房主已关闭房间")
		m.destroyRoom(code)
		// 验证已关闭（destroyRoom 同步移除内存房间）
		if m.GetRoom(code) != nil {
			m.logger.Error("房间关闭验证失败，取消重建", "room", code, "player", playerID)
			return nil, ErrRoomExists
		}
		m.logger.Info("房间已关闭（重建流程）", "room", code, "player", playerID)
	}
	if practice {
		return m.PracticeRoom(gameID, playerID, name, options)
	}
	return m.CreateRoom(gameID, playerID, name, false)
}

// creatorRoom 查找创建者为指定玩家的房间：
// 优先按玩家座位匹配（正常在线场景），回退按创建者 ID 匹配（玩家掉线/离开后房间仍在）
func (m *Manager) creatorRoom(playerID string) *Room {
	if r := m.RoomByPlayer(playerID); r != nil && r.CreatorID() == playerID {
		return r
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, r := range m.rooms {
		if r.CreatorID() == playerID {
			return r
		}
	}
	return nil
}

// AddBot 仅房间创建人可手动向所在房间添加一个机器人占席（机器人入座即准备并广播）。
// options 非空时更新房间选项（如斗地主机器人难度）。满席位且全员就绪时自动开局，
// 与手动准备流程一致。
func (m *Manager) AddBot(playerID string, options map[string]string) error {
	r := m.RoomByPlayer(playerID)
	if r == nil {
		return ErrNotInRoom
	}
	if r.State() != StateWaiting {
		return ErrGameStarted
	}
	if r.CreatorID() != playerID {
		return ErrNotRoomCreator
	}
	if g, ok := games.Get(r.GameID()); !ok || !g.SupportsBots() {
		return ErrBotsDisabled
	}
	for k, v := range options {
		r.SetOption(k, v)
	}

	botID := store.NewID()
	if _, err := r.addSeat(botID, genBotName(), true, true); err != nil {
		return err
	}
	// 机器人席位立即准备；全员就绪自动开局（与 SetReady 路径一致）
	allReady, err := r.setReady(botID, true)
	if err != nil {
		return err
	}
	if allReady {
		if err := r.StartGame(); err != nil {
			m.logger.Error("开局失败", "room", r.code, "err", err)
			return err
		}
	}
	m.logger.Info("房间手动添加机器人", "room", r.code, "player", playerID)
	return nil
}

// JoinRoom 加入房间（成功时已向房内其他人广播 player_joined）。broadcast 控制是否广播。
// 如果玩家已在目标房间中，直接返回房间（作为重新获取房间状态的幂等请求）。
// 如果玩家已在另一个房间中，先离开旧房间再加入新房间。
func (m *Manager) JoinRoom(playerID, name, code string, broadcast ...bool) (*Room, error) {
	doBroadcast := true
	if len(broadcast) > 0 {
		doBroadcast = broadcast[0]
	}

	m.mu.RLock()
	r, exists := m.rooms[code]
	m.mu.RUnlock()
	if !exists {
		return nil, ErrRoomNotFound
	}

	// 玩家已在目标房间中 → 直接返回（幂等）
	if r.HasPlayer(playerID) {
		m.logger.Info("玩家重新进入房间（已在场内）", "room", code, "player", name)
		return r, nil
	}

	// 玩家已在另一个房间中 → 先离开旧房间
	if old := m.RoomByPlayer(playerID); old != nil && old.Code() != code {
		m.LeaveRoom(playerID)
	}

	if _, err := r.addSeat(playerID, name, false, doBroadcast); err != nil {
		return nil, err
	}
	m.logger.Info("玩家加入房间", "room", code, "player", name)
	return r, nil
}

// LeaveRoom 玩家离开房间；房间清空则解散
func (m *Manager) LeaveRoom(playerID string) {
	r := m.RoomByPlayer(playerID)
	if r == nil {
		return
	}

	// 对局进行中不允许离开（客户端只能打完或掉线）
	if r.State() == StatePlaying {
		return
	}

	remaining := r.removeSeat(playerID)
	m.logger.Info("玩家离开房间", "room", r.code, "player", playerID)

	// 房间不再有真人（仅剩机器人）时直接解散：避免人机练习/建房离开后，
	// 残留的机器人占席房间在房间列表或重启恢复时被误认为"自动创建的已存在房间"。
	// 对局进行中不允许离开（上方已拦截），故此处只处理 Waiting 房间。
	if remaining == 0 || !r.HasHumanPlayer() {
		m.destroyRoom(r.code)
	}
}

// CloseRoom 房主关闭房间（解散房间并通知所有人）。
// 仅房间创建者可调用；对局进行中时先强制终止再解散。
// 优先按玩家座位匹配，找不到则回退到按创建者 ID 匹配（兼容玩家掉线后房间仍在的场景）。
func (m *Manager) CloseRoom(playerID string) error {
	var target *Room
	// 1. 优先按玩家座位匹配（正常在线场景）
	if r := m.RoomByPlayer(playerID); r != nil && r.CreatorID() == playerID {
		target = r
	} else {
		// 2. 回退：按创建者 ID 直接查找（玩家可能已掉线/离开但房间仍在）
		m.mu.RLock()
		for _, r := range m.rooms {
			if r.CreatorID() == playerID {
				target = r
				break
			}
		}
		m.mu.RUnlock()
	}
	if target == nil {
		return ErrNotInRoom
	}

	target.BroadcastError(protocol.ErrCodeUnknown, "房主已关闭房间")
	m.destroyRoom(target.Code())
	m.logger.Info("房间被房主关闭", "room", target.Code(), "player", playerID)
	return nil
}

// SetReady 准备/取消准备；全员就绪自动开局
func (m *Manager) SetReady(playerID string, ready bool) error {
	r := m.RoomByPlayer(playerID)
	if r == nil {
		return ErrNotInRoom
	}

	allReady, err := r.setReady(playerID, ready)
	if err != nil {
		return err
	}
	if allReady {
		if err := r.StartGame(); err != nil {
			m.logger.Error("开局失败", "room", r.code, "err", err)
		}
	}
	return nil
}

// GetRoom 按房号查房间
func (m *Manager) GetRoom(code string) *Room {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rooms[code]
}

// ExistingRoom 返回当前存在的唯一房间（服务端单房间约束）。
// 无房间时返回 nil。
func (m *Manager) ExistingRoom() *Room {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, r := range m.rooms {
		return r // 单房间约束，最多一个
	}
	return nil
}

// RoomByPlayer 按玩家 ID 查所在房间
func (m *Manager) RoomByPlayer(playerID string) *Room {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, r := range m.rooms {
		if r.HasPlayer(playerID) {
			return r
		}
	}
	return nil
}

// RoomList 房间列表（gameID 非空时按游戏过滤）。
// 大厅实时展示所有已创建房间（含对局进行中），单房间约束下最多一个。
func (m *Manager) RoomList(gameID string) []protocol.RoomListItem {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rooms := make([]protocol.RoomListItem, 0, len(m.rooms))
	for code, r := range m.rooms {
		if gameID != "" && r.GameID() != gameID {
			continue
		}
		gdef, ok := games.Get(r.GameID())
		if !ok {
			continue
		}
		creatorName := ""
		for _, s := range r.AllSeatsInfo() {
			if s.ID == r.CreatorID() {
				creatorName = s.Name
				break
			}
		}
		state := "waiting"
		if r.State() == StatePlaying {
			state = "playing"
		}
		rooms = append(rooms, protocol.RoomListItem{
			RoomCode:    code,
			GameID:      r.GameID(),
			PlayerCount: r.PlayerCount(),
			MaxPlayers:  gdef.MaxPlayers(),
			CreatorName: creatorName,
			State:       state,
		})
	}
	return rooms
}

// ActiveGamesCount 进行中的对局数（优雅关闭用）
func (m *Manager) ActiveGamesCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, r := range m.rooms {
		if r.State() == StatePlaying {
			count++
		}
	}
	return count
}

// TotalRooms 当前房间总数（metrics 用）
func (m *Manager) TotalRooms() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.rooms)
}

// --- 掉线 / 重连 ---

// NotifyOffline 玩家掉线：通知房间；全员离线则销毁房间
func (m *Manager) NotifyOffline(playerID string) {
	r := m.RoomByPlayer(playerID)
	if r == nil {
		return
	}

	allGone, game := r.NotifyOffline(playerID, m.gameCfg.OfflineWaitTimeout)
	m.logger.Info("玩家掉线", "room", r.code, "player", playerID, "all_gone", allGone)

	// 解散判定：
	//  1. 全员离线（原有逻辑）。
	//  2. 等待中的房间已无任何在线真人时立即解散——与启动时的 CleanupStaleRooms 语义一致，
	//     避免人机练习/手动建房在真人掉线/退出后残留的机器人占席房间，在单房间约束下
	//     "自动存在"并阻塞新建房间，或被误认为自动创建的房间。
	//     仅作用于 Waiting 房间，对局进行中不受影响（对局按掉线托管逻辑处理）。
	destroy := allGone
	if !destroy && r.State() == StateWaiting && !r.HasOnlineHumanPlayer() {
		destroy = true
	}

	if destroy {
		if game != nil {
			game.Stop() // 锁外停止，避免与 Actor 广播互锁
		}
		m.destroyRoom(r.code)
	}
}

// NotifyOnline 玩家重连回房间
func (m *Manager) NotifyOnline(playerID string) {
	r := m.RoomByPlayer(playerID)
	if r == nil {
		return
	}
	r.NotifyOnline(playerID)
	m.logger.Info("玩家重连", "room", r.code, "player", playerID)
}

// --- 清理与持久化 ---

// destroyRoom 销毁房间（删快照）
func (m *Manager) destroyRoom(code string) {
	m.mu.Lock()
	r, exists := m.rooms[code]
	delete(m.rooms, code)
	if exists {
		// 置位销毁标志：防止并发中的 persistLoop 把已销毁房间的快照重新写回
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()
	}
	m.mu.Unlock()

	if !exists {
		return
	}
	r.StopGame()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// 补写一次房间记录再标记关闭：确保快速创建即销毁的房间也留有记录
		if err := m.store.UpsertRoom(ctx, m.roomRecord(r)); err != nil {
			m.logger.Error("房间记录落库失败", "room", code, "err", err)
		}
		if err := m.store.DeleteRoomSnapshot(ctx, code); err != nil {
			m.logger.Error("删除房间快照失败", "room", code, "err", err)
		}
		if err := m.store.CloseRoomRecord(ctx, code); err != nil {
			m.logger.Error("关闭房间记录失败", "room", code, "err", err)
		}
	}()
	m.logger.Info("房间解散", "room", code)
}

// roomRecord 构建房间持久化记录（房间 ID、归属游戏、创建人、参与人）
func (m *Manager) roomRecord(r *Room) store.RoomRecord {
	seats := r.AllSeatsInfo()
	ids := make([]string, 0, len(seats))
	creatorName := ""
	creatorID := r.CreatorID()
	for _, s := range seats {
		ids = append(ids, s.ID)
		if s.ID == creatorID {
			creatorName = s.Name
		}
	}
	return store.RoomRecord{
		RoomCode:    r.Code(),
		GameID:      r.GameID(),
		CreatorID:   creatorID,
		CreatorName: creatorName,
		PlayerIDs:   ids,
		Status:      store.RoomStatusOpen,
		CreatedAt:   r.CreatedAt(),
	}
}

// CleanupStaleRooms 启动时清理无活跃创建者的房间：
// 每个房间都有一个创建者（不可被分配为机器人），创建者离线则该房间视为残留，直接销毁。
// 覆盖场景：纯机器人房间（无创建者）、创建者已离线的人机房。
func (m *Manager) CleanupStaleRooms() {
	m.mu.RLock()
	var stale []string
	for code, r := range m.rooms {
		r.mu.Lock()
		creatorOnline := false
		for _, s := range r.seats {
			if s.PlayerID == r.creatorID && s.Online {
				creatorOnline = true
				break
			}
		}
		r.mu.Unlock()

		if !creatorOnline {
			stale = append(stale, code)
		}
	}
	m.mu.RUnlock()

	for _, code := range stale {
		m.destroyRoom(code)
		m.logger.Info("启动清理：残留房间已销毁", "room", code)
	}
}

// CleanupPlayerStaleRooms 清理创建者为指定玩家的旧房间，仅保留最新的一个。
// 适用于应用重启后客户端重连时：同一玩家可能创建过多个房间（上次异常退出），
// 保留 createdAt 最新的房间，销毁其余。
func (m *Manager) CleanupPlayerStaleRooms(playerID string) {
	m.mu.RLock()
	var playerRooms []*Room
	for _, r := range m.rooms {
		if r.CreatorID() == playerID {
			playerRooms = append(playerRooms, r)
		}
	}
	m.mu.RUnlock()

	if len(playerRooms) <= 1 {
		return // 只有一个或没有房间，无需清理
	}

	// 找到最新的房间（按 createdAt 最晚）
	var latest *Room
	for _, r := range playerRooms {
		if latest == nil || r.CreatedAt().After(latest.CreatedAt()) {
			latest = r
		}
	}

	// 销毁其余房间
	for _, r := range playerRooms {
		if r.Code() != latest.Code() {
			m.destroyRoom(r.Code())
			m.logger.Info("启动清理：玩家旧房间已销毁", "room", r.Code(), "player", playerID, "kept", latest.Code())
		}
	}
}

// cleanupLoop 每分钟回收超时的等待房间（复用原 cleanupLoop 逻辑）
func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.cleanupExpired()
		case <-m.stopC:
			return
		}
	}
}

func (m *Manager) cleanupExpired() {
	timeout := m.gameCfg.RoomTimeoutDuration()
	now := time.Now()

	m.mu.RLock()
	expired := make([]*Room, 0)
	for _, r := range m.rooms {
		// 清理两类残留等待房间：
		//  1. 超过存活超时（超时兜底）；
		//  2. 已无任何在线真人的等待房间（真人掉线/仅剩机器人）——作为 NotifyOffline
		//     立即解散的安全网，覆盖连接清理被遗漏的场景，避免残留房间"自动存在"。
		//     对局进行中不受影响。
		if r.State() == StateWaiting && (now.Sub(r.CreatedAt()) > timeout || !r.HasOnlineHumanPlayer()) {
			expired = append(expired, r)
		}
	}
	m.mu.RUnlock()

	for _, r := range expired {
		// 释放锁期间房间可能已开始对局（Waiting→Playing），重新检查状态避免误杀
		if r.State() != StateWaiting {
			continue
		}
		r.BroadcastError(protocol.ErrCodeUnknown, "房间超时已关闭")
		m.destroyRoom(r.code)
		m.logger.Info("房间超时清理", "room", r.code)
	}
}

// persistLoop 快照防抖刷盘：每秒将脏房间序列化写入 SQLite（技术设计 6.1 / 7.1）
func (m *Manager) persistLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.persistDirty()
		case <-m.stopC:
			return
		}
	}
}

func (m *Manager) persistDirty() {
	m.mu.RLock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.mu.RUnlock()

	for _, r := range rooms {
		// 销毁竞态防护：收集列表后房间可能已被销毁（destroyRoom 会异步删除快照），
		// 落库前校验房间仍存在且为同一实例，避免已销毁房间的快照被重新写回
		// 导致应用重启后旧房间“复活”。
		if m.GetRoom(r.code) != r {
			continue
		}
		// 房间记录（ID/游戏/创建人/参与人）随防抖刷盘同步落库
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := m.store.UpsertRoom(ctx, m.roomRecord(r)); err != nil {
				m.logger.Error("房间记录落库失败", "room", r.code, "err", err)
			}
		}()
		rs, game := r.PrepareSnapshot()
		if rs == nil {
			continue
		}
		// 锁外取对局快照，避免与 Actor 互锁（[]byte → json.RawMessage）
		if game != nil {
			rs.Game = game.Snapshot()
		}
		stateJSON, err := encodeSnapshot(rs)
		if err != nil {
			m.logger.Error("房间快照编码失败", "room", r.code, "err", err)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := m.store.SaveRoomSnapshot(ctx, r.code, stateJSON); err != nil {
			m.logger.Error("房间快照落库失败", "room", r.code, "err", err)
		}
		cancel()
	}
}

// --- 崩溃恢复 ---

// RestoreFromStore 启动时回放 room_snapshots：Waiting 房间恢复座位；
// Playing 房间恢复对局 Actor（玩家离线，等待重连；超时托管保证对局推进）
func (m *Manager) RestoreFromStore(ctx context.Context) {
	all, err := m.store.LoadRoomSnapshots(ctx)
	if err != nil {
		m.logger.Error("加载房间快照失败", "err", err)
		return
	}

	for code, stateJSON := range all {
		snap, err := decodeSnapshot(stateJSON)
		if err != nil || snap.RoomCode != code {
			m.logger.Warn("房间快照损坏，丢弃", "room", code, "err", err)
			go func(c string) {
				_ = m.store.DeleteRoomSnapshot(context.Background(), c)
			}(code)
			continue
		}
		// 只恢复进行中的对局（断线重连场景）；不自动恢复等待中的房间，
		// 避免上一会话遗留的建房/人机房间在重启后"自动出现"，与"仅手动创建房间"的交互一致。
		if snap.State != snapStatePlaying {
			m.logger.Info("丢弃等待中的房间快照（仅手动创建才建房）", "room", code)
			go func(c string) {
				_ = m.store.DeleteRoomSnapshot(context.Background(), c)
			}(code)
			continue
		}
		if err := m.restoreRoom(snap); err != nil {
			m.logger.Warn("房间恢复失败，丢弃", "room", code, "err", err)
			continue
		}
		m.logger.Info("房间已恢复", "room", code, "state", snap.State)
	}
}

func (m *Manager) restoreRoom(snap *RoomSnapshot) error {
	gdef, ok := games.Get(snap.GameID)
	if !ok {
		return fmt.Errorf("游戏 %q 未注册", snap.GameID)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.rooms[snap.RoomCode]; exists {
		return nil
	}

	r := newRoom(snap.RoomCode, gdef, m.sender, m.store, GameConfigTimeouts(m.gameCfg), m.logger)
	r.RestoreWaiting(snap)
	m.rooms[snap.RoomCode] = r

	if snap.State == snapStatePlaying && len(snap.Game) > 0 {
		r.mu.Lock()
		env := r.sessionEnvLocked()
		env.Options = snap.Options
		r.mu.Unlock()
		gs, err := gdef.RestoreSession(env, snap.Game)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithCancel(context.Background())
		r.mu.Lock()
		r.state = StatePlaying
		r.game = gs
		r.gameCancel = cancel
		r.mu.Unlock()
		go gs.Run(ctx)
	}
	return nil
}

// Shutdown 优雅关闭：停止全部对局 Actor 并停止后台协程
func (m *Manager) Shutdown() {
	close(m.stopC)

	m.mu.Lock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.mu.Unlock()

	for _, r := range rooms {
		r.StopGame()
	}
	m.persistDirty() // 最后一次刷盘
}

// generateRoomCode 生成不冲突的 6 位数字房号（调用方持 m.mu）
func (m *Manager) generateRoomCode() string {
	for {
		code := make([]byte, roomCodeLength)
		for i := range code {
			code[i] = roomCodeChars[rand.IntN(len(roomCodeChars))]
		}
		if _, exists := m.rooms[string(code)]; !exists {
			return string(code)
		}
	}
}
