// Package session 实现麻将 GameSession Actor。
//
// 规则：2-4 人，108 张牌（万/筒/条各 36），标准胡牌（4 面子 + 1 雀头或七对）；
// 支持碰、自摸、点炮；回合超时自动转挂机由机器人代打。
package session

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"xgames/internal/games"
	"xgames/internal/games/bot/replay"
	"xgames/internal/games/mahjong/bot"
	"xgames/internal/games/mahjong/msg"
	"xgames/internal/games/mahjong/rule"
	"xgames/internal/platform/protocol"
)

type Phase int

const (
	PhaseInit Phase = iota
	PhasePlaying
	PhaseEnded
)

var (
	ErrGameNotStart = errors.New("游戏尚未开始")
	ErrNotYourTurn  = errors.New("还没轮到您")
	ErrInvalidTile  = errors.New("打出的牌不合法")
	ErrNoAction     = errors.New("当前没有可执行的动作")
	ErrNotAllowed   = errors.New("不允许此操作")
	ErrSessionDown  = errors.New("对局已结束")
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

type discardEvent struct {
	playerID string
	tile     int
	reply    chan<- error
}

type pongEvent struct {
	playerID string
	tile     int
	reply    chan<- error
}

type kongEvent struct {
	playerID string
	tile     int
	reply    chan<- error
}

type winEvent struct {
	playerID   string
	isSelfDraw bool
	reply      chan<- error
}

type passEvent struct {
	playerID string
	reply    chan<- error
}

type afkEvent struct {
	playerID string
	afk      bool
	reply    chan<- error
}

type offlineEvent struct{ playerID string }
type onlineEvent struct{ playerID string }

// claimTimeoutEvent 碰/杠/胡询问超时事件（seq 用于丢弃已失效询问的过期定时器，
// 避免前一次询问快速结束后，旧定时器误杀新询问导致客户端动作按钮提前消失）
type claimTimeoutEvent struct{ seq int }

type stateReq struct {
	playerID string
	reply    chan<- *msg.MjGameStateDTO
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

	events   chan any
	shutdown chan any
	done     chan struct{}

	// ---- 以下状态仅在 Actor goroutine 内读写 ----

	phase Phase
	ended bool

	wall        []int
	dealer      int // 庄家座位索引
	currentTurn int // 当前出牌玩家索引
	meldNumber  int // 第几巡

	hands        [][]int       // 每人手牌
	melds        [][]rule.Meld // 每人面子
	discardPools [][]int       // 每人弃牌池

	tenpaiRounds []int // 每人听牌持续轮数（听牌保护用，与 players 索引对齐）

	lastDiscard     int  // 最后一张弃牌
	lastDiscardFrom int  // 弃牌者索引
	waitingForClaim bool // 是否等待碰/胡响应
	claimIsSelfDraw bool // 等待的是自摸胡决策（过=继续出牌，而非继续询问）
	claimPlayer     int  // 被询问碰/胡的玩家索引
	claimSeq        int  // 询问序号（每次发起新询问 +1，过期定时器据此失效）

	// 语音节奏预算（松弛画音同步，见 docs/architecture.md §10.3）：托管/bot 自动动作
	// 距上一动作播报不足 minActionGap 时挂起等待。与 waitingForClaim 互斥。
	lastActionAt time.Time          // 最近一次带语音的动作广播时刻（节奏锚点）
	actionGen    int                // 自动动作代次（调度/取消时 +1，过期到期事件据此丢弃）
	pendingAuto  *pendingAutoAction // 挂起中的托管/bot 自动动作（预算等待中）

	// 定时器
	timer          *time.Timer
	timerStartedAt time.Time
	turnTotal      time.Duration
	turnPaused     bool
	pausedRest     time.Duration
	offlineTimer   *time.Timer
	offlineIdx     int

	scores []int // 玩家平台积分（开局时由房间注入，与 players 索引对齐；仅随状态下发）

	// 复盘系统：真人玩家决策记录（bot 与托管代打不记录）
	playerLogger  *replay.PlayerReplayLogger
	gameStartTime time.Time

	restored *Snapshot
}

// New 创建 GameSession Actor
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
		playerLogger: replay.NewPlayerReplayLogger("mahjong", roomCode),
	}
}

// SetEngine 注入机器人决策引擎
func (gs *Session) SetEngine(e bot.DecisionEngine) { gs.engine = e }

// SetScores 注入玩家平台积分（开局时由房间调用；与 players 索引对齐）
func (gs *Session) SetScores(scores []int) { gs.scores = scores }

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
	case discardEvent:
		gs.exitAfkOnAction(e.playerID)
		e.reply <- gs.handleDiscard(e.playerID, e.tile)
	case pongEvent:
		gs.exitAfkOnAction(e.playerID)
		e.reply <- gs.handlePong(e.playerID, e.tile)
	case kongEvent:
		gs.exitAfkOnAction(e.playerID)
		e.reply <- gs.handleKong(e.playerID, e.tile)
	case winEvent:
		gs.exitAfkOnAction(e.playerID)
		e.reply <- gs.handleWin(e.playerID, e.isSelfDraw)
	case passEvent:
		e.reply <- gs.handlePass(e.playerID)
	case afkEvent:
		e.reply <- gs.handleAfk(e.playerID, e.afk)
	case offlineEvent:
		gs.handleOffline(e.playerID)
	case onlineEvent:
		gs.handleOnline(e.playerID)
	case claimTimeoutEvent:
		gs.handleClaimTimeout(e.seq)
	case autoActionEvent:
		gs.firePendingAuto(e.gen)
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

func (gs *Session) Discard(playerID string, tile int) error {
	reply := make(chan error, 1)
	if !gs.call(discardEvent{playerID: playerID, tile: tile, reply: reply}) {
		return ErrSessionDown
	}
	return <-reply
}

func (gs *Session) Pong(playerID string, tile int) error {
	reply := make(chan error, 1)
	if !gs.call(pongEvent{playerID: playerID, tile: tile, reply: reply}) {
		return ErrSessionDown
	}
	return <-reply
}

func (gs *Session) Kong(playerID string, tile int) error {
	reply := make(chan error, 1)
	if !gs.call(kongEvent{playerID: playerID, tile: tile, reply: reply}) {
		return ErrSessionDown
	}
	return <-reply
}

func (gs *Session) Win(playerID string, isSelfDraw bool) error {
	reply := make(chan error, 1)
	if !gs.call(winEvent{playerID: playerID, isSelfDraw: isSelfDraw, reply: reply}) {
		return ErrSessionDown
	}
	return <-reply
}

func (gs *Session) Pass(playerID string) error {
	reply := make(chan error, 1)
	if !gs.call(passEvent{playerID: playerID, reply: reply}) {
		return ErrSessionDown
	}
	return <-reply
}

func (gs *Session) SetAfk(playerID string, afk bool) error {
	reply := make(chan error, 1)
	if !gs.call(afkEvent{playerID: playerID, afk: afk, reply: reply}) {
		return ErrSessionDown
	}
	return <-reply
}

func (gs *Session) PlayerOffline(playerID string) {
	gs.post(offlineEvent{playerID: playerID})
}

func (gs *Session) PlayerOnline(playerID string) {
	gs.post(onlineEvent{playerID: playerID})
}

func (gs *Session) GameStateFor(playerID string) *msg.MjGameStateDTO {
	reply := make(chan *msg.MjGameStateDTO, 1)
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
	case msg.MsgMjDiscard:
		p, perr := protocol.ParsePayload[msg.MjDiscardPayload](m)
		if perr != nil {
			return games.NewCodedError(protocol.ErrCodeInvalidMsg, "")
		}
		err = gs.Discard(playerID, p.Tile)
	case msg.MsgMjKong:
		p, perr := protocol.ParsePayload[msg.MjKongPayload](m)
		if perr != nil {
			return games.NewCodedError(protocol.ErrCodeInvalidMsg, "")
		}
		err = gs.Kong(playerID, p.Tile)
	case msg.MsgMjPong:
		p, perr := protocol.ParsePayload[msg.MjPongPayload](m)
		if perr != nil {
			return games.NewCodedError(protocol.ErrCodeInvalidMsg, "")
		}
		err = gs.Pong(playerID, p.Tile)
	case msg.MsgMjWin:
		p, perr := protocol.ParsePayload[msg.MjWinPayload](m)
		if perr != nil {
			return games.NewCodedError(protocol.ErrCodeInvalidMsg, "")
		}
		err = gs.Win(playerID, p.IsSelfDraw)
	case msg.MsgMjPass:
		_, perr := protocol.ParsePayload[msg.MjPassPayload](m)
		if perr != nil {
			return games.NewCodedError(protocol.ErrCodeInvalidMsg, "")
		}
		err = gs.Pass(playerID)
	case msg.MsgMjAfk:
		p, perr := protocol.ParsePayload[msg.MjAfkPayload](m)
		if perr != nil {
			return games.NewCodedError(protocol.ErrCodeInvalidMsg, "")
		}
		err = gs.SetAfk(playerID, p.Afk)
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

func (gs *Session) Stop() {
	reply := make(chan struct{})
	if !gs.call(stopEvent{reply: reply}) {
		return
	}
	<-reply
}

// --- 内部辅助 ---

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

func (gs *Session) playerIdx(id string) int {
	for i, p := range gs.players {
		if p.ID == id {
			return i
		}
	}
	return -1
}

func (gs *Session) broadcast(t protocol.MessageType, payload any) {
	gs.broadcaster.Broadcast(protocol.NewMessage(t, payload))
}

func (gs *Session) sendTo(playerID string, t protocol.MessageType, payload any) {
	gs.broadcaster.SendTo(playerID, protocol.NewMessage(t, payload))
}

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

func (gs *Session) turnDelayFor(idx int) time.Duration {
	// 机器人或挂机都使用短超时，由会话侧自动代打，避免长时间卡在等待牌局
	if gs.players[idx].IsBot || gs.players[idx].Afk {
		return afkActDelay
	}
	return gs.timeouts.Turn
}

func toCodedError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrNotYourTurn):
		return games.NewCodedError(msg.ErrCodeNotYourTurn, "")
	case errors.Is(err, ErrInvalidTile):
		return games.NewCodedError(msg.ErrCodeInvalidTile, "")
	case errors.Is(err, ErrNoAction):
		return games.NewCodedError(msg.ErrCodeNoAction, "")
	case errors.Is(err, ErrNotAllowed):
		return games.NewCodedError(msg.ErrCodeNotAllowed, "")
	case errors.Is(err, ErrGameNotStart), errors.Is(err, ErrSessionDown):
		return games.NewCodedError(protocol.ErrCodeGameNotStart, "")
	default:
		return games.NewCodedError(protocol.ErrCodeUnknown, err.Error())
	}
}
