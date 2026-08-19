// Package session 实现 GameSession Actor：每局对局一个 goroutine，
// 所有对局状态（手牌、底牌、回合、倍数、计时器）只在 Actor 内读写，无锁。
//
// 规则：叫分（1/2/3 分）定地主 + 农民加倍，状态机骨架、定时器、
// 离线暂停/恢复为 select + channel 事件驱动（见技术设计 6.3）。
package session

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"xgames/internal/games"
	"xgames/internal/games/bot/replay"
	"xgames/internal/games/ddz/bot"
	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/msg"
	"xgames/internal/games/ddz/rule"
	"xgames/internal/platform/protocol"
)

// Phase 对局阶段
type Phase int

const (
	PhaseInit Phase = iota
	PhaseBidding
	PhaseDoubling
	PhasePlaying
	PhaseEnded
)

// 领域错误（经 WS 层映射为 protocol 错误码）
var (
	ErrGameNotStart = errors.New("游戏尚未开始")
	ErrNotYourTurn  = errors.New("还没轮到您")
	ErrInvalidBid   = errors.New("无效的叫分")
	ErrInvalidCards = errors.New("无效的牌")
	ErrCannotBeat   = errors.New("打不过上家")
	ErrMustPlay     = errors.New("新一轮必须出牌")
	ErrSessionDown  = errors.New("对局已结束")
)

// maxRedeals 最大流局次数；达到后随机强制指定地主，避免无限流局
const maxRedeals = 3

// 时间源（包级变量，测试可注入加速时钟）
var (
	waitAfter = func(d time.Duration) *time.Timer { return time.NewTimer(d) }
	timerNow  = time.Now
)

// Player 对局中的玩家
type Player struct {
	ID         string
	Name       string
	Seat       int
	IsBot      bool
	Hand       []card.Card
	IsLandlord bool
	IsOffline  bool
	Afk        bool // 挂机（显式托管）：仅挂机/离线时机器人才可代为决策
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

type bidEvent struct {
	playerID string
	score    int
	reply    chan<- error
}

type doubleEvent struct {
	playerID string
	double   bool
	reply    chan<- error
}

type playEvent struct {
	playerID string
	cards    []protocol.CardInfo
	reply    chan<- error
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

type stateReq struct {
	playerID string
	reply    chan<- *msg.GameStateDTO
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
	engine      bot.DecisionEngine // 机器人决策引擎（真人挂机托管用；nil 回退提示系统）

	events   chan any
	shutdown chan any // 请求-响应类事件走无缓冲队列，避免与慢发送者互锁
	done     chan struct{}

	// ---- 以下状态仅在 Actor goroutine 内读写（无锁） ----

	phase Phase
	ended bool // 对局已结算，Actor 即将退出

	bottomCards []card.Card

	// 叫分 / 加倍
	currentBidder int // 当前叫分 / 加倍的玩家索引
	highBid       int // 当前最高叫分（0 = 尚无人叫）
	highBidder    int // 叫出最高分的玩家索引，-1 表示尚无
	bidActed      int // 叫分阶段已表态人数（最多 3 人次后定地主或流局）
	doubleActed   int // 加倍阶段已表态农民数（最多 2 人）
	bidMultiplier int // 底倍（叫分分数）及加倍后的倍数
	redealCount   int // 已发生的流局次数

	// 倍数相关（出牌阶段累计）
	bombCount     int // 炸弹+王炸数量，每个翻一倍
	landlordPlays int // 地主出牌次数（反春天判断）
	farmerPlays   int // 农民出牌次数（春天判断）

	// 出牌
	currentPlayer     int
	lastPlayedHand    rule.ParsedHand
	lastPlayedCards   []card.Card // 供快照与 DTO 还原
	lastPlayerIdx     int
	consecutivePasses int
	beatStreaks       [3]int // 各座位连续让牌/无法管住对手的次数（托管拆牌重计划触发依据）

	// 出牌记录（挂机托管构造决策上下文用：记牌器与最近两手）
	playedRankCounts map[card.Rank]int // 全场已出各点数累计张数（不含底牌发出前）
	recentPlays      [2]bot.PlayRecord // [0]=最近一手
	oncePlayed       [3]bool           // 各座位本局是否已出过牌（未开张时引擎放弃让牌配合）

	// 复盘系统：真人玩家决策记录（bot 与托管代打不记录）
	playerLogger  *replay.PlayerReplayLogger
	gameStartTime time.Time

	// 定时器（select + channel 实现）
	timerKind      timerKind   // 当前运行的计时器类型
	timer          *time.Timer // 当前回合计时器
	timerStartedAt time.Time   // 计时器启动时刻
	turnTotal      time.Duration // 本轮计时总时长（离线暂停后恢复用）
	turnPaused     bool          // 当前回合计时是否被离线暂停
	pausedRest     time.Duration // 暂停时的剩余时间
	offlineTimer   *time.Timer   // 离线等待计时器
	offlineIdx     int           // 触发离线等待的玩家索引

	// 快照恢复（仅 Run 启动时使用）
	restored         *Snapshot
	restoredLastHand rule.ParsedHand
}

type timerKind int

const (
	timerNone timerKind = iota
	timerBid
	timerPlay
	timerAutoPass // 无牌可出的自动过牌（短计时，不触发转挂机）
)

// New 创建 GameSession Actor（未启动）。players 按座位顺序。
func New(roomCode string, players []*Player, bc Broadcaster, sink ResultSink, to Timeouts) *Session {
	s := &Session{
		roomCode:      roomCode,
		players:       players,
		broadcaster:   bc,
		sink:          sink,
		timeouts:      to,
		logger:        slog.Default(),
		events:        make(chan any, 64),
		shutdown:      make(chan any),
		done:          make(chan struct{}),
		phase:         PhaseInit,
		highBidder:    -1,
		bidMultiplier: 1,

		playedRankCounts: make(map[card.Rank]int),

		// 复盘系统：真人玩家决策收集器
		playerLogger: replay.NewPlayerReplayLogger("ddz", roomCode),
	}

	return s
}

// SetEngine 注入机器人决策引擎（真人挂机托管复用同一套规则引擎）。
// 必须在 Run 之前调用（Room 开局时注入；未启用机器人时为 nil）。
func (gs *Session) SetEngine(e bot.DecisionEngine) { gs.engine = e }

// Run Actor 事件循环（每局一个 goroutine）。启动后立即发牌进入叫分阶段。
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
		gs.startBiddingRound()
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
	case bidEvent:
		gs.exitAfkOnAction(e.playerID) // 真人主动行动自动取消挂机
		e.reply <- gs.handleBid(e.playerID, e.score)
	case doubleEvent:
		gs.exitAfkOnAction(e.playerID)
		e.reply <- gs.handleDouble(e.playerID, e.double)
	case playEvent:
		gs.exitAfkOnAction(e.playerID)
		e.reply <- gs.handlePlayCards(e.playerID, e.cards)
	case passEvent:
		gs.exitAfkOnAction(e.playerID)
		e.reply <- gs.handlePass(e.playerID)
	case afkEvent:
		e.reply <- gs.handleAfk(e.playerID, e.afk)
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

// --- 公开 API（供 WS 层 / 机器人调度调用，全部经事件队列串行化） ---

// Bid 叫分（0 = 不叫，1/2/3 = 叫分）
func (gs *Session) Bid(playerID string, score int) error {
	reply := make(chan error, 1)
	if !gs.call(bidEvent{playerID: playerID, score: score, reply: reply}) {
		return ErrSessionDown
	}
	return <-reply
}

// Double 加倍 / 不加倍（地主确定后的加倍阶段）
func (gs *Session) Double(playerID string, double bool) error {
	reply := make(chan error, 1)
	if !gs.call(doubleEvent{playerID: playerID, double: double, reply: reply}) {
		return ErrSessionDown
	}
	return <-reply
}

// PlayCards 出牌
func (gs *Session) PlayCards(playerID string, cards []protocol.CardInfo) error {
	reply := make(chan error, 1)
	if !gs.call(playEvent{playerID: playerID, cards: cards, reply: reply}) {
		return ErrSessionDown
	}
	return <-reply
}

// Pass 不出
func (gs *Session) Pass(playerID string) error {
	reply := make(chan error, 1)
	if !gs.call(passEvent{playerID: playerID, reply: reply}) {
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
func (gs *Session) GameStateFor(playerID string) *msg.GameStateDTO {
	reply := make(chan *msg.GameStateDTO, 1)
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

// OnMessage 斗地主私有消息入口：bid/double/play_cards/pass/afk
func (gs *Session) OnMessage(playerID string, m protocol.Message) error {
	var err error
	switch m.Type {
	case msg.MsgBid:
		p, perr := protocol.ParsePayload[msg.BidPayload](m)
		if perr != nil {
			return games.NewCodedError(protocol.ErrCodeInvalidMsg, "")
		}
		err = gs.Bid(playerID, p.Score)
	case msg.MsgDouble:
		p, perr := protocol.ParsePayload[msg.DoublePayload](m)
		if perr != nil {
			return games.NewCodedError(protocol.ErrCodeInvalidMsg, "")
		}
		err = gs.Double(playerID, p.Double)
	case msg.MsgPlayCards:
		p, perr := protocol.ParsePayload[msg.PlayCardsPayload](m)
		if perr != nil {
			return games.NewCodedError(protocol.ErrCodeInvalidMsg, "")
		}
		err = gs.PlayCards(playerID, p.Cards)
	case msg.MsgPass:
		err = gs.Pass(playerID)
	case msg.MsgAfk:
		p, perr := protocol.ParsePayload[msg.AfkPayload](m)
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

// toCodedError 领域错误 → 协议错误码（原 WS 层映射逻辑随消息入口迁入游戏内部）
func toCodedError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrNotYourTurn):
		return games.NewCodedError(msg.ErrCodeNotYourTurn, "")
	case errors.Is(err, ErrInvalidBid):
		return games.NewCodedError(protocol.ErrCodeInvalidMsg, err.Error())
	case errors.Is(err, ErrInvalidCards):
		return games.NewCodedError(msg.ErrCodeInvalidCards, "")
	case errors.Is(err, ErrCannotBeat):
		return games.NewCodedError(msg.ErrCodeCannotBeat, "")
	case errors.Is(err, ErrMustPlay):
		return games.NewCodedError(msg.ErrCodeMustPlay, "")
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
func (gs *Session) startTurnTimer(kind timerKind, d time.Duration) {
	gs.timerKind = kind
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
	gs.timerKind = timerNone
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

// isMustPlay 当前出牌玩家是否必须出牌（新一轮开始）
func (gs *Session) isMustPlay() bool {
	return gs.lastPlayerIdx == gs.currentPlayer || gs.lastPlayedHand.IsEmpty()
}

// zeroParsedHand 空的上家出牌记录
func zeroParsedHand() rule.ParsedHand {
	return rule.ParsedHand{}
}

// broadcast 广播便捷方法
func (gs *Session) broadcast(t protocol.MessageType, payload any) {
	gs.broadcaster.Broadcast(newMessage(t, payload))
}

func (gs *Session) sendTo(playerID string, t protocol.MessageType, payload any) {
	gs.broadcaster.SendTo(playerID, newMessage(t, payload))
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
