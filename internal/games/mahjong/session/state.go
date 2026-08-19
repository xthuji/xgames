package session

import (
	"errors"
	"time"

	"xgames/internal/games/mahjong/msg"
	"xgames/internal/games/mahjong/rule"
	"xgames/internal/platform/protocol"
)

// 机器人/挂机的出牌兜底超时（需略长于控制器主动出牌延迟 2~2.5s，控制器胜利则正常流转，否则由本兜底代打）
const afkActDelay = 3 * time.Second

func (gs *Session) handleAfk(playerID string, afk bool) error {
	if gs.phase == PhaseInit || gs.phase == PhaseEnded {
		return ErrGameNotStart
	}
	idx := gs.playerIdx(playerID)
	if idx == -1 {
		return ErrNotYourTurn
	}
	player := gs.players[idx]
	if player.IsBot || player.Afk == afk {
		return nil
	}

	player.Afk = afk
	gs.broadcastAfk(player, afk)
	gs.logger.Info("挂机状态变更", "room", gs.roomCode, "player", player.Name, "afk", afk)

	// 关闭托管：取消其挂起中的自动动作，局面转回真人交互（见 docs/architecture.md §10.3.3）
	gs.cancelPendingAutoFor(idx)

	if afk && gs.waitingForClaim && gs.claimPlayer == idx {
		// 托管开启时若正等待该玩家碰/杠/胡决策，立即按机器人策略代打
		gs.resolvePendingClaimForAfk(idx)
	}

	if gs.isCurrentTurnPlayer(idx) && gs.timer != nil {
		if afk {
			gs.startTurnTimer(afkActDelay)
		} else {
			gs.startTurnTimer(gs.timeouts.Turn)
		}
	}
	return nil
}

// resolvePendingClaimForAfk 托管开启时自动处理等待中的碰/杠/胡询问（机器人策略代打）
func (gs *Session) resolvePendingClaimForAfk(idx int) {
	gs.claimSeq++ // 使挂起的询问定时器失效

	if gs.claimIsSelfDraw {
		// 自摸胡：托管自动胡
		gs.waitingForClaim = false
		gs.claimIsSelfDraw = false
		gs.executeWin(idx, true)
		return
	}

	gs.waitingForClaim = false
	tile := gs.lastDiscard
	fromIdx := gs.lastDiscardFrom

	if rule.CanWinFromDiscardWithMelds(gs.hands[idx], tile, len(gs.melds[idx])) {
		gs.executeWin(idx, false)
		return
	}
	if rule.CanKongFromDiscard(gs.hands[idx], tile) && gs.botDecideKong(idx, tile) {
		gs.executeKong(idx, tile, fromIdx, false)
		return
	}
	if rule.CanPong(gs.hands[idx], tile) && gs.botDecidePong(idx, tile) {
		gs.executePong(idx, tile, fromIdx)
		return
	}

	// 无可执行动作，继续询问后续玩家
	gs.resolveClaimsAfter(fromIdx, idx)
}

func (gs *Session) broadcastAfk(player *Player, afk bool) {
	gs.broadcast(msg.MsgMjAfkChanged, msg.MjAfkChangedPayload{
		PlayerID: player.ID,
		Afk:      afk,
	})
}

func (gs *Session) enterAfkOnTimeout(idx int) {
	p := gs.players[idx]
	if p.IsBot || p.Afk || p.IsOffline {
		return
	}
	p.Afk = true
	gs.logger.Info("回合超时，自动转为挂机", "room", gs.roomCode, "player", p.Name)
	gs.broadcastAfk(p, true)
}

func (gs *Session) exitAfkOnAction(playerID string) {
	idx := gs.playerIdx(playerID)
	if idx == -1 {
		return
	}
	p := gs.players[idx]
	if p.IsBot || !p.Afk {
		return
	}
	p.Afk = false
	gs.logger.Info("主动行动，取消挂机", "room", gs.roomCode, "player", p.Name)
	gs.broadcastAfk(p, false)
	// 主动行动即回手动：取消其挂起中的自动动作（本次动作随后在对应 handler 中正常处理）
	gs.cancelPendingAutoFor(idx)
}

// --- 离线 ---

func (gs *Session) handleOffline(playerID string) {
	idx := gs.playerIdx(playerID)
	if idx == -1 {
		return
	}
	gs.players[idx].IsOffline = true

	if !gs.isCurrentTurnPlayer(idx) || gs.timer == nil {
		return
	}

	gs.pausedRest = gs.timeLeft()
	stopTimer(gs.timer)
	gs.timer = nil
	gs.turnPaused = true

	stopTimer(gs.offlineTimer)
	gs.offlineIdx = idx
	gs.offlineTimer = waitAfter(gs.timeouts.OfflineWait)

	gs.logger.Info("玩家离线，暂停计时", "room", gs.roomCode,
		"player", gs.players[idx].Name, "remaining", gs.pausedRest)
}

func (gs *Session) handleOnline(playerID string) {
	idx := gs.playerIdx(playerID)
	if idx == -1 {
		return
	}
	gs.players[idx].IsOffline = false

	if p := gs.players[idx]; !p.IsBot && p.Afk {
		p.Afk = false
		gs.broadcastAfk(p, false)
	}
	// 重连自动解除托管（绕过 handleAfk）：同样取消挂起中的自动动作，转回真人交互
	gs.cancelPendingAutoFor(idx)

	if gs.offlineTimer != nil && gs.offlineIdx == idx {
		stopTimer(gs.offlineTimer)
		gs.offlineTimer = nil
	}

	if !gs.turnPaused || !gs.isCurrentTurnPlayer(idx) {
		return
	}

	rest := gs.pausedRest
	gs.turnPaused = false
	gs.pausedRest = 0
	if rest <= 0 {
		gs.handleTurnTimeout()
		return
	}

	gs.timerStartedAt = timerNow()
	gs.turnTotal = rest
	stopTimer(gs.timer)
	gs.timer = waitAfter(rest)
	gs.logger.Info("玩家重连，恢复计时", "room", gs.roomCode,
		"player", gs.players[idx].Name, "remaining", rest)
}

func (gs *Session) handleOfflineTimeout() {
	idx := gs.offlineIdx
	if idx < 0 || idx >= len(gs.players) {
		return
	}
	player := gs.players[idx]
	if !player.IsOffline || !gs.isCurrentTurnPlayer(idx) {
		return
	}

	gs.logger.Info("玩家离线超时，自动托管", "room", gs.roomCode, "player", player.Name)

	if !player.Afk {
		player.Afk = true
		gs.broadcastAfk(player, true)
	}

	gs.autoplayFor(idx)
}

func (gs *Session) isCurrentTurnPlayer(idx int) bool {
	return gs.phase == PhasePlaying && gs.currentTurn == idx
}

// --- 状态 DTO ---

func (gs *Session) gameStateFor(playerID string) *msg.MjGameStateDTO {
	phase := "playing"
	if gs.phase == PhaseEnded {
		phase = "ended"
	}

	myIdx := gs.playerIdx(playerID)

	players := make([]msg.MjPlayerHandDTO, len(gs.players))
	for i, p := range gs.players {
		dto := msg.MjPlayerHandDTO{
			PlayerID:   p.ID,
			PlayerName: p.Name,
			Seat:       p.Seat,
			IsBot:      p.IsBot,
			Online:     !p.IsOffline,
			Afk:        p.Afk,
			HandCount:  len(gs.hands[i]),
			IsDealer:   i == gs.dealer,
			Score:      gs.scoreOf(i),
		}
		if i == myIdx {
			hand := make([]int, len(gs.hands[i]))
			copy(hand, gs.hands[i])
			sortHand(hand)
			dto.Hand = hand
		}
		for _, m := range gs.melds[i] {
			dto.Melds = append(dto.Melds, msg.MjMeldDTO{
				Type: int(m.Type),
				Tile: m.Tile,
				From: m.From,
			})
		}
		players[i] = dto
	}

	currentTurn := ""
	if gs.phase == PhasePlaying {
		currentTurn = gs.players[gs.currentTurn].ID
	}

	pools := make([][]int, len(gs.players))
	for i, pool := range gs.discardPools {
		pools[i] = append([]int(nil), pool...)
	}

	return &msg.MjGameStateDTO{
		Phase:        phase,
		Players:      players,
		CurrentTurn:  currentTurn,
		MyIndex:      myIdx,
		Dealer:       gs.dealer,
		WallRemain:   len(gs.wall),
		MeldNumber:   gs.meldNumber,
		DiscardPools: pools,
	}
}

// scoreOf 返回指定座位玩家的平台积分（未注入时为 0）
func (gs *Session) scoreOf(idx int) int {
	if idx < 0 || idx >= len(gs.scores) {
		return 0
	}
	return gs.scores[idx]
}

// --- 快照 ---

type Snapshot struct {
	RoomCode     string `json:"room_code"`
	Phase        string `json:"phase"`
	Players      []SnapshotPlayer `json:"players"`
	Wall         []int  `json:"wall"`
	Dealer       int    `json:"dealer"`
	CurrentTurn  int    `json:"current_turn"`
	MeldNumber   int    `json:"meld_number"`
	Hands        [][]int `json:"hands"`
	Melds        [][]rule.Meld `json:"melds"`
	DiscardPools [][]int `json:"discard_pools"`
	TimerRemaining int64 `json:"timer_remaining_ms"`
	TurnPaused   bool   `json:"turn_paused"`
	Scores       []int  `json:"scores,omitempty"` // 玩家平台积分（与 Players 索引对齐）
	TenpaiRounds []int  `json:"tenpai_rounds,omitempty"` // 每人听牌持续轮数（与 Players 索引对齐）
}

type SnapshotPlayer struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Seat      int    `json:"seat"`
	IsBot     bool   `json:"is_bot"`
	IsOffline bool   `json:"is_offline"`
	Afk       bool   `json:"afk,omitempty"`
}

func (gs *Session) snapshot() *Snapshot {
	phase := "playing"
	if gs.phase != PhasePlaying {
		phase = ""
	}

	players := make([]SnapshotPlayer, len(gs.players))
	for i, p := range gs.players {
		players[i] = SnapshotPlayer{
			ID: p.ID, Name: p.Name, Seat: p.Seat,
			IsBot: p.IsBot, IsOffline: p.IsOffline, Afk: p.Afk,
		}
	}

	var remaining int64
	switch {
	case gs.turnPaused:
		remaining = gs.pausedRest.Milliseconds()
	case gs.timer != nil:
		remaining = gs.timeLeft().Milliseconds()
	}

	hands := make([][]int, len(gs.hands))
	for i, h := range gs.hands {
		hands[i] = append([]int(nil), h...)
	}
	melds := make([][]rule.Meld, len(gs.melds))
	for i, m := range gs.melds {
		melds[i] = append([]rule.Meld(nil), m...)
	}
	pools := make([][]int, len(gs.discardPools))
	for i, p := range gs.discardPools {
		pools[i] = append([]int(nil), p...)
	}

	return &Snapshot{
		RoomCode:       gs.roomCode,
		Phase:          phase,
		Players:        players,
		Wall:           append([]int(nil), gs.wall...),
		Dealer:         gs.dealer,
		CurrentTurn:    gs.currentTurn,
		MeldNumber:     gs.meldNumber,
		Hands:          hands,
		Melds:          melds,
		DiscardPools:   pools,
		TimerRemaining: remaining,
		TurnPaused:     gs.turnPaused,
		Scores:         append([]int(nil), gs.scores...),
		TenpaiRounds:   append([]int(nil), gs.tenpaiRounds...),
	}
}

func RestoreFromSnapshot(snap *Snapshot, bc Broadcaster, sink ResultSink, to Timeouts) (*Session, error) {
	if snap == nil || snap.Phase == "" {
		return nil, errors.New("快照为空或不可恢复")
	}
	if len(snap.Players) < 2 {
		return nil, errors.New("快照玩家数不足")
	}

	players := make([]*Player, len(snap.Players))
	for i, sp := range snap.Players {
		players[i] = &Player{
			ID: sp.ID, Name: sp.Name, Seat: sp.Seat,
			IsBot: sp.IsBot, IsOffline: sp.IsOffline, Afk: sp.Afk,
		}
	}

	gs := New(snap.RoomCode, players, bc, sink, to)
	gs.restored = snap
	return gs, nil
}

func (gs *Session) applySnapshot(snap *Snapshot) {
	// 恢复局按恢复时刻重新计时，避免报告时长从零值时刻起算出现异常值
	gs.gameStartTime = time.Now()
	gs.phase = PhasePlaying
	gs.wall = append([]int(nil), snap.Wall...)
	gs.dealer = snap.Dealer
	gs.currentTurn = snap.CurrentTurn
	gs.meldNumber = snap.MeldNumber
	gs.hands = make([][]int, len(snap.Hands))
	for i, h := range snap.Hands {
		gs.hands[i] = append([]int(nil), h...)
	}
	gs.melds = make([][]rule.Meld, len(snap.Melds))
	for i, m := range snap.Melds {
		gs.melds[i] = append([]rule.Meld(nil), m...)
	}
	gs.discardPools = make([][]int, len(snap.DiscardPools))
	for i, p := range snap.DiscardPools {
		gs.discardPools[i] = append([]int(nil), p...)
	}
	if len(snap.TenpaiRounds) == len(gs.players) {
		gs.tenpaiRounds = append([]int(nil), snap.TenpaiRounds...)
	} else {
		gs.tenpaiRounds = make([]int, len(gs.players))
	}

	// 通知各玩家开局
	for i, p := range gs.players {
		gs.sendTo(p.ID, protocol.MsgGameStart, protocol.GameStartPayload{
			Players: gs.playerInfos(),
		})
		gs.sendHandTo(i)
	}

	gs.broadcast(msg.MsgMjTurn, msg.MjTurnPayload{
		PlayerID:   gs.players[gs.currentTurn].ID,
		Timeout:    int(gs.turnDelayFor(gs.currentTurn).Seconds()),
		MeldNumber: gs.meldNumber,
	})

	if snap.TimerRemaining > 0 && gs.timer != nil {
		rest := time.Duration(snap.TimerRemaining) * time.Millisecond
		gs.timerStartedAt = timerNow()
		gs.turnTotal = rest
		stopTimer(gs.timer)
		gs.timer = waitAfter(rest)
	}
}
