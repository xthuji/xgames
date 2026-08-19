// Package store 实现 SQLite 持久层（纯 Go 驱动 modernc.org/sqlite）。
//
// 写入策略：所有写操作经写连接（MaxOpenConns=1）串行执行，规避 SQLite 写锁竞争；
// 读路径（排行榜/统计）使用独立读连接池并发执行（WAL 模式）。
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"xgames/internal/migrations"
)

// Store 持久层接口（预留 PostgreSQL 替换路径）
type Store interface {
	// players
	EnsurePlayer(ctx context.Context, id, name string) error
	GetOrCreatePlayer(ctx context.Context, id, machineID string) (*Player, error)
	GetPlayer(ctx context.Context, id string) (*Player, error)
	TouchPlayer(ctx context.Context, id string) error
	UpdatePlayerName(ctx context.Context, id, newName string) (string, error)

	// reconnect tokens
	SaveReconnectToken(ctx context.Context, playerID, token string, expiresAt time.Time) error
	ValidateReconnectToken(ctx context.Context, playerID, token string) (bool, error)

	// settle & stats（gameID 缺省 "" 按斗地主兼容）
	RecordMatch(ctx context.Context, result MatchResult) error
	GetPlayerStats(ctx context.Context, gameID, playerID string) (*PlayerStats, error)
	ResetScores(ctx context.Context) error

	// leaderboard
	Leaderboard(ctx context.Context, gameID, kind string, offset, limit int) ([]LeaderboardEntry, error)
	PlayerRank(ctx context.Context, gameID, playerID string) (int, error)

	// room snapshots
	SaveRoomSnapshot(ctx context.Context, roomCode, stateJSON string) error
	LoadRoomSnapshots(ctx context.Context) (map[string]string, error)
	DeleteRoomSnapshot(ctx context.Context, roomCode string) error

	// room records（房间信息持久化：大厅展示/排查）
	UpsertRoom(ctx context.Context, rec RoomRecord) error
	CloseRoomRecord(ctx context.Context, roomCode string) error
	LoadOpenRooms(ctx context.Context) ([]RoomRecord, error)

	// health
	Ping(ctx context.Context) error

	Close() error
}

// Player 玩家身份记录
type Player struct {
	ID         string
	Name       string
	CreatedAt  time.Time
	LastSeenAt *time.Time
}

// PlayerStats 玩家累计统计
type PlayerStats struct {
	PlayerID      string `json:"player_id"`
	TotalGames    int    `json:"total_games"`
	Wins          int    `json:"wins"`
	Losses        int    `json:"losses"`
	LandlordGames int    `json:"landlord_games"`
	LandlordWins  int    `json:"landlord_wins"`
	FarmerGames   int    `json:"farmer_games"`
	FarmerWins    int    `json:"farmer_wins"`
	Score         int    `json:"score"`
	CurrentStreak int    `json:"current_streak"`
	MaxWinStreak  int    `json:"max_win_streak"`
}

// MatchResult 一局结算结果（单事务写入）
type MatchResult struct {
	GameID      string // 空按 "ddz" 兼容
	LandlordID  string
	LandlordWin bool
	Multiplier  int
	BotEngine   string // 固定 heuristic（纯规则引擎）/ "" (全真人)
	Players     []MatchPlayerResult
}

// MatchPlayerResult 单个玩家的本局结果
type MatchPlayerResult struct {
	PlayerID   string
	IsLandlord bool
	Score      int // 本局得分（负为输）
}

// LeaderboardEntry 排行榜条目
type LeaderboardEntry struct {
	PlayerID string `json:"player_id"`
	Name     string `json:"name"`
	Score    int    `json:"score"`
	Wins     int    `json:"wins"`
	Games    int    `json:"games"`
}

// SQLiteStore SQLite 实现
type SQLiteStore struct {
	w *sql.DB // 写连接（单连接串行）
	r *sql.DB // 读连接池
}

// Open 打开（或创建）SQLite 数据库并执行迁移。
// dsn 为文件路径（自动创建父目录）；使用 WAL + busy_timeout。
func Open(path string) (*SQLiteStore, error) {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据目录失败: %w", err)
		}
	}

	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)"

	w, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1) // 单写者

	r, err := sql.Open("sqlite", dsn)
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(8)

	s := &SQLiteStore{w: w, r: r}
	if err := s.migrate(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// migrate 执行内嵌迁移脚本（按文件名顺序）。每个脚本在独立事务中执行，
// 并在 schema_migrations 中登记；已执行的脚本跳过，保证破坏性迁移（如重建表）只执行一次。
func (s *SQLiteStore) migrate() error {
	if _, err := s.w.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		name       TEXT PRIMARY KEY,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return err
	}

	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return err
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		var applied bool
		if err := s.w.QueryRow(`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name = ?)`, e.Name()).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}

		data, err := migrations.FS.ReadFile(e.Name())
		if err != nil {
			return err
		}
		if err := s.applyMigration(e.Name(), string(data)); err != nil {
			return fmt.Errorf("执行迁移 %s 失败: %w", e.Name(), err)
		}
	}
	return nil
}

// applyMigration 单事务执行迁移脚本并登记版本（失败整体回滚）
func (s *SQLiteStore) applyMigration(name, script string) error {
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(script); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (name) VALUES (?)`, name); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) Close() error {
	errW := s.w.Close()
	errR := s.r.Close()
	if errW != nil {
		return errW
	}
	return errR
}

// Ping 健康探测（readyz 用：可写探测）
func (s *SQLiteStore) Ping(ctx context.Context) error {
	return s.w.PingContext(ctx)
}

// NewID 生成时间可排序的 32 位 hex ID（毫秒时间戳前缀 + 随机数）
func NewID() string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[0:8], uint64(time.Now().UnixMilli()))
	_, _ = rand.Read(b[8:])
	return hex.EncodeToString(b[:])
}
