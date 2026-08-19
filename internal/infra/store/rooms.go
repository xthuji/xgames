package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// rooms.go 房间信息持久化：房间 ID、归属游戏、创建人、参与人与状态。
// 内存房间状态由房间管理器维护，此处仅持久化供大厅展示/排查的房间记录。

// RoomRecord 房间持久化记录
type RoomRecord struct {
	RoomCode    string    `json:"room_code"`
	GameID      string    `json:"game_id"`
	CreatorID   string    `json:"creator_id"`
	CreatorName string    `json:"creator_name"`
	PlayerIDs   []string  `json:"player_ids"` // 参与人 ID（含机器人）
	Status      string    `json:"status"`     // open=存在中 / closed=已关闭
	CreatedAt   time.Time `json:"created_at"`
}

const (
	RoomStatusOpen   = "open"
	RoomStatusClosed = "closed"
)

// UpsertRoom 写入/更新房间记录（created_at 保持首次创建时间）
func (s *SQLiteStore) UpsertRoom(ctx context.Context, rec RoomRecord) error {
	if rec.Status == "" {
		rec.Status = RoomStatusOpen
	}
	ids, err := json.Marshal(rec.PlayerIDs)
	if err != nil {
		ids = []byte("[]")
	}
	_, err = s.w.ExecContext(ctx, `INSERT INTO rooms
		(room_code, game_id, creator_id, creator_name, player_ids, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(room_code) DO UPDATE SET
			game_id      = excluded.game_id,
			creator_id   = excluded.creator_id,
			creator_name = excluded.creator_name,
			player_ids   = excluded.player_ids,
			status       = excluded.status`,
		rec.RoomCode, rec.GameID, rec.CreatorID, rec.CreatorName, string(ids), rec.Status, rec.CreatedAt.UTC())
	return err
}

// CloseRoomRecord 标记房间记录为已关闭（幂等）
func (s *SQLiteStore) CloseRoomRecord(ctx context.Context, roomCode string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE rooms
		SET status = ?, closed_at = CURRENT_TIMESTAMP
		WHERE room_code = ? AND status = ?`,
		RoomStatusClosed, roomCode, RoomStatusOpen)
	return err
}

// LoadOpenRooms 加载所有未关闭的房间记录
func (s *SQLiteStore) LoadOpenRooms(ctx context.Context) ([]RoomRecord, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT room_code, game_id, creator_id, creator_name, player_ids, created_at
		FROM rooms WHERE status = ? ORDER BY created_at DESC`, RoomStatusOpen)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RoomRecord
	for rows.Next() {
		var rec RoomRecord
		var idsJSON string
		var createdAt sql.NullTime
		if err := rows.Scan(&rec.RoomCode, &rec.GameID, &rec.CreatorID, &rec.CreatorName, &idsJSON, &createdAt); err != nil {
			return nil, err
		}
		rec.Status = RoomStatusOpen
		if createdAt.Valid {
			rec.CreatedAt = createdAt.Time
		}
		if err := json.Unmarshal([]byte(idsJSON), &rec.PlayerIDs); err != nil {
			rec.PlayerIDs = nil
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
