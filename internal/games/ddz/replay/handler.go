package replay

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ReplayFileInfo 复盘报告文件信息
type ReplayFileInfo struct {
	Filename  string `json:"filename"`
	GameID    string `json:"gameId"`
	CreatedAt int64  `json:"createdAt"`
	Size      int64  `json:"size"`
}

// ReplayHandler 复盘报告 HTTP 处理器
type ReplayHandler struct {
	replayDir string
}

// NewReplayHandler 创建复盘报告处理器
func NewReplayHandler(replayDir string) *ReplayHandler {
	// 确保目录存在
	if err := os.MkdirAll(replayDir, 0755); err != nil {
		log.Printf("创建复盘报告目录失败: %v", err)
	}
	
	return &ReplayHandler{
		replayDir: replayDir,
	}
}

// RegisterRoutes 注册 HTTP 路由
func (h *ReplayHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/replays", h.handleListReplays)
	mux.HandleFunc("/api/replays/", h.handleReplayDetail)
}

// resolveDir 根据游戏 ID 解析报告目录。
// 报告由各游戏对局结束后保存到 data/replays/<game>/ 子目录（如 chess/ddz/gomoku/mahjong），
// game 为空时回退到根目录（兼容历史数据与测试）。
func (h *ReplayHandler) resolveDir(game string) string {
	if game == "" {
		return h.replayDir
	}
	// 防目录穿越：仅允许小写字母与数字组成的游戏目录名
	for _, r := range game {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return h.replayDir
		}
	}
	return filepath.Join(h.replayDir, game)
}

// handleListReplays 获取复盘报告列表
// GET /api/replays?limit=20&offset=0&roomCode=xxx
func (h *ReplayHandler) handleListReplays(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 解析查询参数
	limit := 20
	offset := 0
	roomCode := r.URL.Query().Get("roomCode")
	game := r.URL.Query().Get("game")
	playerID := r.URL.Query().Get("playerId")

	if l := r.URL.Query().Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		fmt.Sscanf(o, "%d", &offset)
	}

	// 读取文件列表（按 game 定位到对应游戏的报告子目录）
	// 子目录尚未创建（新环境还没有任何报告落盘）时按空列表处理，
	// 让前端走"暂无复盘报告"重试文案而非加载失败
	files, err := ioutil.ReadDir(h.resolveDir(game))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			files = nil
		} else {
			http.Error(w, "Failed to read replays", http.StatusInternalServerError)
			return
		}
	}

	// 过滤和排序
	var replays []ReplayFileInfo
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".txt") {
			// 解析文件名获取 gameID 和时间戳
			gameID, createdAt := parseFilename(f.Name())
			
			// 如果指定了 roomCode，只返回该房间的报告
			if roomCode != "" && gameID != roomCode {
				continue
			}

			// 如果指定了 playerId，只返回文件名包含该玩家标识的报告
			if playerID != "" && !strings.Contains(f.Name(), playerID) {
				continue
			}
			
			replays = append(replays, ReplayFileInfo{
				Filename:  f.Name(),
				GameID:    gameID,
				CreatedAt: createdAt,
				Size:      f.Size(),
			})
		}
	}

	// 按时间倒序排序
	sort.Slice(replays, func(i, j int) bool {
		return replays[i].CreatedAt > replays[j].CreatedAt
	})

	// 确保返回空数组而不是 null
	if replays == nil {
		replays = []ReplayFileInfo{}
	}

	// 分页
	total := len(replays)
	if offset >= total {
		replays = []ReplayFileInfo{}
	} else {
		end := offset + limit
		if end > total {
			end = total
		}
		replays = replays[offset:end]
	}

	// 返回 JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total": total,
		"items": replays,
	})
}

// handleReplayDetail 获取单个复盘报告详情
// GET /api/replays/{filename}
func (h *ReplayHandler) handleReplayDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 从 URL 路径中提取文件名
	path := r.URL.Path
	filename := strings.TrimPrefix(path, "/api/replays/")

	if filename == "" || strings.Contains(filename, "..") {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}

	// 读取文件内容（game 参数定位到对应游戏的报告子目录）
	dir := h.resolveDir(r.URL.Query().Get("game"))
	fullPath := filepath.Join(dir, filename)
	content, err := ioutil.ReadFile(fullPath)
	if err != nil {
		http.Error(w, "Replay not found", http.StatusNotFound)
		return
	}

	// 返回文本内容
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(content)
}

// parseFilename 解析文件名获取 gameID（即房间码）和时间戳。
// 兼容两种格式：
//   - 旧格式: {roomCode}_{timestamp}.txt
//   - 新格式: {roomCode}_{playerID}_{timestamp}.txt（以真人玩家为中心的复盘报告，每个玩家一份）
func parseFilename(filename string) (string, int64) {
	// 移除 .txt 后缀
	name := strings.TrimSuffix(filename, ".txt")
	
	// 分割最后一部分作为时间戳
	parts := strings.Split(name, "_")
	if len(parts) < 2 {
		return name, 0
	}
	
	// 最后一部分是时间戳
	timestampStr := parts[len(parts)-1]
	var timestamp int64
	fmt.Sscanf(timestampStr, "%d", &timestamp)
	
	// 首段为房间码；新格式的时间戳前一段是 playerID，由列表接口按文件名 Contains 过滤
	return parts[0], timestamp
}
