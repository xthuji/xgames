package store

import "context"

// SaveRoomSnapshot 保存房间快照（upsert），用于崩溃恢复
func (s *SQLiteStore) SaveRoomSnapshot(ctx context.Context, roomCode, stateJSON string) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO room_snapshots (room_code, state_json, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(room_code) DO UPDATE SET
			state_json = excluded.state_json,
			updated_at = CURRENT_TIMESTAMP`,
		roomCode, stateJSON)
	return err
}

// LoadRoomSnapshots 启动时加载全部房间快照：room_code → state_json
func (s *SQLiteStore) LoadRoomSnapshots(ctx context.Context) (map[string]string, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT room_code, state_json FROM room_snapshots`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var code, state string
		if err := rows.Scan(&code, &state); err != nil {
			return nil, err
		}
		out[code] = state
	}
	return out, rows.Err()
}

// DeleteRoomSnapshot 房间结束/解散后删除快照
func (s *SQLiteStore) DeleteRoomSnapshot(ctx context.Context, roomCode string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM room_snapshots WHERE room_code = ?`, roomCode)
	return err
}
