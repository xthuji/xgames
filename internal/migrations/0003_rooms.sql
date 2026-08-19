-- 房间信息持久化：房间 ID、归属游戏、创建人、参与人与状态。
-- 房间生命周期与内存态同步：创建/更新时 upsert，房间关闭时标记 closed。
CREATE TABLE IF NOT EXISTS rooms (
    room_code    TEXT PRIMARY KEY,
    game_id      TEXT NOT NULL,
    creator_id   TEXT NOT NULL DEFAULT '',
    creator_name TEXT NOT NULL DEFAULT '',
    player_ids   TEXT NOT NULL DEFAULT '[]',   -- 参与人 ID JSON 数组（含机器人）
    status       TEXT NOT NULL DEFAULT 'open', -- open=存在中 / closed=已关闭
    created_at   DATETIME NOT NULL,
    closed_at    DATETIME
);

CREATE INDEX IF NOT EXISTS idx_rooms_status ON rooms(status);
