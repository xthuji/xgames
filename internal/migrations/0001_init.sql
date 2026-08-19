-- 斗地主 SQLite 初始迁移 (见技术设计 7.2)

CREATE TABLE IF NOT EXISTS players (
    id          TEXT PRIMARY KEY,             -- ULID
    name        TEXT NOT NULL,
    machine_id  TEXT,                         -- 机器唯一标识（IP + 机器码哈希）
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_seen_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_players_machine ON players(machine_id);

CREATE TABLE IF NOT EXISTS reconnect_tokens (
    player_id   TEXT PRIMARY KEY REFERENCES players(id),
    token       TEXT NOT NULL UNIQUE,
    expires_at  DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS matches (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    ended_at     DATETIME DEFAULT CURRENT_TIMESTAMP,
    landlord_id  TEXT NOT NULL,
    landlord_win INTEGER NOT NULL,            -- 1/0
    multiplier   INTEGER NOT NULL,
    bot_engine   TEXT                         -- 固定 heuristic（纯规则引擎）/'' (全真人)
);

CREATE TABLE IF NOT EXISTS match_players (
    match_id    INTEGER NOT NULL REFERENCES matches(id),
    player_id   TEXT NOT NULL REFERENCES players(id),
    is_landlord INTEGER NOT NULL,
    score       INTEGER NOT NULL,
    PRIMARY KEY (match_id, player_id)
);

CREATE TABLE IF NOT EXISTS player_stats (
    player_id      TEXT PRIMARY KEY REFERENCES players(id),
    total_games    INTEGER DEFAULT 0,
    wins           INTEGER DEFAULT 0,
    losses         INTEGER DEFAULT 0,
    landlord_games INTEGER DEFAULT 0,
    landlord_wins  INTEGER DEFAULT 0,
    farmer_games   INTEGER DEFAULT 0,
    farmer_wins    INTEGER DEFAULT 0,
    score          INTEGER DEFAULT 0,
    current_streak INTEGER DEFAULT 0,
    max_win_streak INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS daily_scores (
    player_id TEXT NOT NULL REFERENCES players(id),
    day       DATE NOT NULL,                  -- YYYY-MM-DD
    score     INTEGER DEFAULT 0,
    wins      INTEGER DEFAULT 0,
    PRIMARY KEY (player_id, day)
);

CREATE TABLE IF NOT EXISTS room_snapshots (
    room_code  TEXT PRIMARY KEY,
    state_json TEXT NOT NULL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_matches_ended ON matches(ended_at);
CREATE INDEX IF NOT EXISTS idx_daily_day ON daily_scores(day, score DESC);
CREATE INDEX IF NOT EXISTS idx_stats_score ON player_stats(score DESC);
