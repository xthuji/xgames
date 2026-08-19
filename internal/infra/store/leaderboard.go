package store

import (
	"context"
	"fmt"
)

// Leaderboard 查询指定游戏的排行榜（gameID 空按 "ddz"）。kind: total（总榜）/ daily（日榜）/ weekly（周榜）。
// 分页参数对齐 GetLeaderboardPayload{offset, limit}。
func (s *SQLiteStore) Leaderboard(ctx context.Context, gameID, kind string, offset, limit int) ([]LeaderboardEntry, error) {
	if gameID == "" {
		gameID = "ddz"
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	var query string
	switch kind {
	case "daily":
		// 自然日榜：当天累计
		query = `
			SELECT d.player_id, p.name, SUM(d.score) AS score, SUM(d.wins) AS wins, 0 AS games
			FROM daily_scores d JOIN players p ON p.id = d.player_id
			WHERE d.day = date('now', 'localtime') AND d.game_id = ?
			GROUP BY d.player_id ORDER BY score DESC LIMIT ? OFFSET ?`
	case "weekly":
		// ISO 周榜：本周一 00:00 起累计（strftime('%w') 周日=0）
		query = `
			SELECT d.player_id, p.name, SUM(d.score) AS score, SUM(d.wins) AS wins, 0 AS games
			FROM daily_scores d JOIN players p ON p.id = d.player_id
			WHERE d.day >= date('now', 'localtime', '-' || ((strftime('%w', 'now', 'localtime') + 6) % 7) || ' days')
				AND d.game_id = ?
			GROUP BY d.player_id ORDER BY score DESC LIMIT ? OFFSET ?`
	case "total", "":
		query = `
			SELECT s.player_id, p.name, s.score, s.wins, s.total_games AS games
			FROM player_stats s JOIN players p ON p.id = s.player_id
			WHERE s.game_id = ?
			ORDER BY s.score DESC LIMIT ? OFFSET ?`
	default:
		return nil, fmt.Errorf("未知排行榜类型: %s", kind)
	}

	rows, err := s.r.QueryContext(ctx, query, gameID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]LeaderboardEntry, 0, limit)
	for rows.Next() {
		var e LeaderboardEntry
		if err := rows.Scan(&e.PlayerID, &e.Name, &e.Score, &e.Wins, &e.Games); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// PlayerRank 指定游戏的总榜排名（得分严格高于自己的人数 + 1）；无统计记录返回 0（gameID 空按 "ddz"）
func (s *SQLiteStore) PlayerRank(ctx context.Context, gameID, playerID string) (int, error) {
	if gameID == "" {
		gameID = "ddz"
	}
	var hasScore bool
	if err := s.r.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM player_stats WHERE player_id = ? AND game_id = ? AND total_games > 0)`,
		playerID, gameID,
	).Scan(&hasScore); err != nil {
		return 0, err
	}
	if !hasScore {
		return 0, nil
	}

	var rank int
	err := s.r.QueryRowContext(ctx, `
		SELECT COUNT(*) + 1 FROM player_stats o
		WHERE o.game_id = ? AND o.score > (SELECT score FROM player_stats WHERE player_id = ? AND game_id = ?)`,
		gameID, playerID, gameID).Scan(&rank)
	if err != nil {
		return 0, err
	}
	return rank, nil
}
