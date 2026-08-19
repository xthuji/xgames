// Package msg 麻将协议：游戏私有消息类型常量与 payload 结构。
//
// 常量与结构名带 Mj 前缀：协议生成器把各游戏 msg 合并进同一份
// protocol.ts，名字必须全局唯一。
package msg

import "xgames/internal/platform/protocol"

// 客户端 → 服务端 消息类型
const (
	MsgMjDiscard protocol.MessageType = "mj_discard" // 出牌
	MsgMjAfk     protocol.MessageType = "mj_afk"     // 挂机/取消挂机
	MsgMjPong    protocol.MessageType = "mj_pong"    // 碰
	MsgMjKong    protocol.MessageType = "mj_kong"    // 杠（明杠）
	MsgMjWin     protocol.MessageType = "mj_win"     // 胡（自摸/点炮）
	MsgMjPass    protocol.MessageType = "mj_pass"    // 放弃碰/杠/胡
)

// 服务端 → 客户端 消息类型
const (
	MsgMjTurn        protocol.MessageType = "mj_turn"         // 轮到出牌
	MsgMjDraw        protocol.MessageType = "mj_draw"         // 玩家摸牌（仅通知该玩家）
	MsgMjDiscarded   protocol.MessageType = "mj_discarded"    // 有人出牌
	MsgMjPongMade    protocol.MessageType = "mj_pong_made"    // 有人碰牌
	MsgMjKongMade    protocol.MessageType = "mj_kong_made"    // 有人杠牌
	MsgMjActionAvail protocol.MessageType = "mj_action_avail" // 可碰/杠/胡，请选择
	MsgMjAfkChanged  protocol.MessageType = "mj_afk_changed"  // 挂机状态变更
	MsgMjWallEmpty   protocol.MessageType = "mj_wall_empty"   // 牌墙已尽，流局
	MsgMjDice        protocol.MessageType = "mj_dice"         // 开局掷骰定庄（广播）
)

// 麻将专属错误码
const (
	ErrCodeNotYourTurn  = 3301
	ErrCodeInvalidTile  = 3302
	ErrCodeNoAction     = 3303
	ErrCodeNotAllowed   = 3304
)

// ErrorMessages 麻将错误码对应的默认文案
var ErrorMessages = map[int]string{
	ErrCodeNotYourTurn: "还没轮到您",
	ErrCodeInvalidTile: "打出的牌不合法",
	ErrCodeNoAction:    "当前没有可执行的动作",
	ErrCodeNotAllowed:  "不允许此操作",
}

// --- 客户端请求 Payloads ---

// MjDiscardPayload 出牌请求
type MjDiscardPayload struct {
	Tile int `json:"tile"` // 牌类型（0-33）
}

// MjAfkPayload 挂机请求
type MjAfkPayload struct {
	Afk bool `json:"afk"`
}

// MjPongPayload 碰请求
type MjPongPayload struct {
	Tile int `json:"tile"` // 要碰的牌
}

// MjKongPayload 杠请求
type MjKongPayload struct {
	Tile int `json:"tile"` // 要杠的牌
}

// MjWinPayload 胡请求
type MjWinPayload struct {
	IsSelfDraw bool `json:"is_self_draw"` // true=自摸, false=点炮
}

// MjPassPayload 放弃动作
type MjPassPayload struct{}

// --- 服务端响应 Payloads ---

// MjTurnPayload 轮到出牌通知
type MjTurnPayload struct {
	PlayerID   string `json:"player_id"`
	Timeout    int    `json:"timeout"`
	MeldNumber int    `json:"meld_number"` // 本局第几巡（1 起）
}

// MjDrawPayload 摸牌通知（定向发给摸牌玩家）
type MjDrawPayload struct {
	Tile       int `json:"tile"`        // 摸到的牌
	WallRemain int `json:"wall_remain"` // 剩余牌数
}

// MjDiscardedPayload 出牌通知（广播）
type MjDiscardedPayload struct {
	PlayerID string `json:"player_id"`
	Tile     int    `json:"tile"`
}

// MjPongMadePayload 碰牌通知（广播）
type MjPongMadePayload struct {
	PlayerID string `json:"player_id"`
	Tile     int    `json:"tile"`
	From     string `json:"from"` // 被碰的玩家 ID
}

// MjKongMadePayload 杠牌通知（广播）
type MjKongMadePayload struct {
	PlayerID  string `json:"player_id"`
	Tile      int    `json:"tile"`
	From      string `json:"from"`       // 被杠的玩家 ID（暗杠/补杠时为空）
	IsAnKong  bool   `json:"is_ankong"`  // 是否暗杠
	IsAddKong bool   `json:"is_add_kong"` // 是否补杠（碰后加杠，客户端将已碰面子升级为杠）
}

// MjActionAvailPayload 可用动作通知（定向）
type MjActionAvailPayload struct {
	CanPong bool `json:"can_pong"`
	CanKong bool `json:"can_kong"`
	CanWin  bool `json:"can_win"`
	Tile    int  `json:"tile"` // 相关牌
	Timeout int  `json:"timeout"`
}

// MjAfkChangedPayload 挂机状态变更
type MjAfkChangedPayload struct {
	PlayerID string `json:"player_id"`
	Afk      bool   `json:"afk"`
}

// MjPlayerHandDTO 玩家手牌信息（对其他玩家隐藏具体牌面）
type MjPlayerHandDTO struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"`
	Seat       int    `json:"seat"`
	IsBot      bool   `json:"is_bot"`
	Online     bool   `json:"online"`
	Afk        bool   `json:"afk"`
	HandCount  int    `json:"hand_count"`      // 手牌数量（对其他玩家可见）
	Hand       []int  `json:"hand,omitempty"`  // 仅自己可见
	Melds      []MjMeldDTO `json:"melds"`
	IsDealer   bool   `json:"is_dealer"`
	Score      int    `json:"score"`           // 玩家平台积分（头像下方展示）
}

// MjMeldDTO 面子信息
type MjMeldDTO struct {
	Type int `json:"type"`  // 0=碰 1=杠 3=暗杠
	Tile int `json:"tile"`
	From int `json:"from"` // 来源玩家座位
}

// MjGameStateDTO 游戏状态（重连恢复用）
type MjGameStateDTO struct {
	Phase       string            `json:"phase"` // playing/ended
	Players     []MjPlayerHandDTO `json:"players"`
	CurrentTurn string            `json:"current_turn"`
	MyIndex     int               `json:"my_index"` // 自己在 players 中的索引
	Dealer      int               `json:"dealer"`   // 庄家座位
	WallRemain  int               `json:"wall_remain"`
	MeldNumber  int               `json:"meld_number"`
	DiscardPools [][]int          `json:"discard_pools"` // 每个玩家的弃牌池
}

// GameOverExtra 麻将 game_over 扩展数据（放入平台 GameOverPayload.Extra）
type GameOverExtra struct {
	PlayerHands []PlayerHand `json:"player_hands"` // 所有玩家手牌（结算时展示）
}

// MjDicePayload 开局掷骰结果（广播）
type MjDicePayload struct {
	Values []int `json:"values"` // 各玩家掷骰点数（与 players 索引对齐）
	Dealer int   `json:"dealer"` // 庄家索引（先手）
}

// PlayerHand 玩家手牌信息（用于游戏结束展示）
type PlayerHand struct {
	PlayerID   string    `json:"player_id"`
	PlayerName string    `json:"player_name"`
	Hand       []int     `json:"hand"`
	Melds      []MjMeldDTO `json:"melds"`
}
