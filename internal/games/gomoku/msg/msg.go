// Package msg 五子棋协议：游戏私有消息类型常量与 payload 结构。
//
// 平台信封（protocol.Message / NewMessage / ParsePayload）与平台级消息
// （房间/匹配/用户/排行）仍在 platform/protocol；这里只放五子棋专属内容。
// 常量与结构名带 Gk 前缀：协议生成器把各游戏 msg 合并进同一份
// protocol.ts，名字必须全局唯一。
package msg

import "xgames/internal/platform/protocol"

// 客户端 → 服务端 消息类型
const (
	MsgGkMove        protocol.MessageType = "gk_move"         // 落子
	MsgGkAfk         protocol.MessageType = "gk_afk"          // 挂机/取消挂机（托管开关）
	MsgGkResign      protocol.MessageType = "gk_resign"       // 认输
	MsgGkDrawRequest protocol.MessageType = "gk_draw_request" // 请求和棋
	MsgGkDrawResponse protocol.MessageType = "gk_draw_response" // 和棋应答
)

// 服务端 → 客户端 消息类型
const (
	MsgGkTurn          protocol.MessageType = "gk_turn"           // 轮到落子
	MsgGkMoveMade      protocol.MessageType = "gk_move_made"      // 有人落子
	MsgGkAfkChanged    protocol.MessageType = "gk_afk_changed"    // 挂机状态变更通知
	MsgGkDrawRequested protocol.MessageType = "gk_draw_requested" // 对手请求和棋通知
	MsgGkDrawResult    protocol.MessageType = "gk_draw_result"    // 和棋请求结果
)

// 五子棋专属错误码
const (
	ErrCodeNotYourTurn    = 3101
	ErrCodeOccupied       = 3102
	ErrCodeOutOfBounds    = 3103
	ErrCodeDrawNotYourTurn = 3104
)

// ErrorMessages 五子棋错误码对应的默认文案（注册时并入平台文案表）
var ErrorMessages = map[int]string{
	ErrCodeNotYourTurn:     "还没轮到您",
	ErrCodeOccupied:        "该位置已有棋子",
	ErrCodeOutOfBounds:     "落子位置超出棋盘",
	ErrCodeDrawNotYourTurn: "非你的回合，不能请求和棋",
}

// --- 客户端请求 Payloads ---

// GkMovePayload 落子请求
type GkMovePayload struct {
	Row int `json:"row"` // 行（0-14）
	Col int `json:"col"` // 列（0-14）
}

// GkAfkPayload 挂机请求（托管开关：挂机后由机器人策略自动落子）
type GkAfkPayload struct {
	Afk bool `json:"afk"` // true = 挂机, false = 取消挂机
}

// --- 认输 / 和棋 Payloads ---

// GkResignPayload 认输请求（空结构）
type GkResignPayload struct{}

// GkDrawRequestPayload 和棋请求
type GkDrawRequestPayload struct {
	RequesterID string `json:"requester_id"` // 请求者 playerID
}

// GkDrawResponsePayload 和棋应答
type GkDrawResponsePayload struct {
	Accept bool `json:"accept"` // true=同意和棋, false=拒绝
}

// GkDrawRequestedPayload 通知对手有人请求和棋
type GkDrawRequestedPayload struct {
	RequesterID   string `json:"requester_id"`
	RequesterName string `json:"requester_name"`
}

// GkDrawResultPayload 和棋请求结果
type GkDrawResultPayload struct {
	Accepted bool `json:"accepted"` // true=双方同意和棋（对局已结束）, false=被拒绝
}

// --- 服务端响应 Payloads ---

// GkTurnPayload 轮到落子通知
type GkTurnPayload struct {
	PlayerID   string `json:"player_id"`
	Timeout    int    `json:"timeout"`     // 超时时间（秒）
	MoveNumber int    `json:"move_number"` // 本局第几手（1 起）
}

// GkMoveMadePayload 落子通知
type GkMoveMadePayload struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"`
	Row        int    `json:"row"`
	Col        int    `json:"col"`
	Color      int    `json:"color"`       // 1=黑, 2=白
	MoveNumber int    `json:"move_number"` // 本局第几手（1 起）
}

// GkAfkChangedPayload 挂机状态变更通知（广播给全房间）
type GkAfkChangedPayload struct {
	PlayerID string `json:"player_id"`
	Afk      bool   `json:"afk"`
}

// GkGameStateDTO 游戏状态数据传输对象（重连恢复用）
type GkGameStateDTO struct {
	Phase       string                `json:"phase"` // playing/ended
	Players     []protocol.PlayerInfo `json:"players"`
	Board       []int                 `json:"board"` // 15×15 行优先：0=空, 1=黑, 2=白
	CurrentTurn string                `json:"current_turn"`
	MyColor     int                   `json:"my_color"` // 该 DTO 视角玩家执子颜色（1=黑, 2=白）
	LastRow     int                   `json:"last_row"` // 最近一手位置（-1 = 尚未落子）
	LastCol     int                   `json:"last_col"`
	MoveNumber  int                   `json:"move_number"` // 下一手的序号（1 起）
}
