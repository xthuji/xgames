package config

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// lanPlayHost 开启局域网对战时使用的监听地址（所有网卡）。
const lanPlayHost = "0.0.0.0"

// UserSettings 用户级运行开关，持久化到用户数据目录下的 settings.json。
//
// 与 config.yaml 分离的原因：config.yaml 随 .app 打包、每次升级/重拷会被覆盖，
// 只能承载出厂默认；本结构承载用户可改的运行期偏好（当前仅“局域网对战”），
// 必须落在升级后仍保留的位置。进程启动时读取，用于决定实际监听地址。
type UserSettings struct {
	path string

	mu      sync.RWMutex
	lanPlay bool
}

// userSettingsFile settings.json 的磁盘结构。
type userSettingsFile struct {
	LanPlay bool `json:"lan_play"`
}

// NewUserSettings 读取 path 处的设置。文件缺失或损坏时回落默认（lanPlay=false，
// 仅本机监听）——首次启动本就没有该文件，不视为错误，仅记日志。
func NewUserSettings(path string, logger *slog.Logger) *UserSettings {
	u := &UserSettings{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) && logger != nil {
			logger.Warn("读取用户设置失败，回落默认", "path", path, "err", err)
		}
		return u
	}
	var f userSettingsFile
	if err := json.Unmarshal(data, &f); err != nil {
		if logger != nil {
			logger.Warn("解析用户设置失败，回落默认", "path", path, "err", err)
		}
		return u
	}
	u.lanPlay = f.LanPlay
	return u
}

// LanPlay 返回是否开启局域网对战。
func (u *UserSettings) LanPlay() bool {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.lanPlay
}

// SetLanPlay 更新内存值并原子落盘（临时文件 + rename），避免半写损坏。
func (u *UserSettings) SetLanPlay(v bool) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.lanPlay = v

	data, err := json.MarshalIndent(userSettingsFile{LanPlay: v}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(u.path), 0o755); err != nil {
		return err
	}
	tmp := u.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, u.path)
}

// BindHost 依据开关决定实际监听地址：开启局域网对战返回 0.0.0.0（所有网卡，
// 允许同网段设备接入，但会触发 macOS 应用防火墙授权弹窗）；否则返回传入的默认
// 地址（通常 127.0.0.1，仅本机访问、不触发弹窗）。
func (u *UserSettings) BindHost(defaultHost string) string {
	if u.LanPlay() {
		return lanPlayHost
	}
	return defaultHost
}
