package replay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupTestHandler(t *testing.T) (*ReplayHandler, string) {
	// 创建临时目录用于测试
	tempDir := t.TempDir()
	
	handler := NewReplayHandler(tempDir)
	
	// 创建一些测试用的复盘报告文件（文件名包含player ID以便过滤）
	createTestReplayFile(t, tempDir, "room_001_player1_1693824000.txt", "test report 1")
	createTestReplayFile(t, tempDir, "room_002_player1_1693824100.txt", "test report 2 player1")
	createTestReplayFile(t, tempDir, "room_003_player2_1693824200.txt", "test report 3 player2")
	
	return handler, tempDir
}

func createTestReplayFile(t *testing.T, dir, filename, content string) {
	filepath := filepath.Join(dir, filename)
	err := os.WriteFile(filepath, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
}

func TestHandleListReplays(t *testing.T) {
	handler, _ := setupTestHandler(t)
	
	tests := []struct {
		name       string
		url        string
		wantStatus int
		wantCount  int
	}{
		{
			name:       "获取所有报告",
			url:        "/api/replays",
			wantStatus: http.StatusOK,
			wantCount:  3,
		},
		{
			name:       "限制返回数量",
			url:        "/api/replays?limit=2",
			wantStatus: http.StatusOK,
			wantCount:  2,
		},
		{
			name:       "分页查询",
			url:        "/api/replays?offset=1&limit=2",
			wantStatus: http.StatusOK,
			wantCount:  2,
		},
		{
			name:       "按玩家过滤",
			url:        "/api/replays?playerId=player1",
			wantStatus: http.StatusOK,
			wantCount:  2, // room_002 和 room_003 包含 player1
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			w := httptest.NewRecorder()
			
			handler.handleListReplays(w, req)
			
			if w.Code != tt.wantStatus {
				t.Errorf("handleListReplays() status = %v, want %v", w.Code, tt.wantStatus)
			}
			
			var response map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatalf("Failed to parse response: %v", err)
			}
			
			items, ok := response["items"].([]interface{})
			if !ok {
				t.Fatal("Response items is not an array")
			}
			
			if len(items) != tt.wantCount {
				t.Errorf("handleListReplays() count = %v, want %v", len(items), tt.wantCount)
			}
		})
	}
}

// 新环境报告子目录尚未创建时（还没有任何报告落盘），列表应返回空数组而非 500，
// 否则前端会提示“加载复盘报告失败”而非“暂无复盘报告”
func TestHandleListReplaysMissingGameDir(t *testing.T) {
	tempDir := t.TempDir()
	handler := NewReplayHandler(tempDir) // 仅创建根目录，不创建 <game>/ 子目录

	req := httptest.NewRequest(http.MethodGet, "/api/replays?game=ddz", nil)
	w := httptest.NewRecorder()
	handler.handleListReplays(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("handleListReplays() status = %d, want %d, body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var response struct {
		Total int              `json:"total"`
		Items []ReplayFileInfo `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	if response.Total != 0 || len(response.Items) != 0 {
		t.Errorf("期望空列表, got total=%d items=%d", response.Total, len(response.Items))
	}
}

func TestHandleReplayDetail(t *testing.T) {
	handler, _ := setupTestHandler(t)
	
	tests := []struct {
		name       string
		filename   string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "获取存在的报告",
			filename:   "room_001_player1_1693824000.txt",
			wantStatus: http.StatusOK,
			wantBody:   "test report 1",
		},
		{
			name:       "获取不存在的报告",
			filename:   "nonexistent.txt",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "非法文件名",
			filename:   "../etc/passwd",
			wantStatus: http.StatusBadRequest,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/api/replays/" + tt.filename
			req := httptest.NewRequest(http.MethodGet, url, nil)
			w := httptest.NewRecorder()
			
			handler.handleReplayDetail(w, req)
			
			if w.Code != tt.wantStatus {
				t.Errorf("handleReplayDetail() status = %v, want %v", w.Code, tt.wantStatus)
			}
			
			if tt.wantBody != "" && w.Code == http.StatusOK {
				if w.Body.String() != tt.wantBody {
					t.Errorf("handleReplayDetail() body = %v, want %v", w.Body.String(), tt.wantBody)
				}
			}
		})
	}
}

func TestHandleListReplaysByGame(t *testing.T) {
	handler, tempDir := setupTestHandler(t)

	// 模拟各游戏报告保存结构：data/replays/<game>/<roomCode>_<timestamp>.txt
	for _, g := range []string{"chess", "ddz", "gomoku", "mahjong"} {
		gameDir := filepath.Join(tempDir, g)
		if err := os.MkdirAll(gameDir, 0755); err != nil {
			t.Fatalf("Failed to create game dir: %v", err)
		}
		createTestReplayFile(t, gameDir, "048439_1788429920.txt", "report for "+g)
	}

	// 按 game 过滤：只返回对应游戏子目录的报告
	req := httptest.NewRequest(http.MethodGet, "/api/replays?limit=1&roomCode=048439&game=chess", nil)
	w := httptest.NewRecorder()
	handler.handleListReplays(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("handleListReplays() status = %v, want %v", w.Code, http.StatusOK)
	}
	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	items, ok := response["items"].([]interface{})
	if !ok {
		t.Fatal("Response items is not an array")
	}
	if len(items) != 1 {
		t.Fatalf("handleListReplays(game=chess) count = %v, want 1", len(items))
	}
	item := items[0].(map[string]interface{})
	if item["filename"] != "048439_1788429920.txt" {
		t.Errorf("filename = %v, want 048439_1788429920.txt", item["filename"])
	}

	// 未知 game 不越界：回退到根目录（根目录无 048439 房间的报告 → 空）
	req2 := httptest.NewRequest(http.MethodGet, "/api/replays?roomCode=048439&game=../etc", nil)
	w2 := httptest.NewRecorder()
	handler.handleListReplays(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("handleListReplays(malicious game) status = %v, want %v", w2.Code, http.StatusOK)
	}
}

func TestHandleReplayDetailByGame(t *testing.T) {
	handler, tempDir := setupTestHandler(t)

	gameDir := filepath.Join(tempDir, "mahjong")
	if err := os.MkdirAll(gameDir, 0755); err != nil {
		t.Fatalf("Failed to create game dir: %v", err)
	}
	createTestReplayFile(t, gameDir, "620183_1788429720.txt", "mahjong replay report")

	// 带 game 参数读取子目录报告
	req := httptest.NewRequest(http.MethodGet, "/api/replays/620183_1788429720.txt?game=mahjong", nil)
	w := httptest.NewRecorder()
	handler.handleReplayDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("handleReplayDetail(game=mahjong) status = %v, want %v", w.Code, http.StatusOK)
	}
	if w.Body.String() != "mahjong replay report" {
		t.Errorf("handleReplayDetail() body = %v, want mahjong replay report", w.Body.String())
	}

	// 不带 game 参数时找不到该文件（报告在子目录中）
	req2 := httptest.NewRequest(http.MethodGet, "/api/replays/620183_1788429720.txt", nil)
	w2 := httptest.NewRecorder()
	handler.handleReplayDetail(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Errorf("handleReplayDetail(no game) status = %v, want %v", w2.Code, http.StatusNotFound)
	}
}

func TestParseFilename(t *testing.T) {
	tests := []struct {
		name          string
		filename      string
		wantGameID    string
		wantTimestamp int64
	}{
		{
			name:          "旧格式（房间码_时间戳）",
			filename:      "123456_1693824000.txt",
			wantGameID:    "123456",
			wantTimestamp: 1693824000,
		},
		{
			name:          "新格式（房间码_玩家ID_时间戳）",
			filename:      "123456_a1b2c3d4e5f60718a9b0c1d2e3f40516_1693824000.txt",
			wantGameID:    "123456",
			wantTimestamp: 1693824000,
		},
		{
			name:          "无时间戳",
			filename:      "invalid.txt",
			wantGameID:    "invalid",
			wantTimestamp: 0,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gameID, timestamp := parseFilename(tt.filename)
			
			if gameID != tt.wantGameID {
				t.Errorf("parseFilename() gameID = %v, want %v", gameID, tt.wantGameID)
			}
			
			if timestamp != tt.wantTimestamp {
				t.Errorf("parseFilename() timestamp = %v, want %v", timestamp, tt.wantTimestamp)
			}
		})
	}
}

func TestNewReplayHandler(t *testing.T) {
	tempDir := t.TempDir()
	
	handler := NewReplayHandler(tempDir)
	
	if handler == nil {
		t.Fatal("NewReplayHandler() returned nil")
	}
	
	if handler.replayDir != tempDir {
		t.Errorf("NewReplayHandler() replayDir = %v, want %v", handler.replayDir, tempDir)
	}
}

func TestRegisterRoutes(t *testing.T) {
	handler := NewReplayHandler(t.TempDir())
	
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	
	// 验证路由是否已注册
	tests := []string{
		"/api/replays",
		"/api/replays/",
	}
	
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			
			// 这应该能找到一个处理器（即使是405错误也说明路由存在）
			mux.ServeHTTP(w, req)
			
			// 如果返回404，说明路由未注册
			if w.Code == http.StatusNotFound {
				t.Errorf("Route %s is not registered", path)
			}
		})
	}
}

func TestHandleListReplaysMethodNotAllowed(t *testing.T) {
	handler := NewReplayHandler(t.TempDir())
	
	req := httptest.NewRequest(http.MethodPost, "/api/replays", nil)
	w := httptest.NewRecorder()
	
	handler.handleListReplays(w, req)
	
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleListReplays() with POST status = %v, want %v", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleReplayDetailMethodNotAllowed(t *testing.T) {
	handler := NewReplayHandler(t.TempDir())
	
	req := httptest.NewRequest(http.MethodPost, "/api/replays/test.txt", nil)
	w := httptest.NewRecorder()
	
	handler.handleReplayDetail(w, req)
	
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleReplayDetail() with POST status = %v, want %v", w.Code, http.StatusMethodNotAllowed)
	}
}

// 性能测试
func BenchmarkHandleListReplays(b *testing.B) {
	tempDir := b.TempDir()
	handler := NewReplayHandler(tempDir)
	
	// 创建100个测试文件
	for i := 0; i < 100; i++ {
		filename := filepath.Join(tempDir, fmt.Sprintf("room_%d_%d.txt", i, time.Now().Unix()))
		os.WriteFile(filename, []byte("test content"), 0644)
	}
	
	req := httptest.NewRequest(http.MethodGet, "/api/replays?limit=20", nil)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		handler.handleListReplays(w, req)
	}
}
