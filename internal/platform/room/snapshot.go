package room

import (
	"encoding/json"
	"time"
)

// 房间状态（快照中的字符串取值）
const (
	snapStateWaiting = "waiting"
	snapStatePlaying = "playing"
)

// RoomPlayer 快照中的房间玩家（Waiting 状态恢复用）
type RoomPlayer struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Seat  int    `json:"seat"`
	IsBot bool   `json:"is_bot"`
	Ready bool   `json:"ready"`
}

// RoomSnapshot 房间快照：元数据 + Waiting 玩家 + （可选）进行中对局全量状态。
// Game 为游戏私有快照（JSON 原文，由各游戏自行解析恢复），平台不感知其结构。
type RoomSnapshot struct {
	RoomCode  string            `json:"room_code"`
	GameID    string            `json:"game_id,omitempty"` // 房间所属游戏（旧快照缺省按 "ddz" 恢复）
	State     string            `json:"state"`             // waiting / playing
	CreatedAt time.Time         `json:"created_at"`
	CreatorID string            `json:"creator_id,omitempty"` // 房间创建者（不可被分配为机器人）
	Options   map[string]string `json:"options,omitempty"`    // 房间选项（如斗地主 difficulty）
	Players   []RoomPlayer      `json:"players"`
	Game      json.RawMessage   `json:"game,omitempty"` // 进行中对局快照（Running 恢复）
}

// encodeSnapshot 序列化为 JSON 字符串（写入 room_snapshots.state_json）
func encodeSnapshot(rs *RoomSnapshot) (string, error) {
	data, err := json.Marshal(rs)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// decodeSnapshot 反序列化；损坏数据返回 error（调用方丢弃并告警）。
// 向后兼容：旧快照无 game_id，按 "ddz" 恢复。
func decodeSnapshot(stateJSON string) (*RoomSnapshot, error) {
	var rs RoomSnapshot
	if err := json.Unmarshal([]byte(stateJSON), &rs); err != nil {
		return nil, err
	}
	if rs.GameID == "" {
		rs.GameID = "ddz"
	}
	return &rs, nil
}
