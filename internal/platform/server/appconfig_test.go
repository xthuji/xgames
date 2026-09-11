package server_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"xgames/internal/infra/store"
	"xgames/internal/platform/server"
)

// TestAppConfigLanPlay 验证 /app-config.json 依实际监听地址回显 lan_play：
// 绑定回环 → false；绑定 0.0.0.0 → true。前端据此回显「局域网对战」开关初值。
func TestAppConfigLanPlay(t *testing.T) {
	dbPath := fmt.Sprintf("%s/appconfig-%d.db", t.TempDir(), time.Now().UnixNano())
	st, err := store.Open(dbPath)
	require.NoError(t, err)
	defer st.Close()

	registerTestGames()
	app := server.NewApp(testConfig(), st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ts := httptest.NewServer(app.Handler())
	defer ts.Close()

	fetch := func(t *testing.T) (lanPlay bool, wsURL string) {
		t.Helper()
		resp, err := http.Get(ts.URL + "/app-config.json")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var cfg struct {
			WSURL  string `json:"ws_url"`
			LanRun bool   `json:"lan_play"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&cfg))
		return cfg.LanRun, cfg.WSURL
	}

	app.SetListenAddr("127.0.0.1:3040")
	lan, ws := fetch(t)
	assert.False(t, lan, "回环绑定应 lan_play=false")
	assert.Contains(t, ws, "127.0.0.1")

	app.SetListenAddr("0.0.0.0:3040")
	lan, _ = fetch(t)
	assert.True(t, lan, "0.0.0.0 绑定应 lan_play=true")
}
