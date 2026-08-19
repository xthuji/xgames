// Package session 实现中国象棋 GameSession Actor。
//
// 规则：9×10 棋盘，0 号座执红先行，将死/困毙对方胜；
// 三次重复局面、13 回合未吃子（自然限着）、双方均无取胜可能子力局面判和；
// 回合超时自动转挂机，由机器人决策引擎代走。
package session

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"xgames/internal/games"
	"xgames/internal/games/bot/replay"
	"xgames/internal/games/chess/bot"
	"xgames/internal/games/chess/msg"
	"xgames/internal/games/chess/rule"
	"xgames/internal/platform/protocol"
)

// Phase 对局阶段
type Phase int

const (
	PhaseInit Phase = iota
	PhasePlaying
	PhaseEnded
)

var (
	ErrGameNotStart    = errors.New("游戏尚未开始")
	ErrNotYourTurn     = errors.New("还没轮到您")
	ErrNotYourPiece    = errors.New("不能移动对方的棋子")
	ErrIllegalMove     = errors.New("该棋子无法走到此位置")
	ErrSessionDown     = errors.New("对局已结束")
	ErrDrawNotYourTurn = errors.New("非你的回合，不能请求和棋")
	ErrDrawNoPending   = errors.New("当前没有待你应答的和棋请求")
)

var (
	waitAfter = func(d time.Duration) *time.Timer { return time.NewTimer(d) }
	timerNow  = time.Now
)

type Player struct {
	ID        string
	Name      string
	Seat      int
	IsBot     bool
	IsOffline bool
	Afk       bool
}

type (
	Broadcaster  = games.Broadcaster
	ResultSink   = games.ResultSink
	GameResult   = games.GameResult
	PlayerResult = games.PlayerResult
	Timeouts     = games.Timeouts
)

// --- Actor 事件 ---

type moveEvent struct {
	playerID           string
	fromRow, fromCol   int
	toRow, toCol       int
	reply              chan<- error
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
	reply    chan<- *msg.CcGameStateDTO
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
	engine      bot.DecisionEngine
	drawEval   func(board []int, camp int) bool // 机器人和棋评估函数（由 Room 注入）
	drawRequester int                              // 待处理和棋请求的请求者索引（-1=无，防止伪造应答）

	events   chan any
	shutdown chan any
	done     chan struct{}

	phase Phase
	ended bool

	board         []int
	currentPlayer int  // 当前走子玩家索引（0=红, 1=黑）
	moveNumber    int
	lastFromRow   int
	lastFromCol   int
	lastToRow     int
	lastToCol     int

	redCaptures   []int // 红方吃掉的棋子
	blackCaptures []int // 黑方吃掉的棋子

	// 和棋判定（不变作和 / 自然限着 / 简单和棋局面）
	posHistory    []uint64        // 局面哈希序列（含初始局面，供快照恢复重建计数）
	posCount      map[uint64]int  // 局面哈希 → 出现次数（含执子方，rule.PositionHash 口径）
	halfmoveClock int             // 连续未吃子步数（ply 口径，吃子归零）
	drawReason    rule.DrawReason // 判和原因（endGameDraw 设置，endGame 结算时写入广播）

	// 复盘系统：真人玩家决策记录（bot 与托管代走不记录）
	playerLogger  *replay.PlayerReplayLogger
	gameStartTime time.Time

	timer          *time.Timer
	timerStartedAt time.Time
	turnTotal      time.Duration
	turnPaused     bool
	pausedRest     time.Duration
	offlineTimer   *time.Timer
	offlineIdx     int

	restored *Snapshot
}

// New 创建 GameSession Actor（未启动）
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
		lastFromRow:  -1,
		lastFromCol:  -1,
		lastToRow:    -1,
		lastToCol:    -1,
		posCount:     make(map[uint64]int),
		drawRequester: -1,
		playerLogger: replay.NewPlayerReplayLogger("chess", roomCode),
	}
}

func (gs *Session) SetEngine(e bot.DecisionEngine) { gs.engine = e }

// SetDrawEvaluator 注入机器人和棋评估函数（由 Room 在开局时注入）。
func (gs *Session) SetDrawEvaluator(fn func(board []int, camp int) bool) { gs.drawEval = fn }

// Run Actor 事件循环
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

func (gs *Session) Done() <-chan struct{} { return gs.done }

func (gs *Session) handleEvent(ev any) bool {
	switch e := ev.(type) {
	case moveEvent:
		gs.exitAfkOnAction(e.playerID)
		e.reply <- gs.handleMove(e.playerID, e.fromRow, e.fromCol, e.toRow, e.toCol)
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

// --- 公开 API ---

func (gs *Session) Move(playerID string, fromRow, fromCol, toRow, toCol int) error {
	reply := make(chan error, 1)
	if !gs.call(moveEvent{playerID: playerID, fromRow: fromRow, fromCol: fromCol, toRow: toRow, toCol: toCol, reply: reply}) {
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

func (gs *Session) PlayerOffline(playerID string) { gs.post(offlineEvent{playerID: playerID}) }
func (gs *Session) PlayerOnline(playerID string)  { gs.post(onlineEvent{playerID: playerID}) }

func (gs *Session) GameStateFor(playerID string) *msg.CcGameStateDTO {
	reply := make(chan *msg.CcGameStateDTO, 1)
	if !gs.call(stateReq{playerID: playerID, reply: reply}) {
		return nil
	}
	return <-reply
}

func (gs *Session) SnapshotState() *Snapshot {
	reply := make(chan *Snapshot, 1)
	if !gs.call(snapshotReq{reply: reply}) {
		return nil
	}
	return <-reply
}

// --- games.Session 适配 ---

func (gs *Session) OnMessage(playerID string, m protocol.Message) error {
	var err error
	switch m.Type {
	case msg.MsgCcMove:
		p, perr := protocol.ParsePayload[msg.CcMovePayload](m)
		if perr != nil {
			return games.NewCodedError(protocol.ErrCodeInvalidMsg, "")
		}
		err = gs.Move(playerID, p.FromRow, p.FromCol, p.ToRow, p.ToCol)
	case msg.MsgCcAfk:
		p, perr := protocol.ParsePayload[msg.CcAfkPayload](m)
		if perr != nil {
			return games.NewCodedError(protocol.ErrCodeInvalidMsg, "")
		}
		err = gs.SetAfk(playerID, p.Afk)
	case msg.MsgCcResign:
		err = gs.Resign(playerID)
	case msg.MsgCcDrawRequest:
		err = gs.DrawRequest(playerID)
	case msg.MsgCcDrawResponse:
		p, perr := protocol.ParsePayload[msg.CcDrawResponsePayload](m)
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

func (gs *Session) SubmitBotAction(playerID string, m protocol.Message) error {
	return gs.OnMessage(playerID, m)
}

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

func toCodedError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrNotYourTurn):
		return games.NewCodedError(msg.ErrCodeNotYourTurn, "")
	case errors.Is(err, ErrNotYourPiece):
		return games.NewCodedError(msg.ErrCodeNotYourPiece, "")
	case errors.Is(err, ErrIllegalMove):
		return games.NewCodedError(msg.ErrCodeIllegalMove, "")
	case errors.Is(err, ErrDrawNotYourTurn):
		return games.NewCodedError(msg.ErrCodeDrawNotYourTurn, "")
	case errors.Is(err, ErrGameNotStart), errors.Is(err, ErrSessionDown):
		return games.NewCodedError(protocol.ErrCodeGameNotStart, "")
	default:
		return games.NewCodedError(protocol.ErrCodeUnknown, err.Error())
	}
}

func (gs *Session) Stop() {
	reply := make(chan struct{})
	if !gs.call(stopEvent{reply: reply}) {
		return
	}
	<-reply
}

func (gs *Session) call(ev any) bool {
	select {
	case gs.shutdown <- ev:
		return true
	case <-gs.done:
		return false
	}
}

func (gs *Session) post(ev any) {
	select {
	case gs.events <- ev:
	case <-gs.done:
	default:
	}
}

// --- 定时器 ---

func (gs *Session) startTurnTimer(d time.Duration) {
	stopTimer(gs.timer)
	gs.timer = waitAfter(d)
	gs.timerStartedAt = timerNow()
	gs.turnTotal = d
	gs.turnPaused = false
	gs.pausedRest = 0
}

func (gs *Session) timeLeft() time.Duration {
	rest := gs.turnTotal - timerNow().Sub(gs.timerStartedAt)
	if rest < 0 {
		rest = 0
	}
	return rest
}

func (gs *Session) stopTurnTimer() {
	stopTimer(gs.timer)
	gs.timer = nil
	gs.turnPaused = false
	gs.pausedRest = 0
}

// --- 内部辅助 ---

func (gs *Session) playerIdx(id string) int {
	for i, p := range gs.players {
		if p.ID == id {
			return i
		}
	}
	return -1
}

func (gs *Session) isCurrentTurnPlayer(idx int) bool {
	return gs.phase == PhasePlaying && gs.currentPlayer == idx
}

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
