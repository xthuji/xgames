package bot

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"xgames/internal/games/chess/msg"
	"xgames/internal/games/chess/rule"
	"xgames/internal/platform/protocol"
)

// Controller 中国象棋机器人控制器：消息驱动。
//
// Room 把对局广播转发给本控制器；控制器跟踪每局棋盘与座位，
// 在机器人回合延迟后调用 DecisionEngine 决策，并通过
// Responder 提交走子动作。
type Controller struct {
	engine DecisionEngine
	logger *slog.Logger

	mu    sync.Mutex
	games map[string]*gameTrack

	delay func(board []int, camp int) time.Duration // 拟人化思考延迟（按局面复杂度动态调整）
}

// NewController 创建机器人控制器
func NewController(engine DecisionEngine, logger *slog.Logger) *Controller {
	return &Controller{
		engine: engine,
		logger: logger,
		games:  make(map[string]*gameTrack),
		delay: humanDelay,
	}
}

// Engine 暴露决策引擎
func (c *Controller) Engine() DecisionEngine { return c.engine }

// SetDelay 覆盖决策延迟（测试用）
func (c *Controller) SetDelay(fn func(board []int, camp int) time.Duration) { c.delay = fn }

type gameTrack struct {
	bots       map[string]string  // botID → 名称
	campOf     map[string]int     // playerID → 阵营（1=红, 2=黑）
	board      []int              // 棋盘镜像
	difficulty DifficultyConfig   // 机器人难度（房间选项 options["difficulty"]）

	// 判和感知上下文（与引擎 zobristHash 口径一致，含执子方）
	history       []uint64 // 实战局面哈希序列（含初始局面）
	halfmoveClock int      // 连续未吃子 ply 数（吃子归零）
}

// OnBroadcast 对局广播消息
func (c *Controller) OnBroadcast(roomCode string, m protocol.Message, resp BotResponder) {
	switch m.Type {
	case protocol.MsgGameStart:
		p, err := protocol.ParsePayload[protocol.GameStartPayload](m)
		if err != nil {
			return
		}
		c.onGameStart(roomCode, p)
	case msg.MsgCcTurn:
		p, err := protocol.ParsePayload[msg.CcTurnPayload](m)
		if err != nil {
			return
		}
		c.onTurn(roomCode, p, resp)
	case msg.MsgCcMoveMade:
		p, err := protocol.ParsePayload[msg.CcMoveMadePayload](m)
		if err != nil {
			return
		}
		c.onMoveMade(roomCode, p)
	case protocol.MsgGameOver:
		p, err := protocol.ParsePayload[protocol.GameOverPayload](m)
		if err == nil {
			c.onGameOver(roomCode, p)
		} else {
			c.mu.Lock()
			delete(c.games, roomCode)
			c.mu.Unlock()
		}
	}
}

// OnTargeted 定向消息：中国象棋无私有定向消息
func (c *Controller) OnTargeted(string, string, protocol.Message, BotResponder) {}

func (c *Controller) onGameStart(roomCode string, p protocol.GameStartPayload) {
	board := rule.NewBoard()
	gt := &gameTrack{
		bots:   make(map[string]string),
		campOf: make(map[string]int),
		board:  board,
		// 难度链路：房间选项 options["difficulty"] → 难度配置（空/未知回退 normal）
		difficulty: DifficultyFor(p.Options["difficulty"]),
		// 判和感知：初始局面哈希（红方先行）计入重复计数
		history: []uint64{zobristHash(board, rule.CampRed)},
	}

	for _, pl := range p.Players {
		if pl.Seat < 0 || pl.Seat > 1 {
			continue
		}
		gt.campOf[pl.ID] = rule.CampOfSeat(pl.Seat)
		if pl.IsBot {
			gt.bots[pl.ID] = pl.Name
		}
	}
	c.mu.Lock()
	c.games[roomCode] = gt
	c.mu.Unlock()
}

func (c *Controller) onTurn(roomCode string, p msg.CcTurnPayload, resp BotResponder) {
	c.mu.Lock()
	gt := c.games[roomCode]
	if gt == nil {
		c.mu.Unlock()
		return
	}
	name, isBot := gt.bots[p.PlayerID]
	if !isBot {
		c.mu.Unlock()
		return
	}
	board := rule.CopyBoard(gt.board)
	camp := gt.campOf[p.PlayerID]
	difficulty := gt.difficulty
	// 判和上下文：实战历史 + 限着计数快照（决策在锁外异步执行）
	gctx := GameContext{
		History:       append([]uint64(nil), gt.history...),
		HalfmoveClock: gt.halfmoveClock,
	}
	c.mu.Unlock()

	time.AfterFunc(c.delay(board, camp), func() {
		// 决策：优先走上下文接口（携带判和感知 + 难度 + 决策数据），
		// 依次回退详情接口 / 基础接口
		var fr, fc, tr, tc int
		if gcer, ok := c.engine.(GameContexter); ok {
			detail := gcer.DecideMoveWithContext(context.Background(), name, board, camp, difficulty, gctx)
			fr, fc, tr, tc = detail.FromRow, detail.FromCol, detail.ToRow, detail.ToCol
		} else if dd, ok := c.engine.(DecisionDetailer); ok {
			detail := dd.DecideMoveDetailed(context.Background(), name, board, camp, difficulty)
			fr, fc, tr, tc = detail.FromRow, detail.FromCol, detail.ToRow, detail.ToCol
		} else {
			fr, fc, tr, tc = c.engine.DecideMove(context.Background(), name, board, camp)
		}

		if fr < 0 {
			return
		}
		action := protocol.NewMessage(msg.MsgCcMove, msg.CcMovePayload{
			FromRow: fr, FromCol: fc, ToRow: tr, ToCol: tc,
		})
		if err := resp.SubmitBotAction(p.PlayerID, action); err != nil {
			c.logger.Warn("机器人走子失败", "room", roomCode, "bot", name, "err", err)
		}
	})
}

func (c *Controller) onMoveMade(roomCode string, p msg.CcMoveMadePayload) {
	c.withTrack(roomCode, func(gt *gameTrack) {
		piece := gt.board[rule.Idx(p.FromRow, p.FromCol)]
		gt.board[rule.Idx(p.FromRow, p.FromCol)] = rule.Empty
		gt.board[rule.Idx(p.ToRow, p.ToCol)] = piece

		// 判和上下文更新：局面哈希入重复计数；吃子重置限着计数
		camp := rule.CampOf(piece)
		nextCamp := rule.CampRed + rule.CampBlack - camp
		gt.history = append(gt.history, zobristHash(gt.board, nextCamp))
		if p.Captured != rule.Empty {
			gt.halfmoveClock = 0
		} else {
			gt.halfmoveClock++
		}
	})
}

// onGameOver 对局结束，清理本局跟踪状态。
// 复盘报告改由 session endGame 以真人玩家为中心生成（见 replay.SaveUserReport）。
func (c *Controller) onGameOver(roomCode string, _ protocol.GameOverPayload) {
	c.mu.Lock()
	if gt := c.games[roomCode]; gt != nil {
		delete(c.games, roomCode)
	}
	c.mu.Unlock()
}

func (c *Controller) withTrack(roomCode string, fn func(gt *gameTrack)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gt := c.games[roomCode]; gt != nil {
		fn(gt)
	}
}

// EvaluateDrawOffer 机器人和棋评估：子力不足/接近限着/劣势时接受，优势时拒绝
func (c *Controller) EvaluateDrawOffer(roomCode string, board []int, camp int) bool {
	// 子力不足：双方均无取胜可能
	if rule.InsufficientMaterial(board) {
		return true
	}

	c.mu.Lock()
	gt := c.games[roomCode]
	if gt == nil {
		c.mu.Unlock()
		return false
	}
	halfmoveClock := gt.halfmoveClock
	c.mu.Unlock()

	// 接近自然限着（26 ply 的 70% = 18 ply）时接受
	if halfmoveClock >= 18 {
		return true
	}

	// 局面评估：劣势或均势接受（分数绝对值 < 200），优势拒绝
	score := evaluate(board, camp)
	if score < 200 {
		return true
	}
	return false
}

// humanDelay 拟人化思考延迟：按局面复杂度动态调整（Phase 2 思考时间拟人化）。
// 简单局面（无吃子且不被将军）：0.5~1.5s
// 中等局面（有吃子或被将军）：1.5~2.5s
// 复杂局面（多处吃子选择）：2.5~4s
func humanDelay(board []int, camp int) time.Duration {
	captures := 0
	for _, m := range rule.AllLegalMoves(board, camp) {
		if m.Captured != rule.Empty {
			captures++
			if captures >= 3 {
				break
			}
		}
	}
	inCheck := rule.IsInCheck(board, camp)
	switch {
	case captures >= 3:
		return time.Duration(2500+rand.IntN(1500)) * time.Millisecond // 复杂：2.5~4s
	case captures > 0 || inCheck:
		return time.Duration(1500+rand.IntN(1000)) * time.Millisecond // 中等：1.5~2.5s
	default:
		return time.Duration(500+rand.IntN(1000)) * time.Millisecond // 简单：0.5~1.5s
	}
}
