// Package chess 中国象棋游戏插件：实现 games.Game 描述符，
// 把对局会话（session）、机器人（bot）与私有协议（msg）装配为平台可注册的插件。
package chess

import (
	"encoding/json"
	"errors"
	"log/slog"

	"xgames/internal/games"
	"xgames/internal/games/chess/bot"
	"xgames/internal/games/chess/msg"
	"xgames/internal/games/chess/session"
)

// ID 中国象棋游戏标识
const ID = "chess"

// Register 注册中国象棋游戏插件
func Register(logger *slog.Logger, botsEnabled bool) {
	var bots *bot.Controller
	if botsEnabled {
		bots = bot.NewController(bot.NewEngine(logger), logger)
	}
	games.RegisterErrorMessages(msg.ErrorMessages)
	games.Register(&chessGame{bots: bots, logger: logger})
}

type chessGame struct {
	bots   *bot.Controller
	logger *slog.Logger
}

func (g *chessGame) ID() string         { return ID }
func (g *chessGame) Name() string       { return "中国象棋" }
func (g *chessGame) MinPlayers() int    { return 2 }
func (g *chessGame) MaxPlayers() int    { return 2 }
func (g *chessGame) SupportsBots() bool { return g.bots != nil }

func (g *chessGame) NewBotController() games.BotController {
	if g.bots == nil {
		return nil
	}
	return g.bots
}

func (g *chessGame) NewSession(env games.SessionEnv) games.Session {
	cs := session.New(env.RoomCode, envPlayers(env), env.Broadcaster, env.ResultSink, env.Timeouts)
	if g.bots != nil {
		cs.SetEngine(g.bots.Engine())
		cs.SetDrawEvaluator(func(board []int, camp int) bool { return g.bots.EvaluateDrawOffer(env.RoomCode, board, camp) })
	}
	return cs
}

func (g *chessGame) RestoreSession(env games.SessionEnv, snapshot []byte) (games.Session, error) {
	var snap session.Snapshot
	if err := json.Unmarshal(snapshot, &snap); err != nil {
		return nil, errors.New("中国象棋对局快照损坏: " + err.Error())
	}
	cs, err := session.RestoreFromSnapshot(&snap, env.Broadcaster, env.ResultSink, env.Timeouts)
	if err != nil {
		return nil, err
	}
	if g.bots != nil {
		cs.SetEngine(g.bots.Engine())
		cs.SetDrawEvaluator(func(board []int, camp int) bool { return g.bots.EvaluateDrawOffer(env.RoomCode, board, camp) })
	}
	return cs, nil
}

func envPlayers(env games.SessionEnv) []*session.Player {
	players := make([]*session.Player, len(env.Players))
	for i, p := range env.Players {
		players[i] = &session.Player{ID: p.ID, Name: p.Name, Seat: p.Seat, IsBot: p.IsBot}
	}
	return players
}
