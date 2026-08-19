// Package room 实现房间与 RoomManager（技术设计 6.1）。
//
// Room 是 games.Session 对局会话的宿主：实现 games.Broadcaster（广播经 WS Hub 下发）
// 与 games.ResultSink（结算落库），并负责准备/开局/掉线/重连/快照持久化。
// 房间经 games.Game 描述符驱动具体游戏，不依赖任何游戏包。
//
// 并发约定：**严禁在持有 room.mu 时调用会话的阻塞 API**（OnMessage/
// Snapshot/GameStateJSON/Stop）——Actor 可能正阻塞在 Broadcast 等待 room.mu，会死锁。
package room

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"xgames/internal/games"
	"xgames/internal/infra/config"
	"xgames/internal/infra/store"
	"xgames/internal/platform/protocol"
)

// State 房间状态
type State string

const (
	StateWaiting State = "waiting"
	StatePlaying State = "playing"
)

// 领域错误（经 WS 层映射为 protocol 错误码）
var (
	ErrRoomNotFound = errors.New("房间不存在")
	ErrRoomFull     = errors.New("房间已满")
	ErrGameStarted  = errors.New("游戏已开始")
	ErrNotInRoom    = errors.New("不在房间中")
	ErrNotEnough    = errors.New("人数不足")
	ErrBotsDisabled = errors.New("机器人未启用")
	ErrGameNotFound = errors.New("游戏不存在")

	ErrNotRoomCreator = errors.New("仅房间创建人可添加机器人")
	ErrRoomExists     = errors.New("服务端仅支持单房间运行，请等待当前对局结束后再创建")
	ErrRoomClosed     = errors.New("房间已关闭")
)

// Sender 输出端口：向指定玩家推送消息（WS Hub 实现；离线玩家由 Hub 丢弃）
type Sender interface {
	SendToPlayer(playerID string, msg protocol.Message)
}

// Seat 房间座位
type Seat struct {
	PlayerID string
	Name     string
	Seat     int
	IsBot    bool
	Ready    bool
	Online   bool
}

// Room 房间：Waiting → Playing → （结算后回到 Waiting 可再来一局）
type Room struct {
	mu        sync.Mutex
	code      string
	creatorID string // 房间创建者（不可被分配为机器人）
	state     State
	createdAt time.Time
	seats     []*Seat // 按座位顺序
	sender    Sender
	store     store.Store
	timeouts  games.Timeouts
	logger    *slog.Logger

	gdef       games.Game    // 房间所属游戏描述符（创建后不变）
	game       games.Session // 当前对局会话（未开局为 nil）
	gameCancel context.CancelFunc
	botCtrl    games.BotController // 机器人控制器（游戏不支持时为 nil）
	options    map[string]string   // 房间选项（如斗地主 difficulty；经 GameStartPayload.Options 下发）
	dirty      bool                // 快照待持久化（1s 防抖由 Manager 统一刷盘）
	closed     bool                // 已销毁（destroyRoom 置位；置位后不再生成快照，防止快照复活）
}

// newRoom 创建房间（由 Manager 调用；gdef 为已注册的游戏描述符）
func newRoom(code string, gdef games.Game, sender Sender, st store.Store, to games.Timeouts, logger *slog.Logger) *Room {
	return &Room{
		code:      code,
		gdef:      gdef,
		state:     StateWaiting,
		createdAt: time.Now(),
		seats:     make([]*Seat, 0, gdef.MaxPlayers()),
		sender:    sender,
		store:     st,
		timeouts:  to,
		botCtrl:   gdef.NewBotController(),
		options:   map[string]string{},
		logger:    logger,
	}
}

// --- 只读访问器 ---

func (r *Room) Code() string { return r.code }

func (r *Room) State() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func (r *Room) CreatedAt() time.Time { return r.createdAt }

// CreatorID 房间创建者玩家 ID（创建者不可被分配为机器人）
func (r *Room) CreatorID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creatorID
}

// SetCreatorID 设置房间创建者（仅 CreateRoom 时调用一次）
func (r *Room) SetCreatorID(playerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.creatorID = playerID
}

// SetOption 设置房间选项（如斗地主机器人难度 difficulty），随 game_start 下发给客户端与会话
func (r *Room) SetOption(key, value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.options[key] = value
	r.dirty = true
}

// GameID 房间所属游戏标识（对应 games.Game.ID）
func (r *Room) GameID() string { return r.gdef.ID() }

// PlayerCount 在座人数
func (r *Room) PlayerCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.seats)
}

// HasPlayer 玩家是否在座
func (r *Room) HasPlayer(playerID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seatByID(playerID) != nil
}

// HasHumanPlayer 房间内是否还有真人（非机器人）在座。仅剩机器人时用于判定解散。
func (r *Room) HasHumanPlayer() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.seats {
		if !s.IsBot {
			return true
		}
	}
	return false
}

// HasOnlineHumanPlayer 房间内是否还有在线真人（非机器人且连接在线）。
// 用于等待中房间的掉线解散判定：真人全部离线（或房间仅剩机器人）时视为残留，应解散。
func (r *Room) HasOnlineHumanPlayer() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.seats {
		if !s.IsBot && s.Online {
			return true
		}
	}
	return false
}

// Game 当前对局会话（未开局返回 nil）
func (r *Room) Game() games.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.game
}

// SeatInfo 玩家信息快照（protocol.PlayerInfo）
func (r *Room) SeatInfo(playerID string) protocol.PlayerInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s := r.seatByID(playerID); s != nil {
		return r.seatToInfo(s)
	}
	return protocol.PlayerInfo{ID: playerID}
}

// AllSeatsInfo 全部座位信息（按座序）
func (r *Room) AllSeatsInfo() []protocol.PlayerInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	infos := make([]protocol.PlayerInfo, len(r.seats))
	for i, s := range r.seats {
		infos[i] = r.seatToInfo(s)
	}
	return infos
}

func (r *Room) seatToInfo(s *Seat) protocol.PlayerInfo {
	return protocol.PlayerInfo{
		ID:     s.PlayerID,
		Name:   s.Name,
		Seat:   s.Seat,
		Ready:  s.Ready,
		Online: s.Online || s.IsBot,
		IsBot:  s.IsBot,
	}
}

// seatByID 按玩家 ID 查座位（调用方持锁）
func (r *Room) seatByID(playerID string) *Seat {
	for _, s := range r.seats {
		if s.PlayerID == playerID {
			return s
		}
	}
	return nil
}

// --- 座位操作（由 Manager 持 Manager 锁外调用） ---

// addSeat 加入座位；满员/已开局返回错误。broadcast 控制是否广播 player_joined（PracticeRoom 静默加座时传 false）。
func (r *Room) addSeat(playerID, name string, isBot bool, broadcast ...bool) (*Seat, error) {
	doBroadcast := true
	if len(broadcast) > 0 {
		doBroadcast = broadcast[0]
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.seats) >= r.gdef.MaxPlayers() {
		return nil, ErrRoomFull
	}
	if r.state != StateWaiting {
		return nil, ErrGameStarted
	}
	if r.seatByID(playerID) != nil {
		return nil, errors.New("已在房间中")
	}

	seat := &Seat{
		PlayerID: playerID,
		Name:     name,
		Seat:     len(r.seats),
		IsBot:    isBot,
		Online:   !isBot,
	}
	r.seats = append(r.seats, seat)
	r.dirty = true

	if doBroadcast {
		r.broadcastLocked(protocol.NewMessage(protocol.MsgPlayerJoined, protocol.PlayerJoinedPayload{
			Player: r.seatToInfo(seat),
		}))
	}
	return seat, nil
}

// removeSeat 移除座位；返回剩余人数
func (r *Room) removeSeat(playerID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, s := range r.seats {
		if s.PlayerID == playerID {
			r.seats = append(r.seats[:i], r.seats[i+1:]...)
			r.broadcastLocked(protocol.NewMessage(protocol.MsgPlayerLeft, protocol.PlayerLeftPayload{
				PlayerID:   s.PlayerID,
				PlayerName: s.Name,
			}))
			break
		}
	}
	r.dirty = true
	return len(r.seats)
}

// setReady 设置准备状态；全员就绪返回 true
func (r *Room) setReady(playerID string, ready bool) (allReady bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	seat := r.seatByID(playerID)
	if seat == nil {
		return false, ErrNotInRoom
	}
	if r.state != StateWaiting {
		return false, ErrGameStarted
	}
	seat.Ready = ready
	r.dirty = true

	r.broadcastLocked(protocol.NewMessage(protocol.MsgPlayerReady, protocol.PlayerReadyPayload{
		PlayerID: playerID,
		Ready:    ready,
	}))

	if len(r.seats) == r.gdef.MaxPlayers() {
		allReady = true
		for _, s := range r.seats {
			if !s.Ready {
				allReady = false
				break
			}
		}
	}
	return allReady, nil
}

// SetAllReady 匹配成功：全员自动准备（不广播单条准备消息，由调用方统一广播）
func (r *Room) SetAllReady() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.seats {
		s.Ready = true
	}
	r.dirty = true
}

// SetBotsReady 仅将机器人置为已准备（不广播，由 room_joined 一次性告知客户端）。
// 人机练习房专用：真人保持未准备，与手动建房流程一致。
func (r *Room) SetBotsReady() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.seats {
		if s.IsBot {
			s.Ready = true
		}
	}
	r.dirty = true
}

// --- 开局与对局生命周期 ---

// StartGame 创建并启动对局会话 Actor（调用前提：满员就座且全部准备）
func (r *Room) StartGame() error {
	r.mu.Lock()
	if r.state != StateWaiting || len(r.seats) != r.gdef.MaxPlayers() {
		r.mu.Unlock()
		return ErrNotEnough
	}
	// 安全保护：如果上一局对局残留未清理，先强制停止，避免新对局被 r.game != nil 阻塞
	if r.game != nil {
		stale := r.game
		staleCancel := r.gameCancel
		r.game = nil
		r.gameCancel = nil
		r.mu.Unlock()
		staleCancel()
		stale.Stop()
		r.mu.Lock()
	}
	r.state = StatePlaying
	for _, s := range r.seats {
		s.Ready = true // 匹配房直接开局时兜底
	}
	// 安全性校验：真人座位必须拥有 playerID（技术设计 6.2：真人永不被当作机器人）
	for _, s := range r.seats {
		if !s.IsBot && s.PlayerID == "" {
			r.state = StateWaiting
			r.mu.Unlock()
			r.logger.Error("开局时发现真人座位缺少 playerID，回滚到 Waiting", "room", r.code)
			return ErrNotEnough
		}
	}

	env := r.sessionEnvLocked()
	options := make(map[string]string, len(r.options))
	for k, v := range r.options {
		options[k] = v
	}
	env.Options = options
	infos := make([]protocol.PlayerInfo, len(r.seats))
	for i, s := range r.seats {
		infos[i] = protocol.PlayerInfo{ID: s.PlayerID, Name: s.Name, Seat: s.Seat, Ready: true, Online: s.Online || s.IsBot, IsBot: s.IsBot}
	}

	ctx, cancel := context.WithCancel(context.Background())
	gs := r.gdef.NewSession(env)
	r.game = gs
	r.gameCancel = cancel
	r.dirty = true
	r.mu.Unlock()

	r.logger.Info("房间开局", "room", r.code, "game", r.gdef.ID())
	// 用 Broadcast（而非 broadcastAll）：game_start 需同步转发给机器人控制器建档
	r.Broadcast(protocol.NewMessage(protocol.MsgGameStart, protocol.GameStartPayload{Players: infos, Options: options}))
	go gs.Run(ctx)
	return nil
}

// sessionEnvLocked 构造会话能力端口（调用方持锁）
func (r *Room) sessionEnvLocked() games.SessionEnv {
	players := make([]games.Player, len(r.seats))
	for i, s := range r.seats {
		players[i] = games.Player{ID: s.PlayerID, Name: s.Name, Seat: s.Seat, IsBot: s.IsBot}
	}
	return games.SessionEnv{
		RoomCode:    r.code,
		Players:     players,
		Broadcaster: r,
		ResultSink:  &resultSink{room: r},
		Timeouts:    r.timeouts,
		Scores:      r.playerScoresLocked(),
	}
}

// playerScoresLocked 查询各座位的平台积分（与 seats 索引对齐；查询失败记 0）。
// 调用方持有 room.mu：SQLite 单行读取耗时极短，可接受持锁查询。
func (r *Room) playerScoresLocked() []int {
	scores := make([]int, len(r.seats))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for i, s := range r.seats {
		st, err := r.store.GetPlayerStats(ctx, r.gdef.ID(), s.PlayerID)
		if err != nil || st == nil {
			continue
		}
		scores[i] = st.Score
	}
	return scores
}

// resultSink 结算端口：对局结束后落库并把房间复位为 Waiting
type resultSink struct{ room *Room }

// OnGameOver 由对局会话 Actor 在结算时调用
func (s *resultSink) OnGameOver(result games.GameResult) {
	r := s.room

	// 过滤机器人并重新分配积分：保证人类玩家积分总和仍为 0。
	// 零和游戏（如斗地主）过滤机器人后，需把机器人的积分按"对手方"原则分配给人类玩家：
	//   - 机器人赢（正分）→ 分给人类输家（减少他们的负数）
	//   - 机器人输（负分）→ 分给人类赢家（增加他们的正数）
	// 零和守卫：仅当全场总分为 0 且机器人积分非 0 时才重分配（非零和游戏不受影响）。
	var humans []store.MatchPlayerResult
	var humanWinners, humanLosers []int // 索引
	botScore, totalScore := 0, 0
	names := map[string]string{} // 玩家 ID → 昵称（结算后推送统计用）

	for _, p := range result.Players {
		totalScore += p.Score
		names[p.PlayerID] = p.PlayerName
		if p.IsBot {
			botScore += p.Score
			continue
		}
		idx := len(humans)
		humans = append(humans, store.MatchPlayerResult{
			PlayerID:   p.PlayerID,
			IsLandlord: p.IsLandlord,
			Score:      p.Score,
		})
		if p.Score > 0 {
			humanWinners = append(humanWinners, idx)
		} else if p.Score < 0 {
			humanLosers = append(humanLosers, idx)
		}
	}

	// 重新分配 botScore（零和时 botScore 恒等于 -humanOriginalSum）
	if totalScore == 0 && botScore != 0 && len(humans) > 0 {
		if botScore > 0 {
			// 机器人赢了：把正分平摊给人类输家
			splitEvenly(humans, humanLosers, botScore)
		} else {
			// 机器人输了：把负分平摊给人类赢家（等于增加赢家分数）
			splitEvenly(humans, humanWinners, -botScore)
		}
	}

	if len(humans) > 0 {
		var mr store.MatchResult
		mr.GameID = r.GameID()
		mr.LandlordID = result.LandlordID
		mr.LandlordWin = result.LandlordWin
		mr.Multiplier = result.Multiplier
		mr.Players = humans

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := r.store.RecordMatch(ctx, mr); err != nil {
			r.logger.Error("结算落库失败", "room", r.code, "err", err)
		} else {
			pushStats(r, humans, names)
		}
		cancel()
	}

	// 房间复位：回到 Waiting（可再来一局）；机器人保持已准备，
	// 人机房返回房间后玩家一键准备即可自动重开
	r.mu.Lock()
	r.state = StateWaiting
	for _, seat := range r.seats {
		seat.Ready = seat.IsBot
	}
	r.game = nil
	if r.gameCancel != nil {
		r.gameCancel()
		r.gameCancel = nil
	}
	r.dirty = true
	r.mu.Unlock()
}

// splitEvenly 把 delta 平均分给 humans 中下标为 idxs 的玩家：
// 每轮按剩余未分配人数均摊（portion = delta / 剩余人数），尾差自然落入最后一位，
// 保证各人份额相等（或至多差 1）且总额恰为 delta。
func splitEvenly(humans []store.MatchPlayerResult, idxs []int, delta int) {
	for i, idx := range idxs {
		portion := delta / (len(idxs) - i)
		humans[idx].Score += portion
		delta -= portion
	}
	if delta != 0 && len(idxs) > 0 {
		humans[idxs[len(idxs)-1]].Score += delta
	}
}

// pushStats 结算落库成功后向真人玩家推送权威统计。
// game_over 广播携带的是重分配前的原始得分，与人机房落库口径存在差异，
// 客户端收到本消息后把累计积分/排名显示对齐到服务端口径。
func pushStats(r *Room, humans []store.MatchPlayerResult, names map[string]string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gameID := r.GameID()
	for _, p := range humans {
		st, err := r.store.GetPlayerStats(ctx, gameID, p.PlayerID)
		if err != nil || st == nil {
			continue
		}
		rank, _ := r.store.PlayerRank(ctx, gameID, p.PlayerID)
		var winRate float64
		if st.TotalGames > 0 {
			winRate = float64(st.Wins) / float64(st.TotalGames)
		}
		r.sender.SendToPlayer(p.PlayerID, protocol.NewMessage(protocol.MsgStatsResult, protocol.StatsResultPayload{
			PlayerID:      p.PlayerID,
			PlayerName:    names[p.PlayerID],
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
}

// --- 掉线 / 重连 ---

// NotifyOffline 玩家掉线：标记座位、通知房间、暂停对局计时。
// 返回是否需要销毁房间（全员离线且无机器人）。调用方在销毁时负责 Stop 对局。
func (r *Room) NotifyOffline(playerID string, offlineWait int) (allGone bool, game games.Session) {
	r.mu.Lock()
	seat := r.seatByID(playerID)
	if seat == nil {
		r.mu.Unlock()
		return false, nil
	}
	seat.Online = false
	r.dirty = true

	allGone = true
	for _, s := range r.seats {
		if s.Online || s.IsBot {
			allGone = false
		}
	}

	// 通知其他在线玩家
	msg := protocol.NewMessage(protocol.MsgPlayerOffline, protocol.PlayerOfflinePayload{
		PlayerID:   seat.PlayerID,
		PlayerName: seat.Name,
		Timeout:    offlineWait,
	})
	for _, s := range r.seats {
		if s.PlayerID != playerID && s.Online && !s.IsBot {
			r.sender.SendToPlayer(s.PlayerID, msg)
		}
	}
	game = r.game
	r.mu.Unlock()

	// 对局中：暂停该玩家计时（post 非阻塞，可安全在锁外调用）
	if game != nil {
		game.PlayerOffline(playerID)
	}
	return allGone, game
}

// NotifyOnline 玩家重连：标记座位、通知房间、恢复对局计时
func (r *Room) NotifyOnline(playerID string) {
	r.mu.Lock()
	seat := r.seatByID(playerID)
	if seat == nil {
		r.mu.Unlock()
		return
	}
	seat.Online = true
	r.dirty = true

	msg := protocol.NewMessage(protocol.MsgPlayerOnline, protocol.PlayerOnlinePayload{
		PlayerID:   seat.PlayerID,
		PlayerName: seat.Name,
	})
	for _, s := range r.seats {
		if s.PlayerID != playerID && s.Online && !s.IsBot {
			r.sender.SendToPlayer(s.PlayerID, msg)
		}
	}
	game := r.game
	r.mu.Unlock()

	if game != nil {
		game.PlayerOnline(playerID)
	}
}

// --- games.Broadcaster 实现（会话的输出端口） ---

// Broadcast 广播给房间全部真人玩家；有机器人时同步转发给机器人控制器
func (r *Room) Broadcast(msg protocol.Message) {
	r.mu.Lock()
	hasBots := r.hasBotsLocked()
	game := r.game
	r.broadcastLocked(msg)
	r.mu.Unlock()

	if hasBots && r.botCtrl != nil && game != nil {
		if resp, ok := game.(games.BotResponder); ok {
			r.botCtrl.OnBroadcast(r.code, msg, resp)
		}
	}
}

// broadcastLocked 广播（调用方持锁）
func (r *Room) broadcastLocked(msg protocol.Message) {
	for _, s := range r.seats {
		if !s.IsBot && s.Online {
			r.sender.SendToPlayer(s.PlayerID, msg)
		}
	}
}

// broadcastAll 广播给全部真人玩家（忽略在线状态，重连后可补收）
func (r *Room) broadcastAll(msg protocol.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.seats {
		if !s.IsBot {
			r.sender.SendToPlayer(s.PlayerID, msg)
		}
	}
}

// SendTo 定向发送：机器人座位转发给控制器，真人经 Hub 下发（离线由 Hub 丢弃）
func (r *Room) SendTo(playerID string, msg protocol.Message) {
	r.mu.Lock()
	seat := r.seatByID(playerID)
	isBot := seat != nil && seat.IsBot
	game := r.game
	ctrl := r.botCtrl
	r.mu.Unlock()

	if isBot {
		if ctrl != nil && game != nil {
			if resp, ok := game.(games.BotResponder); ok {
				ctrl.OnTargeted(r.code, playerID, msg, resp)
			}
		}
		return
	}
	r.sender.SendToPlayer(playerID, msg)
}

// BroadcastNameChange 通知房间内其他玩家某玩家昵称变更
func (r *Room) BroadcastNameChange(playerID, newName string) {
	r.mu.Lock()
	for _, s := range r.seats {
		if s.PlayerID == playerID {
			s.Name = newName
		}
	}
	msg := protocol.NewMessage(protocol.MsgProfileUpdated, protocol.ProfileUpdatedPayload{
		PlayerID:   playerID,
		PlayerName: newName,
	})
	targets := make([]*Seat, 0, len(r.seats))
	for _, s := range r.seats {
		if s.PlayerID != playerID && !s.IsBot && s.Online {
			targets = append(targets, s)
		}
	}
	r.mu.Unlock()
	for _, s := range targets {
		r.sender.SendToPlayer(s.PlayerID, msg)
	}
}

// hasBotsLocked 房间内是否有机器人（调用方持锁）
func (r *Room) hasBotsLocked() bool {
	for _, s := range r.seats {
		if s.IsBot {
			return true
		}
	}
	return false
}

// --- 游戏消息路由 ---

// RouteGameMessage 把玩家的游戏私有消息转给当前对局会话。
// 无对局时返回携带协议错误码的错误（由 WS 层回给客户端）。
func (r *Room) RouteGameMessage(playerID string, msg protocol.Message) error {
	r.mu.Lock()
	game := r.game
	r.mu.Unlock()

	if game == nil {
		return games.NewCodedError(protocol.ErrCodeGameNotStart, "")
	}
	return game.OnMessage(playerID, msg)
}

// --- 快照 ---

// PrepareSnapshot 摘取快照所需状态（锁内只取数据，锁外再调 game.Snapshot 避免死锁）
func (r *Room) PrepareSnapshot() (rs *RoomSnapshot, game games.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.dirty || r.closed {
		return nil, nil
	}
	r.dirty = false

	rs = &RoomSnapshot{
		RoomCode:  r.code,
		GameID:    r.gdef.ID(),
		State:     string(r.state),
		CreatedAt: r.createdAt,
		CreatorID: r.creatorID,
		Options:   r.options,
	}
	for _, s := range r.seats {
		rs.Players = append(rs.Players, RoomPlayer{
			ID: s.PlayerID, Name: s.Name, Seat: s.Seat, IsBot: s.IsBot, Ready: s.Ready,
		})
	}
	return rs, r.game
}

// RestoreWaiting 从快照恢复 Waiting 房间座位与选项（玩家全部离线，等待重连）
func (r *Room) RestoreWaiting(snap *RoomSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.createdAt = snap.CreatedAt
	r.creatorID = snap.CreatorID
	// 向后兼容：旧快照无 creatorID，取第一个非机器人玩家
	if r.creatorID == "" {
		for _, p := range snap.Players {
			if !p.IsBot {
				r.creatorID = p.ID
				break
			}
		}
	}
	for k, v := range snap.Options {
		r.options[k] = v
	}
	for _, p := range snap.Players {
		r.seats = append(r.seats, &Seat{
			PlayerID: p.ID, Name: p.Name, Seat: p.Seat,
			IsBot: p.IsBot, Ready: p.Ready, Online: p.IsBot,
		})
	}
}

// StopGame 停止对局 Actor（房间销毁时调用，必须在锁外）
func (r *Room) StopGame() {
	r.mu.Lock()
	game := r.game
	cancel := r.gameCancel
	r.game = nil
	r.gameCancel = nil
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if game != nil {
		game.Stop()
	}
}

// ForceEnd 强制终止对局（客户端 UI 离开对战页面时调用）。
// 不记录战绩，直接复位房间为 Waiting 状态。
func (r *Room) ForceEnd() {
	r.mu.Lock()
	game := r.game
	cancel := r.gameCancel
	if game == nil {
		r.mu.Unlock()
		return // 无对局，幂等
	}
	r.game = nil
	r.gameCancel = nil
	r.state = StateWaiting
	for _, s := range r.seats {
		s.Ready = false
	}
	r.dirty = true
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if game != nil {
		game.Stop()
	}
	r.logger.Info("对局被强制终止", "room", r.code)
}

// BroadcastSystem 系统消息（房间解散通知等）
func (r *Room) BroadcastError(code int, text string) {
	r.broadcastAll(protocol.NewMessage(protocol.MsgError, protocol.ErrorPayload{Code: code, Message: text}))
}

// GameConfigTimeouts 从配置构造对局超时参数（平台缺省值，各游戏自行取用）
func GameConfigTimeouts(cfg config.GameConfig) games.Timeouts {
	return games.Timeouts{
		Turn:        cfg.TurnTimeoutDuration(),
		Bid:         cfg.BidTimeoutDuration(),
		OfflineWait: cfg.OfflineWaitTimeoutDuration(),
	}
}
