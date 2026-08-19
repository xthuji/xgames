// Package mahjong 麻将游戏插件
package mahjong

import (
	"encoding/json"
	"errors"
	"log/slog"

	"xgames/internal/games"
	"xgames/internal/games/mahjong/bot"
	"xgames/internal/games/mahjong/msg"
	"xgames/internal/games/mahjong/session"
)

const ID = "mahjong"

func Register(logger *slog.Logger, botsEnabled bool) {
	var bots *bot.Controller
	if botsEnabled {
		bots = bot.NewController(bot.NewEngine(logger), logger)
	}
	games.RegisterErrorMessages(msg.ErrorMessages)
	games.Register(&mahjongGame{bots: bots, logger: logger})
}

type mahjongGame struct {
	bots   *bot.Controller
	logger *slog.Logger
}

func (g *mahjongGame) ID() string         { return ID }
func (g *mahjongGame) Name() string       { return "麻将" }
func (g *mahjongGame) MinPlayers() int    { return 2 }
func (g *mahjongGame) MaxPlayers() int    { return 4 }
func (g *mahjongGame) SupportsBots() bool { return g.bots != nil }

func (g *mahjongGame) NewBotController() games.BotController {
	if g.bots == nil {
		return nil
	}
	return g.bots
}

func (g *mahjongGame) NewSession(env games.SessionEnv) games.Session {
	gs := session.New(env.RoomCode, envPlayers(env), env.Broadcaster, env.ResultSink, env.Timeouts)
	gs.SetScores(env.Scores)
	if g.bots != nil {
		gs.SetEngine(g.bots.Engine())
	}
	return gs
}

func (g *mahjongGame) RestoreSession(env games.SessionEnv, snapshot []byte) (games.Session, error) {
	var snap session.Snapshot
	if err := json.Unmarshal(snapshot, &snap); err != nil {
		return nil, errors.New("麻将对局快照损坏: " + err.Error())
	}
	gs, err := session.RestoreFromSnapshot(&snap, env.Broadcaster, env.ResultSink, env.Timeouts)
	if err != nil {
		return nil, err
	}
	if len(snap.Scores) > 0 {
		gs.SetScores(snap.Scores)
	} else {
		gs.SetScores(env.Scores)
	}
	if g.bots != nil {
		gs.SetEngine(g.bots.Engine())
	}
	return gs, nil
}

func envPlayers(env games.SessionEnv) []*session.Player {
	players := make([]*session.Player, len(env.Players))
	for i, p := range env.Players {
		players[i] = &session.Player{ID: p.ID, Name: p.Name, Seat: p.Seat, IsBot: p.IsBot}
	}
	return players
}
