package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"xgames/internal/platform/nickname"
)

// EnsurePlayer 玩家不存在则创建，存在则更新 last_seen_at
func (s *SQLiteStore) EnsurePlayer(ctx context.Context, id, name string) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO players (id, name, last_seen_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET last_seen_at = CURRENT_TIMESTAMP`, id, name)
	return err
}

// GetOrCreatePlayer 获取或创建玩家（基于稳定 ID）。
// 如果玩家不存在，使用默认昵称创建；如果已存在，返回现有信息并更新活跃时间。
func (s *SQLiteStore) GetOrCreatePlayer(ctx context.Context, id, machineID string) (*Player, error) {
	// 先尝试查询
	p, err := s.GetPlayer(ctx, id)
	if err != nil {
		return nil, err
	}
	if p != nil {
		// 玩家已存在，更新活跃时间
		_ = s.TouchPlayer(ctx, id)
		return p, nil
	}

	// 玩家不存在，创建新玩家（使用随机昵称）
	name := nickname.Generate()
	_, err = s.w.ExecContext(ctx, `
		INSERT INTO players (id, name, machine_id, last_seen_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET last_seen_at = CURRENT_TIMESTAMP`, id, name, machineID)
	if err != nil {
		return nil, err
	}

	// 重新查询以返回完整信息
	return s.GetPlayer(ctx, id)
}

// GetPlayer 按 ID 查询玩家；不存在返回 (nil, nil)
func (s *SQLiteStore) GetPlayer(ctx context.Context, id string) (*Player, error) {
	row := s.r.QueryRowContext(ctx,
		`SELECT id, name, created_at, last_seen_at FROM players WHERE id = ?`, id)
	p := &Player{}
	var lastSeen sql.NullTime
	err := row.Scan(&p.ID, &p.Name, &p.CreatedAt, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastSeen.Valid {
		p.LastSeenAt = &lastSeen.Time
	}
	return p, nil
}

// TouchPlayer 更新玩家活跃时间
func (s *SQLiteStore) TouchPlayer(ctx context.Context, id string) error {
	_, err := s.w.ExecContext(ctx,
		`UPDATE players SET last_seen_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

// SaveReconnectToken 保存（轮换）重连令牌，每玩家仅一条
func (s *SQLiteStore) SaveReconnectToken(ctx context.Context, playerID, token string, expiresAt time.Time) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO reconnect_tokens (player_id, token, expires_at) VALUES (?, ?, ?)
		ON CONFLICT(player_id) DO UPDATE SET token = excluded.token, expires_at = excluded.expires_at`,
		playerID, token, expiresAt.UTC())
	return err
}

// ValidateReconnectToken 校验令牌有效（匹配且未过期）
func (s *SQLiteStore) ValidateReconnectToken(ctx context.Context, playerID, token string) (bool, error) {
	row := s.r.QueryRowContext(ctx,
		`SELECT token, expires_at FROM reconnect_tokens WHERE player_id = ?`, playerID)
	var stored string
	var expiresAt time.Time
	if err := row.Scan(&stored, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return stored == token && time.Now().Before(expiresAt), nil
}

// UpdatePlayerName 更新玩家昵称（长度 1-20，去除空白）
func (s *SQLiteStore) UpdatePlayerName(ctx context.Context, id, newName string) (string, error) {
	name := strings.TrimSpace(newName)
	if name == "" || len(name) > 20 {
		return "", errors.New("昵称长度需在 1-20 字符之间")
	}
	_, err := s.w.ExecContext(ctx, `UPDATE players SET name = ?, last_seen_at = CURRENT_TIMESTAMP WHERE id = ?`, name, id)
	if err != nil {
		return "", err
	}
	return name, nil
}
