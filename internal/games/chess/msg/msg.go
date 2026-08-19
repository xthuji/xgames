// Package msg 中国象棋协议：游戏私有消息类型常量与 payload 结构。
//
// 常量与结构名带 Cc 前缀，协议生成器把各游戏 msg 合并进同一份
// protocol.ts，名字必须全局唯一。
package msg

import "xgames/internal/platform/protocol"

// 客户端 → 服务端 消息类型
const (
	MsgCcMove         protocol.MessageType = "cc_move"          // 走子
	MsgCcAfk          protocol.MessageType = "cc_afk"           // 挂机/取消挂机
	MsgCcResign       protocol.MessageType = "cc_resign"        // 认输
	MsgCcDrawRequest  protocol.MessageType = "cc_draw_request"  // 请求和棋
	MsgCcDrawResponse protocol.MessageType = "cc_draw_response" // 和棋应答
)

// 服务端 → 客户端 消息类型
const (
	MsgCcTurn          protocol.MessageType = "cc_turn"           // 轮到走子
	MsgCcMoveMade      protocol.MessageType = "cc_move_made"      // 有人走子
	MsgCcAfkChanged    protocol.MessageType = "cc_afk_changed"    // 挂机状态变更通知
	MsgCcDrawRequested protocol.MessageType = "cc_draw_requested" // 对手请求和棋通知
	MsgCcDrawResult    protocol.MessageType = "cc_draw_result"    // 和棋请求结果
)

// 中国象棋专属错误码（3201-3205，避免与五子棋 3101-3103 冲突）
const (
	ErrCodeInvalidMove     = 3201
	ErrCodeNotYourTurn     = 3202
	ErrCodeNotYourPiece    = 3203
	ErrCodeIllegalMove     = 3204
	ErrCodeDrawNotYourTurn = 3205
)

// ErrorMessages 中国象棋错误码对应的默认文案
var ErrorMessages = map[int]string{
	ErrCodeInvalidMove:     "非法走法",
	ErrCodeNotYourTurn:     "还没轮到您",
	ErrCodeNotYourPiece:    "不能移动对方的棋子",
	ErrCodeIllegalMove:     "该棋子无法走到此位置",
	ErrCodeDrawNotYourTurn: "非你的回合，不能请求和棋",
}

// --- 客户端请求 Payloads ---

// CcMovePayload 走子请求
type CcMovePayload struct {
	FromRow int `json:"from_row"`
	FromCol int `json:"from_col"`
	ToRow   int `json:"to_row"`
	ToCol   int `json:"to_col"`
}

// CcAfkPayload 挂机请求
type CcAfkPayload struct {
	Afk bool `json:"afk"`
}

// --- 认输 / 和棋 Payloads ---

// CcResignPayload 认输请求（空结构）
type CcResignPayload struct{}

// CcDrawRequestPayload 和棋请求
type CcDrawRequestPayload struct {
	RequesterID string `json:"requester_id"` // 请求者 playerID
}

// CcDrawResponsePayload 和棋应答
type CcDrawResponsePayload struct {
	Accept bool `json:"accept"` // true=同意和棋, false=拒绝
}

// CcDrawRequestedPayload 通知对手有人请求和棋
type CcDrawRequestedPayload struct {
	RequesterID   string `json:"requester_id"`
	RequesterName string `json:"requester_name"`
}

// CcDrawResultPayload 和棋请求结果
type CcDrawResultPayload struct {
	Accepted bool `json:"accepted"` // true=双方同意和棋（对局已结束）, false=被拒绝
}

// --- 服务端响应 Payloads ---

// CcTurnPayload 轮到走子通知
type CcTurnPayload struct {
	PlayerID   string `json:"player_id"`
	Camp       int    `json:"camp"`        // 1=红, 2=黑
	Timeout    int    `json:"timeout"`     // 超时时间（秒）
	MoveNumber int    `json:"move_number"` // 本局第几手（1 起）
}

// CcMoveMadePayload 走子通知
type CcMoveMadePayload struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"`
	FromRow    int    `json:"from_row"`
	FromCol    int    `json:"from_col"`
	ToRow      int    `json:"to_row"`
	ToCol      int    `json:"to_col"`
	Piece      int    `json:"piece"`      // 走动的棋子
	Captured   int    `json:"captured"`   // 被吃的棋子（0 = 无）
	MoveNumber int    `json:"move_number"`
}

// CcAfkChangedPayload 挂机状态变更通知
type CcAfkChangedPayload struct {
	PlayerID string `json:"player_id"`
	Afk      bool   `json:"afk"`
}

// CcGameStateDTO 游戏状态数据传输对象（重连恢复用）
type CcGameStateDTO struct {
	Phase         string                `json:"phase"` // playing/ended
	Players       []protocol.PlayerInfo `json:"players"`
	Board         []int                 `json:"board"` // 9×10 行优先
	CurrentTurn   string                `json:"current_turn"`
	MyCamp        int                   `json:"my_camp"` // 该 DTO 视角玩家阵营（1=红, 2=黑）
	LastFromRow   int                   `json:"last_from_row"` // 最近一手起点（-1 = 尚未走子）
	LastFromCol   int                   `json:"last_from_col"`
	LastToRow     int                   `json:"last_to_row"`
	LastToCol     int                   `json:"last_to_col"`
	MoveNumber    int                   `json:"move_number"`
	RedCaptures   []int                 `json:"red_captures"`   // 红方吃掉的棋子
	BlackCaptures []int                 `json:"black_captures"` // 黑方吃掉的棋子
}
