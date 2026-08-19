package room

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Waiting 房间快照：编码 → 解码往返一致
func TestRoomSnapshotRoundtrip_Waiting(t *testing.T) {
	rs := &RoomSnapshot{
		RoomCode:  "123456",
		GameID:    "ddz",
		State:     snapStateWaiting,
		CreatedAt: time.Now().Truncate(time.Second),
		Options:   map[string]string{"difficulty": "hard"},
		Players: []RoomPlayer{
			{ID: "p1", Name: "玩家1", Seat: 0, Ready: true},
			{ID: "p2", Name: "玩家2", Seat: 1, IsBot: true, Ready: true},
		},
	}

	encoded, err := encodeSnapshot(rs)
	require.NoError(t, err)
	require.NotEmpty(t, encoded)

	decoded, err := decodeSnapshot(encoded)
	require.NoError(t, err)
	assert.Equal(t, rs.RoomCode, decoded.RoomCode)
	assert.Equal(t, rs.GameID, decoded.GameID)
	assert.Equal(t, rs.State, decoded.State)
	assert.True(t, rs.CreatedAt.Equal(decoded.CreatedAt))
	assert.Equal(t, rs.Options, decoded.Options)
	assert.Equal(t, rs.Players, decoded.Players)
	assert.Nil(t, decoded.Game)
}

// Playing 房间快照：含进行中对局（游戏私有 JSON），往返后原文保留
func TestRoomSnapshotRoundtrip_Playing(t *testing.T) {
	game := []byte(`{"room_code":"123456","phase":"playing","bid_multiplier":4}`)
	rs := &RoomSnapshot{
		RoomCode:  "123456",
		GameID:    "ddz",
		State:     snapStatePlaying,
		CreatedAt: time.Now().Truncate(time.Second),
		Game:      game,
	}

	encoded, err := encodeSnapshot(rs)
	require.NoError(t, err)

	decoded, err := decodeSnapshot(encoded)
	require.NoError(t, err)
	require.NotEmpty(t, decoded.Game)

	// 平台不感知游戏快照结构，原样透传给游戏侧解析
	var gs struct {
		RoomCode      string `json:"room_code"`
		Phase         string `json:"phase"`
		BidMultiplier int    `json:"bid_multiplier"`
	}
	require.NoError(t, json.Unmarshal(decoded.Game, &gs))
	assert.Equal(t, "123456", gs.RoomCode)
	assert.Equal(t, "playing", gs.Phase)
	assert.Equal(t, 4, gs.BidMultiplier)
}

// 旧快照无 game_id：解码后缺省按 "ddz" 恢复（向后兼容）
func TestDecodeSnapshotLegacyGameID(t *testing.T) {
	snap, err := decodeSnapshot(`{"room_code":"123456","state":"waiting"}`)
	require.NoError(t, err)
	assert.Equal(t, "ddz", snap.GameID)
}

// 损坏数据返回 error（调用方丢弃并告警）
func TestDecodeSnapshotCorrupted(t *testing.T) {
	_, err := decodeSnapshot("{invalid-json")
	assert.Error(t, err)
}
