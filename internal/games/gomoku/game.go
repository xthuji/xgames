// Package gomoku 五子棋游戏插件：实现 games.Game 描述符，
// 把对局会话（session）、机器人（bot）与私有协议（msg）装配为平台可注册的插件。
package gomoku

import (
	"encoding/json"
	"errors"
	"log/slog"

	"xgames/internal/games"
	"xgames/internal/games/gomoku/bot"
	"xgames/internal/games/gomoku/msg"
	"xgames/internal/games/gomoku/session"
)

// ID 五子棋游戏标识
const ID = "gomoku"

// Register 注册五子棋游戏插件（进程启动时由 main 调用）。
// botsEnabled 决定机器人子系统是否启用（未启用时人机功能不可用）。
func Register(logger *slog.Logger, botsEnabled bool) {
	var bots *bot.Controller
	if botsEnabled {
		bots = bot.NewController(bot.NewEngine(logger), logger)
	}
	games.RegisterErrorMessages(msg.ErrorMessages)
	games.Register(&gomokuGame{bots: bots, logger: logger})
}

// gomokuGame 五子棋游戏描述符
type gomokuGame struct {
	bots   *bot.Controller // 全局共享的机器人控制器（未启用时为 nil）
	logger *slog.Logger
}

func (g *gomokuGame) ID() string         { return ID }
func (g *gomokuGame) Name() string       { return "五子棋" }
func (g *gomokuGame) MinPlayers() int    { return 2 }
func (g *gomokuGame) MaxPlayers() int    { return 2 }
func (g *gomokuGame) SupportsBots() bool { return g.bots != nil }

// NewBotController 返回共享机器人控制器（未启用时为 nil）
func (g *gomokuGame) NewBotController() games.BotController {
	if g.bots == nil {
		return nil
	}
	return g.bots
}

// NewSession 创建五子棋对局会话（未启动）
func (g *gomokuGame) NewSession(env games.SessionEnv) games.Session {
	gs := session.New(env.RoomCode, envPlayers(env), env.Broadcaster, env.ResultSink, env.Timeouts)
	if g.bots != nil {
		gs.SetEngine(g.bots.Engine()) // 真人挂机托管复用同一套规则引擎
		gs.SetDrawEvaluator(func(board []int) bool { return g.bots.EvaluateDrawOffer(env.RoomCode, board) })
	}
	return gs
}

// RestoreSession 从快照恢复五子棋对局会话（未启动）
func (g *gomokuGame) RestoreSession(env games.SessionEnv, snapshot []byte) (games.Session, error) {
	var snap session.Snapshot
	if err := json.Unmarshal(snapshot, &snap); err != nil {
		return nil, errors.New("五子棋对局快照损坏: " + err.Error())
	}
	gs, err := session.RestoreFromSnapshot(&snap, env.Broadcaster, env.ResultSink, env.Timeouts)
	if err != nil {
		return nil, err
	}
	if g.bots != nil {
		gs.SetEngine(g.bots.Engine())
		gs.SetDrawEvaluator(func(board []int) bool { return g.bots.EvaluateDrawOffer(env.RoomCode, board) })
	}
	return gs, nil
}

// envPlayers 平台玩家描述 → 五子棋对局玩家
func envPlayers(env games.SessionEnv) []*session.Player {
	players := make([]*session.Player, len(env.Players))
	for i, p := range env.Players {
		players[i] = &session.Player{ID: p.ID, Name: p.Name, Seat: p.Seat, IsBot: p.IsBot}
	}
	return players
}
