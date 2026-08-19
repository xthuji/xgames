// Package ddz 斗地主游戏插件：实现 games.Game 描述符，
// 把对局会话（session）、机器人（bot）与私有协议（msg）装配为平台可注册的插件。
package ddz

import (
	"encoding/json"
	"errors"
	"log/slog"

	"xgames/internal/games"
	"xgames/internal/games/ddz/bot"
	"xgames/internal/games/ddz/msg"
	"xgames/internal/games/ddz/session"
)

// ID 斗地主游戏标识（协议缺省 game_id 兼容值）
const ID = "ddz"

// Register 注册斗地主游戏插件（进程启动时由 main 调用）。
// botsEnabled 决定机器人子系统是否启用（未启用时人机功能不可用）。
func Register(logger *slog.Logger, botsEnabled bool) {
	var bots *bot.Controller
	if botsEnabled {
		bots = bot.NewController(bot.NewEngine(logger), logger)
	}
	games.RegisterErrorMessages(msg.ErrorMessages)
	games.Register(&ddzGame{bots: bots, logger: logger})
}

// ddzGame 斗地主游戏描述符
type ddzGame struct {
	bots   *bot.Controller // 全局共享的机器人控制器（未启用时为 nil）
	logger *slog.Logger
}

func (g *ddzGame) ID() string         { return ID }
func (g *ddzGame) Name() string       { return "斗地主" }
func (g *ddzGame) MinPlayers() int    { return 3 }
func (g *ddzGame) MaxPlayers() int    { return 3 }
func (g *ddzGame) SupportsBots() bool { return g.bots != nil }

// NewBotController 返回共享机器人控制器（未启用时为 nil）
func (g *ddzGame) NewBotController() games.BotController {
	if g.bots == nil {
		return nil
	}
	return g.bots
}

// NewSession 创建斗地主对局会话（未启动）
func (g *ddzGame) NewSession(env games.SessionEnv) games.Session {
	gs := session.New(env.RoomCode, envPlayers(env), env.Broadcaster, env.ResultSink, env.Timeouts)
	if g.bots != nil {
		gs.SetEngine(g.bots.Engine()) // 真人挂机托管复用同一套规则引擎
	}
	return gs
}

// RestoreSession 从快照恢复斗地主对局会话（未启动）
func (g *ddzGame) RestoreSession(env games.SessionEnv, snapshot []byte) (games.Session, error) {
	var snap session.Snapshot
	if err := json.Unmarshal(snapshot, &snap); err != nil {
		return nil, errors.New("斗地主对局快照损坏: " + err.Error())
	}
	gs, err := session.RestoreFromSnapshot(&snap, env.Broadcaster, env.ResultSink, env.Timeouts)
	if err != nil {
		return nil, err
	}
	if g.bots != nil {
		gs.SetEngine(g.bots.Engine())
	}
	return gs, nil
}

// envPlayers 平台玩家描述 → 斗地主对局玩家
func envPlayers(env games.SessionEnv) []*session.Player {
	players := make([]*session.Player, len(env.Players))
	for i, p := range env.Players {
		players[i] = &session.Player{ID: p.ID, Name: p.Name, Seat: p.Seat, IsBot: p.IsBot}
	}
	return players
}
