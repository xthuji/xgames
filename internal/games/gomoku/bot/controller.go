package bot

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"xgames/internal/games/gomoku/msg"
	"xgames/internal/games/gomoku/rule"
	"xgames/internal/platform/protocol"
)

// Controller 五子棋机器人控制器：消息驱动（与斗地主 bot.Controller 同构）。
//
// Room 把对局广播转发给本控制器；控制器跟踪每局棋盘与双方座位，
// 在机器人回合延迟 1~3s 后调用 DecisionEngine 决策，并通过
// Responder（即 GameSession）提交落子动作。
type Controller struct {
	engine         DecisionEngine
	logger         *slog.Logger
	replayEnabled  bool // 是否启用复盘记录

	mu    sync.Mutex
	games map[string]*gameTrack

	delay func() time.Duration // 拟人思考延迟（测试可覆盖）
}

// NewController 创建机器人控制器
func NewController(engine DecisionEngine, logger *slog.Logger) *Controller {
	return &Controller{
		engine:        engine,
		logger:        logger,
		games:         make(map[string]*gameTrack),
		delay:         thinkDelay,
		replayEnabled: true, // 默认启用复盘
	}
}

// EnableReplay 启用复盘记录功能
func (c *Controller) EnableReplay() {
	c.replayEnabled = true
}

// Engine 暴露决策引擎（供真人挂机托管复用同一套规则引擎）
func (c *Controller) Engine() DecisionEngine { return c.engine }

// SetDelay 覆盖决策延迟（测试用）
func (c *Controller) SetDelay(fn func() time.Duration) { c.delay = fn }

// thinkDelay 默认拟人延迟：随机 1~3s
func thinkDelay() time.Duration {
	return time.Duration(1000+rand.IntN(2000)) * time.Millisecond
}

// gameTrack 单局跟踪状态（Controller.mu 保护）
type gameTrack struct {
	bots       map[string]string    // botID → 名称
	seatOf     map[string]int       // playerID → 座位（0=黑 1=白）
	board      []int                // 棋盘镜像（rule.Size*rule.Size）
	replayAdapter *GomokuReplayAdapter // P1: 复盘适配器
	round      int                  // P1: 当前回合数
	difficulty DifficultyConfig      // 机器人难度（房间选项 options["difficulty"]）
}

// --- Room 转发入口 ---

// OnBroadcast 对局广播消息（game_start/gk_turn/gk_move_made/game_over）
func (c *Controller) OnBroadcast(roomCode string, m protocol.Message, resp BotResponder) {
	switch m.Type {
	case protocol.MsgGameStart:
		p, err := protocol.ParsePayload[protocol.GameStartPayload](m)
		if err != nil {
			return
		}
		c.onGameStart(roomCode, p)
	case msg.MsgGkTurn:
		p, err := protocol.ParsePayload[msg.GkTurnPayload](m)
		if err != nil {
			return
		}
		c.onTurn(roomCode, p, resp)
	case msg.MsgGkMoveMade:
		p, err := protocol.ParsePayload[msg.GkMoveMadePayload](m)
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

// OnTargeted 定向消息：五子棋无私有定向消息
func (c *Controller) OnTargeted(string, string, protocol.Message, BotResponder) {}

// --- 消息处理 ---

func (c *Controller) onGameStart(roomCode string, p protocol.GameStartPayload) {
	gt := &gameTrack{
		bots:   make(map[string]string),
		seatOf: make(map[string]int),
		board:  rule.NewBoard(),
		round:  1,
		// 难度链路：房间选项 options["difficulty"] → 难度配置（空/未知回退 normal）
		difficulty: DifficultyFor(p.Options["difficulty"]),
	}
	
	// P1: 初始化复盘适配器
	if c.replayEnabled {
		gt.replayAdapter = NewGomokuReplayAdapter(roomCode)
	}
	
	for _, pl := range p.Players {
		if pl.Seat < 0 || pl.Seat > 1 {
			continue
		}
		gt.seatOf[pl.ID] = pl.Seat
		if pl.IsBot {
			gt.bots[pl.ID] = pl.Name
		}
	}
	c.mu.Lock()
	c.games[roomCode] = gt
	c.mu.Unlock()
}

func (c *Controller) onTurn(roomCode string, p msg.GkTurnPayload, resp BotResponder) {
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
	board := append([]int(nil), gt.board...)
	color := rule.ColorOfSeat(gt.seatOf[p.PlayerID])
	isBlack := color == rule.Black

	// P1: 保存复盘适配器引用与难度配置
	replayAdapter := gt.replayAdapter
	difficulty := gt.difficulty
	round := gt.round
	c.mu.Unlock()

	time.AfterFunc(c.delay(), func() {
		// 决策：优先走详情接口（携带难度 + 返回真实决策数据），
		// 未实现详情接口的引擎桩回退 DecideMove
		var detail DecisionDetail
		if dd, ok := c.engine.(DecisionDetailer); ok {
			detail = dd.DecideMoveDetailed(context.Background(), name, board, color, difficulty)
		} else {
			row, col := c.engine.DecideMove(context.Background(), name, board, color)
			detail = DecisionDetail{Row: row, Col: col, Difficulty: difficulty.Name}
		}

		// P1: 记录决策日志（真实评估分/候选/深度/杀棋命中，不再占位）
		if replayAdapter != nil {
			// 转换棋盘为二维数组
			var boardArray [15][15]int
			for i := 0; i < 15; i++ {
				for j := 0; j < 15; j++ {
					boardArray[i][j] = board[rule.Idx(i, j)]
				}
			}

			moveInfo := GomokuMoveInfo{
				Row:        detail.Row,
				Col:        detail.Col,
				Threats:    detail.Threats,
				EvalScore:  detail.Score,
				IsWinningMove: detail.Score >= scoreFive,
			}

			reasoning := generateGomokuReasoning(moveInfo, isBlack, round, detail.VCFFound, detail.VCTFound)

			// 真实 Top-N 候选（含所选着法）
			candidates := make([]GomokuCandidate, 0, len(detail.TopMoves))
			for _, tm := range detail.TopMoves {
				candidates = append(candidates, GomokuCandidate{
					Row:   tm.Row,
					Col:   tm.Col,
					Score: tm.Score,
				})
			}

			replayAdapter.RecordDecision(
				round,
				name,
				isBlack,
				boardArray,
				moveInfo,
				candidates,
				reasoning,
				detail.Score,
				detail.Depth,
				detail.VCFFound,
				detail.VCTFound,
				detail.Difficulty,
				detail.BookHit,
			)

			// 更新回合数
			c.mu.Lock()
			if gt := c.games[roomCode]; gt != nil {
				gt.round++
			}
			c.mu.Unlock()
		}

		action := protocol.NewMessage(msg.MsgGkMove, msg.GkMovePayload{Row: detail.Row, Col: detail.Col})
		if err := resp.SubmitBotAction(p.PlayerID, action); err != nil {
			c.logger.Warn("机器人落子失败", "room", roomCode, "bot", name, "err", err)
		}
	})
}

func (c *Controller) onMoveMade(roomCode string, p msg.GkMoveMadePayload) {
	c.withTrack(roomCode, func(gt *gameTrack) {
		if rule.InBounds(p.Row, p.Col) {
			gt.board[rule.Idx(p.Row, p.Col)] = p.Color
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

// generateGomokuReasoning 生成五子棋决策推理说明
func generateGomokuReasoning(move GomokuMoveInfo, isBlack bool, round int, vcfFound, vctFound bool) string {
	var sb strings.Builder
	
	// VCF/VCT 提示
	if vcfFound {
		sb.WriteString("- 发现连续冲四（VCF）必胜路径\n")
	}
	if vctFound {
		sb.WriteString("- 发现连续活三（VCT）必胜路径\n")
	}
	
	// 威胁分析
	if len(move.Threats) > 0 {
		sb.WriteString(fmt.Sprintf("- 形成威胁: %s\n", strings.Join(move.Threats, ", ")))
	}
	
	// 先手/后手分析
	if isBlack {
		sb.WriteString("- 黑棋先手，保持攻势\n")
	} else {
		sb.WriteString("- 白棋后手，注意防守反击\n")
	}
	
	// 阶段分析
	if round <= 5 {
		sb.WriteString("- 开局阶段，占据中心要地\n")
	} else if round <= 15 {
		sb.WriteString("- 中局阶段，构建进攻态势\n")
	} else {
		sb.WriteString("- 残局阶段，精确计算胜负\n")
	}
	
	// 位置分析
	if isCenterPosition(move.Row, move.Col) {
		sb.WriteString("- 靠近中心，战略要地\n")
	}
	
	return sb.String()
}

func isCenterPosition(row, col int) bool {
	// 中心区域：6-8 行，6-8 列
	return row >= 6 && row <= 8 && col >= 6 && col <= 8
}

// withTrack 在锁内修改指定房间的跟踪状态
func (c *Controller) withTrack(roomCode string, fn func(gt *gameTrack)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gt := c.games[roomCode]; gt != nil {
		fn(gt)
	}
}

// EvaluateDrawOffer 机器人和棋评估：棋盘僵局或满盘时接受，否则拒绝
func (c *Controller) EvaluateDrawOffer(roomCode string, board []int) bool {
	if rule.IsDead(board) || rule.IsFull(board) {
		return true
	}
	return false
}
