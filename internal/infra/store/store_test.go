package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestPlayerRoundtrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id := NewID()
	require.NoError(t, s.EnsurePlayer(ctx, id, "测试玩家"))

	p, err := s.GetPlayer(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, "测试玩家", p.Name)

	// 不存在的玩家
	p, err = s.GetPlayer(ctx, "no-such-id")
	require.NoError(t, err)
	assert.Nil(t, p)
}

func TestReconnectToken(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id := NewID()
	require.NoError(t, s.EnsurePlayer(ctx, id, "p"))

	ok, err := s.ValidateReconnectToken(ctx, id, "tok")
	require.NoError(t, err)
	assert.False(t, ok)

	require.NoError(t, s.SaveReconnectToken(ctx, id, "tok", time.Now().Add(time.Hour)))
	ok, err = s.ValidateReconnectToken(ctx, id, "tok")
	require.NoError(t, err)
	assert.True(t, ok)

	// 轮换：新 token 覆盖旧 token
	require.NoError(t, s.SaveReconnectToken(ctx, id, "tok2", time.Now().Add(time.Hour)))
	ok, _ = s.ValidateReconnectToken(ctx, id, "tok")
	assert.False(t, ok)
	ok, _ = s.ValidateReconnectToken(ctx, id, "tok2")
	assert.True(t, ok)

	// 过期
	require.NoError(t, s.SaveReconnectToken(ctx, id, "tok3", time.Now().Add(-time.Hour)))
	ok, _ = s.ValidateReconnectToken(ctx, id, "tok3")
	assert.False(t, ok)
}

// 和局（0 分）不计胜负且不清连胜：麻将点炮旁观者/流局、棋类和棋不应破连胜
func TestRecordMatchDrawKeepsStreak(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a, b := NewID(), NewID()
	for _, p := range []struct{ id, name string }{{a, "甲"}, {b, "乙"}} {
		require.NoError(t, s.EnsurePlayer(ctx, p.id, p.name))
	}

	// 第一局甲胜：连胜 1
	require.NoError(t, s.RecordMatch(ctx, MatchResult{
		GameID:  "ddz",
		Players: []MatchPlayerResult{{PlayerID: a, Score: 1}, {PlayerID: b, Score: -1}},
	}))

	// 第二局双方 0 分（和局）：不计胜负、连胜保持
	require.NoError(t, s.RecordMatch(ctx, MatchResult{
		GameID:  "ddz",
		Players: []MatchPlayerResult{{PlayerID: a, Score: 0}, {PlayerID: b, Score: 0}},
	}))
	st, err := s.GetPlayerStats(ctx, "ddz", a)
	require.NoError(t, err)
	assert.Equal(t, 2, st.TotalGames)
	assert.Equal(t, 1, st.Wins)
	assert.Equal(t, 0, st.Losses)
	assert.Equal(t, 1, st.CurrentStreak)

	// 第三局甲负：连胜清零、计负
	require.NoError(t, s.RecordMatch(ctx, MatchResult{
		GameID:  "ddz",
		Players: []MatchPlayerResult{{PlayerID: a, Score: -1}, {PlayerID: b, Score: 1}},
	}))
	st, _ = s.GetPlayerStats(ctx, "ddz", a)
	assert.Equal(t, 1, st.Losses)
	assert.Equal(t, 0, st.CurrentStreak)
}

func TestRecordMatchAndStats(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	landlord, farmerA, farmerB := NewID(), NewID(), NewID()
	for _, p := range []struct{ id, name string }{{landlord, "地主"}, {farmerA, "农民A"}, {farmerB, "农民B"}} {
		require.NoError(t, s.EnsurePlayer(ctx, p.id, p.name))
	}

	// 地主胜，倍数 4
	result := MatchResult{
		LandlordID: landlord, LandlordWin: true, Multiplier: 4, BotEngine: "",
		Players: []MatchPlayerResult{
			{PlayerID: landlord, IsLandlord: true, Score: 8},
			{PlayerID: farmerA, IsLandlord: false, Score: -4},
			{PlayerID: farmerB, IsLandlord: false, Score: -4},
		},
	}
	require.NoError(t, s.RecordMatch(ctx, result))

	st, err := s.GetPlayerStats(ctx, "ddz", landlord)
	require.NoError(t, err)
	assert.Equal(t, 1, st.TotalGames)
	assert.Equal(t, 1, st.Wins)
	assert.Equal(t, 1, st.LandlordGames)
	assert.Equal(t, 1, st.LandlordWins)
	assert.Equal(t, 8, st.Score)
	assert.Equal(t, 1, st.CurrentStreak)
	assert.Equal(t, 1, st.MaxWinStreak)

	st, err = s.GetPlayerStats(ctx, "ddz", farmerA)
	require.NoError(t, err)
	assert.Equal(t, 1, st.Losses)
	assert.Equal(t, 1, st.FarmerGames)
	assert.Equal(t, 0, st.FarmerWins)
	assert.Equal(t, -4, st.Score)

	// 第二局：农民胜，地主连败清零
	result2 := MatchResult{
		LandlordID: landlord, LandlordWin: false, Multiplier: 1,
		Players: []MatchPlayerResult{
			{PlayerID: landlord, IsLandlord: true, Score: -2},
			{PlayerID: farmerA, IsLandlord: false, Score: 1},
			{PlayerID: farmerB, IsLandlord: false, Score: 1},
		},
	}
	require.NoError(t, s.RecordMatch(ctx, result2))

	st, _ = s.GetPlayerStats(ctx, "ddz", landlord)
	assert.Equal(t, 2, st.TotalGames)
	assert.Equal(t, 6, st.Score)
	assert.Equal(t, 0, st.CurrentStreak) // 输了清零
	assert.Equal(t, 1, st.MaxWinStreak)

	st, _ = s.GetPlayerStats(ctx, "ddz", farmerA)
	assert.Equal(t, 1, st.CurrentStreak) // 首局输，第二局赢 → 连胜 1
	assert.Equal(t, 1, st.MaxWinStreak)
	assert.Equal(t, 1, st.FarmerWins) // 首局农民败，第二局农民胜
	assert.Equal(t, 2, st.FarmerGames)

	// 总榜（按游戏过滤）
	entries, err := s.Leaderboard(ctx, "ddz", "total", 0, 10)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	assert.Equal(t, landlord, entries[0].PlayerID) // 6 分最高

	// 日榜（按游戏过滤）
	entries, err = s.Leaderboard(ctx, "ddz", "daily", 0, 10)
	require.NoError(t, err)
	assert.Len(t, entries, 3)

	// 多游戏隔离：另一游戏结算不影响 ddz 统计/榜单，斗地主专属字段写零值
	require.NoError(t, s.RecordMatch(ctx, MatchResult{
		GameID: "test_game",
		Players: []MatchPlayerResult{
			{PlayerID: farmerA, Score: 3},
			{PlayerID: farmerB, Score: -3},
		},
	}))
	st, _ = s.GetPlayerStats(ctx, "ddz", farmerA)
	assert.Equal(t, 2, st.TotalGames) // ddz 统计不变
	st, err = s.GetPlayerStats(ctx, "test_game", farmerA)
	require.NoError(t, err)
	assert.Equal(t, 1, st.TotalGames)
	assert.Equal(t, 3, st.Score)
	entries, err = s.Leaderboard(ctx, "test_game", "total", 0, 10)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, farmerA, entries[0].PlayerID)
	// 无记录的游戏返回零值，缺省空 gameID 兼容斗地主
	empty, err := s.GetPlayerStats(ctx, "test_game", landlord)
	require.NoError(t, err)
	assert.Equal(t, 0, empty.TotalGames)
	compat, err := s.GetPlayerStats(ctx, "", landlord)
	require.NoError(t, err)
	assert.Equal(t, 2, compat.TotalGames)
}

func TestMigrationUpgradeExistingDB(t *testing.T) {
	// 模拟旧版库：无 schema_migrations、无 game_id 列，已有存量数据；
	// Open 后 0002 应把存量归属 ddz 且不丢失。
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	old, err := sql.Open("sqlite", "file:"+dbPath)
	require.NoError(t, err)
	_, err = old.Exec(`
		CREATE TABLE players (id TEXT PRIMARY KEY, name TEXT NOT NULL,
			machine_id TEXT, created_at DATETIME, last_seen_at DATETIME);
		CREATE TABLE player_stats (
			player_id TEXT PRIMARY KEY, total_games INTEGER DEFAULT 0,
			wins INTEGER DEFAULT 0, losses INTEGER DEFAULT 0,
			landlord_games INTEGER DEFAULT 0, landlord_wins INTEGER DEFAULT 0,
			farmer_games INTEGER DEFAULT 0, farmer_wins INTEGER DEFAULT 0,
			score INTEGER DEFAULT 0, current_streak INTEGER DEFAULT 0, max_win_streak INTEGER DEFAULT 0);
		CREATE TABLE daily_scores (player_id TEXT NOT NULL, day DATE NOT NULL,
			score INTEGER DEFAULT 0, wins INTEGER DEFAULT 0, PRIMARY KEY (player_id, day));
		INSERT INTO players (id, name) VALUES ('p1', '存量玩家');
		INSERT INTO player_stats (player_id, total_games, wins, score) VALUES ('p1', 5, 3, 42);
		INSERT INTO daily_scores VALUES ('p1', '2024-01-01', 10, 1);
	`)
	require.NoError(t, err)
	require.NoError(t, old.Close())

	s, err := Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	st, err := s.GetPlayerStats(ctx, "ddz", "p1")
	require.NoError(t, err)
	assert.Equal(t, 5, st.TotalGames)
	assert.Equal(t, 42, st.Score)

	// 升级后新结算正常写入，重启不重复迁移（数据不翻倍）
	s2, err := Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s2.Close() })
	st, err = s2.GetPlayerStats(ctx, "ddz", "p1")
	require.NoError(t, err)
	assert.Equal(t, 5, st.TotalGames)
}

func TestRoomSnapshots(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.SaveRoomSnapshot(ctx, "123456", `{"state":"waiting"}`))
	all, err := s.LoadRoomSnapshots(ctx)
	require.NoError(t, err)
	assert.Equal(t, `{"state":"waiting"}`, all["123456"])

	// upsert 覆盖
	require.NoError(t, s.SaveRoomSnapshot(ctx, "123456", `{"state":"playing"}`))
	all, _ = s.LoadRoomSnapshots(ctx)
	assert.Equal(t, `{"state":"playing"}`, all["123456"])

	require.NoError(t, s.DeleteRoomSnapshot(ctx, "123456"))
	all, _ = s.LoadRoomSnapshots(ctx)
	assert.Empty(t, all)
}
