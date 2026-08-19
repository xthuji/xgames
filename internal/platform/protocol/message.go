package protocol

import (
	"encoding/json"
	"log/slog"
)

// Message 基础消息结构
type Message struct {
	Type    MessageType     `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// NewMessage 构造 JSON 信封消息（payload 序列化失败属于程序错误，降级为空对象）
func NewMessage(t MessageType, payload any) Message {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("序列化消息失败", "type", t, "err", err)
		data = []byte("{}")
	}
	return Message{Type: t, Payload: data}
}

// ParsePayload 解析消息 payload 到指定结构体
func ParsePayload[T any](msg Message) (T, error) {
	var v T
	err := json.Unmarshal(msg.Payload, &v)
	return v, err
}

// MessageType 消息类型
type MessageType string

// 客户端 → 服务端 消息类型
const (
	// 连接操作
	MsgReconnect MessageType = "reconnect" // 断线重连
	MsgPing      MessageType = "ping"      // 心跳 ping

	// 房间操作
	MsgCreateRoom    MessageType = "create_room"    // 创建房间
	MsgJoinRoom      MessageType = "join_room"      // 加入房间
	MsgLeaveRoom     MessageType = "leave_room"     // 离开房间
	MsgCloseRoom     MessageType = "close_room"     // 关闭房间（仅房主可操作）
	MsgQuickMatch    MessageType = "quick_match"    // 快速匹配
	MsgCancelMatch   MessageType = "cancel_match"   // 取消匹配
	MsgPracticeMatch MessageType = "practice_match" // 人机练习（创建带 2 个机器人席位的房间）
	MsgAddBot        MessageType = "add_bot"        // 添加机器人（手动占用房间空席位）
	MsgRecreateRoom  MessageType = "recreate_room"  // 关闭已有房间并新建（单请求完成，仅房主可用）
	MsgReady         MessageType = "ready"          // 准备就绪
	MsgCancelReady   MessageType = "cancel_ready"   // 取消准备

	// 对局（平台级；游戏私有消息由各游戏自行定义，见 games/*/msg）
	MsgLeaveGame MessageType = "leave_game" // 离开对局（客户端 UI 离开对战页面时通知服务端终止）

	// 排行榜
	MsgGetStats             MessageType = "get_stats"              // 获取个人统计
	MsgGetLeaderboard       MessageType = "get_leaderboard"        // 获取排行榜
	MsgGetRoomList          MessageType = "get_room_list"          // 获取房间列表
	MsgGetOnlineCount       MessageType = "get_online_count"       // 获取在线人数
	MsgGetMaintenanceStatus MessageType = "get_maintenance_status" // 获取维护状态
	MsgUpdateProfile        MessageType = "update_profile"         // 更新玩家资料
	MsgUpdateReplaySetting  MessageType = "update_replay_setting"  // 更新复盘功能设置
	MsgGameSync             MessageType = "game_sync"              // 对局同步检测（客户端主动查询）
	MsgRequestGameState     MessageType = "request_game_state"     // 请求对局状态（场景恢复用）
	MsgClearUserData        MessageType = "clear_user_data"        // 清理用户数据（房间+对战）
)

// 服务端 → 客户端 消息类型
const (
	// 连接相关
	MsgConnected     MessageType = "connected"      // 连接成功
	MsgReconnected   MessageType = "reconnected"    // 重连成功
	MsgPong          MessageType = "pong"           // 心跳 pong
	MsgPlayerOffline MessageType = "player_offline" // 玩家掉线通知
	MsgPlayerOnline  MessageType = "player_online"  // 玩家上线通知
	MsgOnlineCount   MessageType = "online_count"   // 在线人数更新

	// 房间相关
	MsgRoomCreated  MessageType = "room_created"  // 房间创建成功
	MsgRoomJoined   MessageType = "room_joined"   // 加入房间成功
	MsgPlayerJoined MessageType = "player_joined" // 其他玩家加入
	MsgPlayerLeft   MessageType = "player_left"   // 玩家离开
	MsgPlayerReady  MessageType = "player_ready"  // 玩家准备
	MsgMatchFound   MessageType = "match_found"   // 匹配成功

	// 游戏流程（平台级信封；游戏私有消息由各游戏定义）
	MsgGameStart MessageType = "game_start" // 游戏开始（通用：玩家列表 + 房间选项）
	MsgGameOver  MessageType = "game_over"  // 游戏结束（通用：胜者 + 得分 + 游戏扩展数据）

	// 排行榜
	MsgStatsResult       MessageType = "stats_result"       // 个人统计结果
	MsgLeaderboardResult MessageType = "leaderboard_result" // 排行榜结果
	MsgRoomListResult    MessageType = "room_list_result"   // 房间列表结果

	// 系统通知
	MsgMaintenancePush MessageType = "maintenance_push" // 主动推送
	MsgMaintenancePull MessageType = "maintenance_pull" // 被动拉取

	// 错误
	MsgError                 MessageType = "error"                    // 错误消息
	MsgProfileUpdated        MessageType = "profile_updated"          // 资料更新通知
	MsgReplaySettingUpdated  MessageType = "replay_setting_updated"   // 复盘功能设置更新通知

	// 同步
	MsgGameSyncResult  MessageType = "game_sync_result"  // 对局同步检测结果
	MsgGameState       MessageType = "game_state"        // 对局状态响应（请求状态恢复）
	MsgUserDataCleared MessageType = "user_data_cleared" // 用户数据清理完成通知
)
