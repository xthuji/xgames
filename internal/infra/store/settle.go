package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// RecordMatch 结算写入：matches + match_players + player_stats + daily_scores 单事务原子提交。
// 按 result.GameID 分游戏记账（空按 "ddz" 兼容）；斗地主专属字段（landlord_*）其他游戏写零值。
func (s *SQLiteStore) RecordMatch(ctx context.Context, result MatchResult) error {
	gameID := result.GameID
	if gameID == "" {
		gameID = "ddz"
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO matches (game_id, landlord_id, landlord_win, multiplier, bot_engine) VALUES (?, ?, ?, ?, ?)`,
		gameID, result.LandlordID, boolToInt(result.LandlordWin), result.Multiplier, result.BotEngine)
	if err != nil {
		return err
	}
	matchID, err := res.LastInsertId()
	if err != nil {
		return err
	}

	day := time.Now().Format("2006-01-02")
	for _, p := range result.Players {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO match_players (match_id, player_id, is_landlord, score) VALUES (?, ?, ?, ?)`,
			matchID, p.PlayerID, boolToInt(p.IsLandlord), p.Score); err != nil {
			return err
		}
		if err := upsertPlayerStats(ctx, tx, gameID, p, result.LandlordWin); err != nil {
			return err
		}
		if err := upsertDailyScore(ctx, tx, gameID, p.PlayerID, day, p.Score); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// upsertPlayerStats 更新玩家累计统计，按游戏分行统计。
// 胜负口径：score > 0 胜 / score < 0 负 / score == 0 和局（麻将点炮旁观者、流局、棋类和棋）
// 不计胜负且不清连胜——否则别人点炮/流局会重置自己的连胜。
func upsertPlayerStats(ctx context.Context, tx *sql.Tx, gameID string, p MatchPlayerResult, landlordWin bool) error {
	win, loss := p.Score > 0, p.Score < 0

	// 先取当前连胜值，决定 streak 更新
	var cur, maxStreak int
	err := tx.QueryRowContext(ctx,
		`SELECT current_streak, max_win_streak FROM player_stats WHERE player_id = ? AND game_id = ?`,
		p.PlayerID, gameID).Scan(&cur, &maxStreak)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	var newCur int
	newMax := maxStreak // 最高连胜永不回退；输牌仅当前连胜清零，和局保持
	switch {
	case win:
		newCur = cur + 1
		if newCur > newMax {
			newMax = newCur
		}
	case loss:
		// 输牌清零
	default:
		newCur = cur // 和局：连胜保持
	}

	wins, losses := boolToInt(win), boolToInt(loss)
	lg, lw, fg, fw := 0, 0, 0, 0
	if p.IsLandlord {
		lg = 1
		lw = boolToInt(landlordWin)
	} else {
		fg = 1
		fw = boolToInt(!landlordWin)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO player_stats (player_id, game_id, total_games, wins, losses,
			landlord_games, landlord_wins, farmer_games, farmer_wins,
			score, current_streak, max_win_streak)
		VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(player_id, game_id) DO UPDATE SET
			total_games    = total_games + 1,
			wins           = wins + excluded.wins,
			losses         = losses + excluded.losses,
			landlord_games = landlord_games + excluded.landlord_games,
			landlord_wins  = landlord_wins + excluded.landlord_wins,
			farmer_games   = farmer_games + excluded.farmer_games,
			farmer_wins    = farmer_wins + excluded.farmer_wins,
			score          = score + excluded.score,
			current_streak = excluded.current_streak,
			max_win_streak = excluded.max_win_streak`,
		p.PlayerID, gameID, wins, losses, lg, lw, fg, fw, p.Score, newCur, newMax)
	return err
}

// upsertDailyScore 累加当日得分（按游戏分行）
func upsertDailyScore(ctx context.Context, tx *sql.Tx, gameID, playerID, day string, score int) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO daily_scores (player_id, day, game_id, score, wins) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(player_id, day, game_id) DO UPDATE SET
			score = score + excluded.score,
			wins  = wins + excluded.wins`,
		playerID, day, gameID, score, boolToInt(score > 0))
	return err
}

// ResetScores 清空积分统计：每次 App 启动时调用，实现“启动即重置积分”，
// 使头像下方的累计积分与排行榜都从本会话重新计起。
func (s *SQLiteStore) ResetScores(ctx context.Context) error {
	if _, err := s.w.ExecContext(ctx, `DELETE FROM daily_scores`); err != nil {
		return err
	}
	_, err := s.w.ExecContext(ctx, `DELETE FROM player_stats`)
	return err
}

// GetPlayerStats 查询玩家指定游戏的累计统计；无记录返回零值（gameID 空按 "ddz"）
func (s *SQLiteStore) GetPlayerStats(ctx context.Context, gameID, playerID string) (*PlayerStats, error) {
	if gameID == "" {
		gameID = "ddz"
	}
	st := &PlayerStats{PlayerID: playerID}
	err := s.r.QueryRowContext(ctx, `
		SELECT total_games, wins, losses, landlord_games, landlord_wins,
			farmer_games, farmer_wins, score, current_streak, max_win_streak
		FROM player_stats WHERE player_id = ? AND game_id = ?`, playerID, gameID).
		Scan(&st.TotalGames, &st.Wins, &st.Losses, &st.LandlordGames, &st.LandlordWins,
			&st.FarmerGames, &st.FarmerWins, &st.Score, &st.CurrentStreak, &st.MaxWinStreak)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	return st, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
