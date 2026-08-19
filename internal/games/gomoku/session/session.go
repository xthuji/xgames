// Package session 实现五子棋 GameSession Actor：每局对局一个 goroutine，
// 所有对局状态（棋盘、回合、计时器）只在 Actor 内读写，无锁。
//
// 规则：15×15 棋盘，0 号座执黑先行，先连五者胜，棋盘下满判平；
// 回合超时自动转挂机，由机器人决策引擎代下。
package session

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"xgames/internal/games"
	"xgames/internal/games/bot/replay"
	"xgames/internal/games/gomoku/bot"
	"xgames/internal/games/gomoku/msg"
	"xgames/internal/platform/protocol"
)

// Phase 对局阶段
type Phase int

const (
	PhaseInit Phase = iota
	PhasePlaying
	PhaseEnded
)

// 领域错误（经消息入口映射为 protocol 错误码）
var (
	ErrGameNotStart    = errors.New("游戏尚未开始")
	ErrNotYourTurn     = errors.New("还没轮到您")
	ErrOccupied        = errors.New("该位置已有棋子")
	ErrOutOfBounds     = errors.New("落子位置超出棋盘")
	ErrSessionDown     = errors.New("对局已结束")
	ErrDrawNotYourTurn = errors.New("非你的回合，不能请求和棋")
	ErrDrawNoPending   = errors.New("当前没有待你应答的和棋请求")
)

// 时间源（包级变量，测试可注入加速时钟）
var (
	waitAfter = func(d time.Duration) *time.Timer { return time.NewTimer(d) }
	timerNow  = time.Now
)

// Player 对局中的玩家
type Player struct {
	ID        string
	Name      string
	Seat      int
	IsBot     bool
	IsOffline bool
	Afk       bool // 挂机（显式托管）：仅挂机/离线时机器人才可代为决策
}

// 平台端口类型别名（实现 games.Session；具体定义见 internal/games）
type (
	Broadcaster  = games.Broadcaster
	ResultSink   = games.ResultSink
	GameResult   = games.GameResult
	PlayerResult = games.PlayerResult
	Timeouts     = games.Timeouts
)

// --- Actor 事件 ---

type moveEvent struct {
	playerID string
	row, col int
	reply    chan<- error
}

type afkEvent struct {
	playerID string
	afk      bool
	reply    chan<- error
}

type offlineEvent struct{ playerID string }
type onlineEvent struct{ playerID string }

type resignEvent struct {
	playerID string
	reply    chan<- error
}

type drawRequestEvent struct {
	playerID string
	reply    chan<- error
}

type drawResponseEvent struct {
	playerID string
	accept   bool
	reply    chan<- error
}

type stateReq struct {
	playerID string
	reply    chan<- *msg.GkGameStateDTO
}

type snapshotReq struct{ reply chan<- *Snapshot }

type stopEvent struct{ reply chan<- struct{} }

// Session GameSession Actor
type Session struct {
	roomCode    string
	players     []*Player
	broadcaster Broadcaster
	sink        ResultSink
	timeouts    Timeouts
	logger      *slog.Logger
	engine      bot.DecisionEngine // 机器人决策引擎（真人挂机托管用）
	drawEval   func(board []int) bool // 机器人和棋评估函数（由 Room 注入 bot.Controller.EvaluateDrawOffer）
	drawRequester int                  // 待处理和棋请求的请求者索引（-1=无，防止伪造应答）

	events   chan any
	shutdown chan any // 请求-响应类事件走无缓冲队列，避免与慢发送者互锁
	done     chan struct{}

	// ---- 以下状态仅在 Actor goroutine 内读写（无锁） ----

	phase Phase
	ended bool // 对局已结算，Actor 即将退出

	board         []int
	currentPlayer int // 当前落子玩家索引（0/1）
	moveNumber    int // 下一手序号（1 起）
	lastRow       int // 最近一手位置（-1 = 尚未落子）
	lastCol       int

	// 复盘系统：真人玩家决策记录（bot 与托管代下不记录）
	playerLogger  *replay.PlayerReplayLogger
	gameStartTime time.Time

	// 定时器（select + channel 实现）
	timer          *time.Timer      // 当前回合计时器
	timerStartedAt time.Time        // 计时器启动时刻
	turnTotal      time.Duration    // 本轮计时总时长（离线暂停后恢复用）
	turnPaused     bool             // 当前回合计时是否被离线暂停
	pausedRest     time.Duration    // 暂停时的剩余时间
	offlineTimer   *time.Timer      // 离线等待计时器
	offlineIdx     int              // 触发离线等待的玩家索引

	// 快照恢复（仅 Run 启动时使用）
	restored *Snapshot
}

// New 创建 GameSession Actor（未启动）。players 按座位顺序（2 人）。
func New(roomCode string, players []*Player, bc Broadcaster, sink ResultSink, to Timeouts) *Session {
	return &Session{
		roomCode:     roomCode,
		players:      players,
		broadcaster:  bc,
		sink:         sink,
		timeouts:     to,
		logger:       slog.Default(),
		events:       make(chan any, 64),
		shutdown:     make(chan any),
		done:         make(chan struct{}),
		phase:        PhaseInit,
		lastRow:      -1,
		lastCol:      -1,
		drawRequester: -1,
		playerLogger: replay.NewPlayerReplayLogger("gomoku", roomCode),
	}
}

// SetEngine 注入机器人决策引擎（真人挂机托管复用同一套引擎）。
// 必须在 Run 之前调用（Room 开局时注入；未启用机器人时为 nil）。
func (gs *Session) SetEngine(e bot.DecisionEngine) { gs.engine = e }

// SetDrawEvaluator 注入机器人和棋评估函数（由 Room 在开局时注入）。
func (gs *Session) SetDrawEvaluator(fn func(board []int) bool) { gs.drawEval = fn }

// Run Actor 事件循环（每局一个 goroutine）。启动后立即进入对局。
func (gs *Session) Run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			gs.logger.Error("GameSession panic", "room", gs.roomCode, "panic", r)
		}
		close(gs.done)
	}()

	if gs.restored != nil {
		gs.applySnapshot(gs.restored)
		gs.restored = nil
	} else {
		gs.startGame()
	}

	for {
		select {
		case ev := <-gs.shutdown:
			if gs.handleEvent(ev) || gs.ended {
				return
			}
		case ev, ok := <-gs.events:
			if !ok {
				return
			}
			if gs.handleEvent(ev) || gs.ended {
				return
			}
		case <-timerChan(gs.timer):
			stopTimer(gs.timer)
			gs.timer = nil
			gs.handleTurnTimeout()
		case <-timerChan(gs.offlineTimer):
			stopTimer(gs.offlineTimer)
			gs.offlineTimer = nil
			gs.handleOfflineTimeout()
		case <-ctx.Done():
			gs.logger.Info("GameSession 收到关闭信号", "room", gs.roomCode)
			return
		}
	}
}

// Done Actor 退出信号
func (gs *Session) Done() <-chan struct{} { return gs.done }

// handleEvent 分发事件；返回 true 表示 Actor 应退出
func (gs *Session) handleEvent(ev any) bool {
	switch e := ev.(type) {
	case moveEvent:
		gs.exitAfkOnAction(e.playerID) // 真人主动行动自动取消挂机
		e.reply <- gs.handleMove(e.playerID, e.row, e.col)
	case afkEvent:
		e.reply <- gs.handleAfk(e.playerID, e.afk)
	case resignEvent:
		e.reply <- gs.handleResign(e.playerID)
	case drawRequestEvent:
		e.reply <- gs.handleDrawRequest(e.playerID)
	case drawResponseEvent:
		e.reply <- gs.handleDrawResponse(e.playerID, e.accept)
	case offlineEvent:
		gs.handleOffline(e.playerID)
	case onlineEvent:
		gs.handleOnline(e.playerID)
	case stateReq:
		e.reply <- gs.gameStateFor(e.playerID)
	case snapshotReq:
		e.reply <- gs.snapshot()
	case stopEvent:
		close(e.reply)
		return true
	}
	return false
}

// --- 公开 API（供消息入口 / 机器人调度调用，全部经事件队列串行化） ---

// Move 落子
func (gs *Session) Move(playerID string, row, col int) error {
	reply := make(chan error, 1)
	if !gs.call(moveEvent{playerID: playerID, row: row, col: col, reply: reply}) {
		return ErrSessionDown
	}
	return <-reply
}

// Resign 认输
func (gs *Session) Resign(playerID string) error {
	reply := make(chan error, 1)
	if !gs.call(resignEvent{playerID: playerID, reply: reply}) {
		return ErrSessionDown
	}
	return <-reply
}

// DrawRequest 请求和棋
func (gs *Session) DrawRequest(playerID string) error {
	reply := make(chan error, 1)
	if !gs.call(drawRequestEvent{playerID: playerID, reply: reply}) {
		return ErrSessionDown
	}
	return <-reply
}

// DrawResponse 和棋应答
func (gs *Session) DrawResponse(playerID string, accept bool) error {
	reply := make(chan error, 1)
	if !gs.call(drawResponseEvent{playerID: playerID, accept: accept, reply: reply}) {
		return ErrSessionDown
	}
	return <-reply
}

// PlayerOffline 玩家掉线（暂停其回合计时）
func (gs *Session) PlayerOffline(playerID string) {
	gs.post(offlineEvent{playerID: playerID})
}

// PlayerOnline 玩家重连（恢复计时）
func (gs *Session) PlayerOnline(playerID string) {
	gs.post(onlineEvent{playerID: playerID})
}

// GameStateFor 生成玩家视角的对局状态（重连恢复用）
func (gs *Session) GameStateFor(playerID string) *msg.GkGameStateDTO {
	reply := make(chan *msg.GkGameStateDTO, 1)
	if !gs.call(stateReq{playerID: playerID, reply: reply}) {
		return nil
	}
	return <-reply
}

// SnapshotState 导出全量快照结构（崩溃恢复用）
func (gs *Session) SnapshotState() *Snapshot {
	reply := make(chan *Snapshot, 1)
	if !gs.call(snapshotReq{reply: reply}) {
		return nil
	}
	return <-reply
}

// --- games.Session 适配（平台房间经 games.Session 接口驱动对局） ---

// OnMessage 五子棋私有消息入口：gk_move / gk_afk
func (gs *Session) OnMessage(playerID string, m protocol.Message) error {
	var err error
	switch m.Type {
	case msg.MsgGkMove:
		p, perr := protocol.ParsePayload[msg.GkMovePayload](m)
		if perr != nil {
			return games.NewCodedError(protocol.ErrCodeInvalidMsg, "")
		}
		err = gs.Move(playerID, p.Row, p.Col)
	case msg.MsgGkAfk:
		p, perr := protocol.ParsePayload[msg.GkAfkPayload](m)
		if perr != nil {
			return games.NewCodedError(protocol.ErrCodeInvalidMsg, "")
		}
		err = gs.SetAfk(playerID, p.Afk)
	case msg.MsgGkResign:
		err = gs.Resign(playerID)
	case msg.MsgGkDrawRequest:
		err = gs.DrawRequest(playerID)
	case msg.MsgGkDrawResponse:
		p, perr := protocol.ParsePayload[msg.GkDrawResponsePayload](m)
		if perr != nil {
			return games.NewCodedError(protocol.ErrCodeInvalidMsg, "")
		}
		err = gs.DrawResponse(playerID, p.Accept)
	default:
		return games.NewCodedError(protocol.ErrCodeInvalidMsg, "")
	}
	if err == ErrSessionDown {
		return games.NewCodedError(protocol.ErrCodeGameNotStart, "")
	}
	return toCodedError(err)
}

// SubmitBotAction 机器人动作提交入口（满足 games.BotResponder；动作与真人同走 OnMessage）
func (gs *Session) SubmitBotAction(playerID string, m protocol.Message) error {
	return gs.OnMessage(playerID, m)
}

// GameStateJSON 玩家视角对局状态 JSON（重连恢复；不可用返回 nil）
func (gs *Session) GameStateJSON(playerID string) []byte {
	dto := gs.GameStateFor(playerID)
	if dto == nil {
		return nil
	}
	data, err := json.Marshal(dto)
	if err != nil {
		return nil
	}
	return data
}

// StateMessage request_game_state 响应：完整状态放入 GameStatePayload.State
func (gs *Session) StateMessage(playerID string) (protocol.Message, bool) {
	dto := gs.GameStateFor(playerID)
	if dto == nil {
		return protocol.Message{}, false
	}
	state, err := json.Marshal(dto)
	if err != nil {
		return protocol.Message{}, false
	}
	return protocol.NewMessage(protocol.MsgGameState, protocol.GameStatePayload{
		Available: true,
		State:     state,
	}), true
}

// Snapshot 全量快照 JSON 字节（games.Session 接口；平台房间快照持久化用）
func (gs *Session) Snapshot() []byte {
	snap := gs.SnapshotState()
	if snap == nil {
		return nil
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return nil
	}
	return data
}

// toCodedError 领域错误 → 协议错误码
func toCodedError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrNotYourTurn):
		return games.NewCodedError(msg.ErrCodeNotYourTurn, "")
	case errors.Is(err, ErrOccupied):
		return games.NewCodedError(msg.ErrCodeOccupied, "")
	case errors.Is(err, ErrOutOfBounds):
		return games.NewCodedError(msg.ErrCodeOutOfBounds, "")
	case errors.Is(err, ErrDrawNotYourTurn):
		return games.NewCodedError(msg.ErrCodeDrawNotYourTurn, "")
	case errors.Is(err, ErrGameNotStart), errors.Is(err, ErrSessionDown):
		return games.NewCodedError(protocol.ErrCodeGameNotStart, "")
	default:
		return games.NewCodedError(protocol.ErrCodeUnknown, err.Error())
	}
}

// Stop 停止 Actor（优雅关闭）
func (gs *Session) Stop() {
	reply := make(chan struct{})
	if !gs.call(stopEvent{reply: reply}) {
		return
	}
	<-reply
}

// call 同步请求-响应：经无缓冲 shutdown 通道投递，Actor 退出时返回 false。
// 调用方必须不是 Broadcaster 回调链上的 goroutine（否则可能自锁）。
func (gs *Session) call(ev any) bool {
	select {
	case gs.shutdown <- ev:
		return true
	case <-gs.done:
		return false
	}
}

// post 异步事件（掉线/上线通知）：队列满则丢弃，永不阻塞
func (gs *Session) post(ev any) {
	select {
	case gs.events <- ev:
	case <-gs.done:
	default:
	}
}

// --- 定时器辅助（仅 Actor 内调用） ---

// startTurnTimer 启动当前回合计时器
func (gs *Session) startTurnTimer(d time.Duration) {
	stopTimer(gs.timer)
	gs.timer = waitAfter(d)
	gs.timerStartedAt = timerNow()
	gs.turnTotal = d
	gs.turnPaused = false
	gs.pausedRest = 0
}

// timeLeft 当前回合计时剩余时间（仅 Actor 内、计时运行中调用）
func (gs *Session) timeLeft() time.Duration {
	rest := gs.turnTotal - timerNow().Sub(gs.timerStartedAt)
	if rest < 0 {
		rest = 0
	}
	return rest
}

// stopTurnTimer 停止当前回合计时器
func (gs *Session) stopTurnTimer() {
	stopTimer(gs.timer)
	gs.timer = nil
	gs.turnPaused = false
	gs.pausedRest = 0
}

// --- 内部辅助 ---

// playerIdx 玩家索引（-1 = 不在对局中）
func (gs *Session) playerIdx(id string) int {
	for i, p := range gs.players {
		if p.ID == id {
			return i
		}
	}
	return -1
}

// isCurrentTurnPlayer 玩家是否为当前回合决策者
func (gs *Session) isCurrentTurnPlayer(idx int) bool {
	return gs.phase == PhasePlaying && gs.currentPlayer == idx
}

// broadcast 广播便捷方法
func (gs *Session) broadcast(t protocol.MessageType, payload any) {
	gs.broadcaster.Broadcast(protocol.NewMessage(t, payload))
}

func timerChan(t *time.Timer) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

func stopTimer(t *time.Timer) {
	if t != nil {
		t.Stop()
	}
}
