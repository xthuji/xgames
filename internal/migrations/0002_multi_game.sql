-- 0002_multi_game.sql: 多游戏平台化 —— 结算/统计/日榜按游戏拆分。
-- 由 migrate() 版本跟踪保证只执行一次；旧数据全部归属斗地主（ddz）。

-- matches：直接加列（斗地主专属字段 landlord_id/multiplier 保留，其他游戏写零值）
ALTER TABLE matches ADD COLUMN game_id TEXT NOT NULL DEFAULT 'ddz';
CREATE INDEX IF NOT EXISTS idx_matches_game ON matches(game_id);

-- player_stats：主键需加入 game_id，SQLite 不支持改主键，按"建新表→搬数据→替换"重建
CREATE TABLE player_stats_new (
    player_id      TEXT NOT NULL REFERENCES players(id),
    game_id        TEXT NOT NULL DEFAULT 'ddz',
    total_games    INTEGER DEFAULT 0,
    wins           INTEGER DEFAULT 0,
    losses         INTEGER DEFAULT 0,
    landlord_games INTEGER DEFAULT 0,
    landlord_wins  INTEGER DEFAULT 0,
    farmer_games   INTEGER DEFAULT 0,
    farmer_wins    INTEGER DEFAULT 0,
    score          INTEGER DEFAULT 0,
    current_streak INTEGER DEFAULT 0,
    max_win_streak INTEGER DEFAULT 0,
    PRIMARY KEY (player_id, game_id)
);
INSERT INTO player_stats_new (
    player_id, game_id, total_games, wins, losses,
    landlord_games, landlord_wins, farmer_games, farmer_wins,
    score, current_streak, max_win_streak)
SELECT player_id, 'ddz', total_games, wins, losses,
    landlord_games, landlord_wins, farmer_games, farmer_wins,
    score, current_streak, max_win_streak
FROM player_stats;
DROP TABLE player_stats;
ALTER TABLE player_stats_new RENAME TO player_stats;
CREATE INDEX IF NOT EXISTS idx_stats_score ON player_stats(game_id, score DESC);

-- daily_scores：主键加入 game_id，同样重建
CREATE TABLE daily_scores_new (
    player_id TEXT NOT NULL REFERENCES players(id),
    day       DATE NOT NULL,                  -- YYYY-MM-DD
    game_id   TEXT NOT NULL DEFAULT 'ddz',
    score     INTEGER DEFAULT 0,
    wins      INTEGER DEFAULT 0,
    PRIMARY KEY (player_id, day, game_id)
);
INSERT INTO daily_scores_new (player_id, day, game_id, score, wins)
SELECT player_id, day, 'ddz', score, wins FROM daily_scores;
DROP TABLE daily_scores;
ALTER TABLE daily_scores_new RENAME TO daily_scores;
CREATE INDEX IF NOT EXISTS idx_daily_day ON daily_scores(game_id, day, score DESC);
