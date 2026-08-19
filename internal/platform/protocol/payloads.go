package protocol

import "encoding/json"

// --- 客户端请求 Payloads ---

// ReconnectPayload 断线重连请求
type ReconnectPayload struct {
	Token    string `json:"token"`     // 重连令牌
	PlayerID string `json:"player_id"` // 玩家 ID
}

// PingPayload 心跳请求
type PingPayload struct {
	Timestamp int64 `json:"timestamp"` // 客户端时间戳（毫秒）
}

// CreateRoomPayload 创建房间请求（缺省 game_id 兼容为 "ddz"）
type CreateRoomPayload struct {
	GameID string `json:"game_id,omitempty"` // 游戏 ID（如 "ddz"）
}

// JoinRoomPayload 加入房间请求
type JoinRoomPayload struct {
	RoomCode string `json:"room_code"`
}

// QuickMatchPayload 快速匹配请求（缺省 game_id 兼容为 "ddz"）
type QuickMatchPayload struct {
	GameID string `json:"game_id,omitempty"` // 游戏 ID（按游戏分队列）
}

// LeaveGamePayload 离开对局请求（客户端 UI 离开对战页面时发送，服务端终止对局）
type LeaveGamePayload struct{}

// GetStatsPayload 获取个人战绩请求（payload 可缺省，空按缺省游戏）
type GetStatsPayload struct {
	GameID string `json:"game_id,omitempty"`
}

// GetLeaderboardPayload 获取排行榜请求
type GetLeaderboardPayload struct {
	GameID string `json:"game_id,omitempty"` // 游戏（空按缺省游戏）
	Type   string `json:"type"`              // total/daily/weekly
	Offset int    `json:"offset"`            // 偏移量
	Limit  int    `json:"limit"`             // 数量
}

// --- 服务端响应 Payloads ---

// ConnectedPayload 连接成功响应
type ConnectedPayload struct {
	PlayerID       string `json:"player_id"`
	PlayerName     string `json:"player_name"`
	ReconnectToken string `json:"reconnect_token"` // 重连令牌
	Score          int    `json:"score"`           // 当前积分
	Rank           int    `json:"rank"`            // 当前排名（0 = 未上榜）
}

// ReconnectedPayload 重连成功响应
type ReconnectedPayload struct {
	PlayerID       string          `json:"player_id"`
	PlayerName     string          `json:"player_name"`
	ReconnectToken string          `json:"reconnect_token"`      // 轮换后的新令牌
	RoomCode       string          `json:"room_code,omitempty"`  // 如果在房间中
	GameState      json.RawMessage `json:"game_state,omitempty"` // 如果在游戏中（游戏私有状态 JSON）
	Score          int             `json:"score"`                // 当前积分
	Rank           int             `json:"rank"`                 // 当前排名（0 = 未上榜）
}

// PongPayload 心跳响应
type PongPayload struct {
	ClientTimestamp int64 `json:"client_timestamp"` // 客户端发送的时间戳
	ServerTimestamp int64 `json:"server_timestamp"` // 服务器时间戳（毫秒）
}

// PlayerOfflinePayload 玩家掉线通知
type PlayerOfflinePayload struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"`
	Timeout    int    `json:"timeout"` // 等待重连超时（秒）
}

// PlayerOnlinePayload 玩家上线通知
type PlayerOnlinePayload struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"`
}

// OnlineCountPayload 在线人数更新
type OnlineCountPayload struct {
	Count int `json:"count"` // 当前在线人数
}

// RoomCreatedPayload 房间创建成功响应
type RoomCreatedPayload struct {
	RoomCode string     `json:"room_code"`
	GameID   string     `json:"game_id"` // 房间所属游戏
	Player   PlayerInfo `json:"player"`
}

// RoomJoinedPayload 加入房间成功响应
type RoomJoinedPayload struct {
	RoomCode  string       `json:"room_code"`
	GameID    string       `json:"game_id,omitempty"`    // 房间所属游戏
	CreatorID string       `json:"creator_id,omitempty"` // 房间创建人（仅创建人可添加机器人）
	Player    PlayerInfo   `json:"player"`
	Players   []PlayerInfo `json:"players"` // 房间内所有玩家
}

// PlayerJoinedPayload 其他玩家加入通知
type PlayerJoinedPayload struct {
	Player PlayerInfo `json:"player"`
}

// PlayerLeftPayload 玩家离开通知
type PlayerLeftPayload struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"`
}

// PlayerReadyPayload 玩家准备通知
type PlayerReadyPayload struct {
	PlayerID string `json:"player_id"`
	Ready    bool   `json:"ready"`
}

// GameStartPayload 游戏开始通知（平台级通用信封）
type GameStartPayload struct {
	Players []PlayerInfo      `json:"players"`           // 按座位顺序排列
	Options map[string]string `json:"options,omitempty"` // 房间选项（如斗地主 difficulty=normal/hard）
}

// PracticeMatchPayload 人机练习请求（缺省 game_id 兼容为 "ddz"）
type PracticeMatchPayload struct {
	GameID     string `json:"game_id,omitempty"`    // 游戏 ID
	Difficulty string `json:"difficulty,omitempty"` // 斗地主机器人难度 normal=正常/hard=专家，默认 normal（难度只影响机器人记牌完整度）
}

// AddBotPayload 手动向房间空席位添加机器人请求
type AddBotPayload struct {
	Difficulty string `json:"difficulty,omitempty"` // 机器人难度 normal=正常/hard=专家，默认 normal（难度只影响机器人记牌完整度）
}

// RecreateRoomPayload 关闭已有房间并新建请求（单请求完成关闭+新建并返回最终结果，仅房主可用）
type RecreateRoomPayload struct {
	GameID     string `json:"game_id,omitempty"`    // 新房间所属游戏
	Practice   bool   `json:"practice,omitempty"`   // true=人机练习（机器人填满剩余席位）
	Difficulty string `json:"difficulty,omitempty"` // 人机练习机器人难度 normal/hard
}

// GameOverPayload 游戏结束通知（平台级通用信封；游戏私有数据放 Extra）
type GameOverPayload struct {
	WinnerID   string          `json:"winner_id"`
	WinnerName string          `json:"winner_name"`
	DrawReason string          `json:"draw_reason,omitempty"` // 和棋原因（平局时可选，如 repetition/natural_limit/insufficient_material）
	Scores     []PlayerScore   `json:"scores"`                // 每位玩家本局得分
	Extra      json.RawMessage `json:"extra,omitempty"`       // 游戏扩展数据（斗地主：剩余手牌/倍数）
}

// PlayerScore 玩家本局得分（用于游戏结束结算）
type PlayerScore struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"`
	IsLandlord bool   `json:"is_landlord,omitempty"` // 是否是地主（斗地主专属）
	Score      int    `json:"score"`                 // 本局得分（输为负，赢为正）
}

// MaintenancePayload 维护模式通知
type MaintenancePayload struct {
	Maintenance bool `json:"maintenance"` // 是否在维护模式
}

// MaintenanceStatusPayload 维护状态响应
type MaintenanceStatusPayload struct {
	Maintenance bool `json:"maintenance"` // 是否在维护模式
}

// ErrorPayload 错误响应
type ErrorPayload struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"` // 可选额外数据（如 ErrRoomExists 时的房间信息）
}

// RoomExistsData ErrRoomExists 错误的额外数据：已有房间的基本信息
type RoomExistsData struct {
	RoomCode      string `json:"room_code"`
	GameID        string `json:"game_id"`
	CreatorIsSelf bool   `json:"creator_is_self"`
}

// StatsResultPayload 个人统计结果
type StatsResultPayload struct {
	PlayerID      string  `json:"player_id"`
	PlayerName    string  `json:"player_name"`
	TotalGames    int     `json:"total_games"`
	Wins          int     `json:"wins"`
	Losses        int     `json:"losses"`
	WinRate       float64 `json:"win_rate"`
	LandlordGames int     `json:"landlord_games"`
	LandlordWins  int     `json:"landlord_wins"`
	FarmerGames   int     `json:"farmer_games"`
	FarmerWins    int     `json:"farmer_wins"`
	Score         int     `json:"score"`
	Rank          int     `json:"rank"`
	CurrentStreak int     `json:"current_streak"`
	MaxWinStreak  int     `json:"max_win_streak"`
}

// LeaderboardResultPayload 排行榜结果
type LeaderboardResultPayload struct {
	Type    string             `json:"type"` // total/daily/weekly
	Entries []LeaderboardEntry `json:"entries"`
}

// LeaderboardEntry 排行榜条目
type LeaderboardEntry struct {
	Rank       int     `json:"rank"`
	PlayerID   string  `json:"player_id"`
	PlayerName string  `json:"player_name"`
	Score      int     `json:"score"`
	Wins       int     `json:"wins"`
	WinRate    float64 `json:"win_rate"`
}

// RoomListResultPayload 房间列表结果
type RoomListResultPayload struct {
	Rooms []RoomListItem `json:"rooms"`
}

// RoomListItem 房间列表项
type RoomListItem struct {
	RoomCode    string `json:"room_code"`
	GameID      string `json:"game_id"` // 房间所属游戏
	PlayerCount int    `json:"player_count"`
	MaxPlayers  int    `json:"max_players"`
	CreatorName string `json:"creator_name,omitempty"` // 房间创建人昵称
	State       string `json:"state"`                  // waiting=等待中 / playing=对局进行中
}

// UpdateProfilePayload 更新玩家资料请求
type UpdateProfilePayload struct {
	Name string `json:"name"` // 新昵称（1-20 字符）
}

// UpdateReplaySettingPayload 更新复盘功能设置请求
type UpdateReplaySettingPayload struct {
	ReplayEnabled bool `json:"replay_enabled"` // 是否启用复盘功能
}

// ProfileUpdatedPayload 资料更新通知
type ProfileUpdatedPayload struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"` // 新昵称
}

// ReplaySettingUpdatedPayload 复盘功能设置更新通知
type ReplaySettingUpdatedPayload struct {
	PlayerID      string `json:"player_id"`
	ReplayEnabled bool   `json:"replay_enabled"` // 复盘功能状态
}

// UserDataClearedPayload 用户数据清理完成通知
type UserDataClearedPayload struct {
	PlayerID string `json:"player_id"`
}

// GameSyncResultPayload 对局同步检测结果（服务端 → 客户端）
type GameSyncResultPayload struct {
	Status   string `json:"status"`              // active=对局进行中 | waiting=房间等待中 | none=无房间
	RoomCode string `json:"room_code,omitempty"` // 房间号（status != none 时返回）
}

// GetRoomListPayload 获取房间列表请求（缺省 game_id 兼容为 "ddz"）
type GetRoomListPayload struct {
	GameID string `json:"game_id,omitempty"` // 按游戏过滤房间列表
}

// GameStatePayload 对局状态响应（服务端 → 客户端；State 为游戏私有状态 JSON）
type GameStatePayload struct {
	Available bool            `json:"available"`       // 是否有活跃对局
	State     json.RawMessage `json:"state,omitempty"` // 游戏私有状态（斗地主：GameStateDTO）
}

// --- 通用数据结构 ---

// PlayerInfo 玩家信息
type PlayerInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Seat       int    `json:"seat"`             // 座位号 0-2
	Ready      bool   `json:"ready"`            // 是否准备
	IsLandlord bool   `json:"is_landlord"`      // 是否是地主
	CardsCount int    `json:"cards_count"`      // 手牌数量
	Online     bool   `json:"online"`           // 是否在线
	IsBot      bool   `json:"is_bot,omitempty"` // 是否是机器人
	Afk        bool   `json:"afk,omitempty"`    // 是否挂机（仅对局内有效）
}

// CardInfo 牌信息
type CardInfo struct {
	Suit  int `json:"suit"`  // 花色: 0=黑桃, 1=红心, 2=梅花, 3=方块, 4=王
	Rank  int `json:"rank"`  // 点数: 3-17 (3-2, 小王=16, 大王=17)
	Color int `json:"color"` // 颜色: 0=黑, 1=红
}
