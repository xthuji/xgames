package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"xgames/internal/infra/config"
)

// settingsPath 返回临时目录下的 settings.json 路径。
func settingsPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "settings.json")
}

// 文件缺失时回落默认（lanPlay=false），不报错。
func TestNewUserSettings_MissingFileDefaultsFalse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "settings.json") // 连目录都不存在
	u := config.NewUserSettings(path, nil)
	if u.LanPlay() {
		t.Fatal("缺省应为 false")
	}
	if got := u.BindHost("127.0.0.1"); got != "127.0.0.1" {
		t.Fatalf("默认应绑定出厂地址，实际 %q", got)
	}
}

// Set/Get 往返 + 落盘后重新读取仍保持一致。
func TestUserSettings_SetGetRoundTrip(t *testing.T) {
	path := settingsPath(t)
	u := config.NewUserSettings(path, nil)

	if err := u.SetLanPlay(true); err != nil {
		t.Fatalf("SetLanPlay: %v", err)
	}
	if !u.LanPlay() {
		t.Fatal("SetLanPlay(true) 后应为 true")
	}
	if got := u.BindHost("127.0.0.1"); got != "0.0.0.0" {
		t.Fatalf("开启后应绑定 0.0.0.0，实际 %q", got)
	}

	// 重新加载应读回持久化值
	u2 := config.NewUserSettings(path, nil)
	if !u2.LanPlay() {
		t.Fatal("重新加载后应仍为 true")
	}
}

// 损坏 JSON 回落默认 false（不 panic、不报错）。
func TestUserSettings_CorruptJSONFallsBack(t *testing.T) {
	path := settingsPath(t)
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	u := config.NewUserSettings(path, nil)
	if u.LanPlay() {
		t.Fatal("损坏文件应回落 false")
	}
}

// 原子写不应残留 .tmp 文件。
func TestUserSettings_NoTempLeftover(t *testing.T) {
	path := settingsPath(t)
	u := config.NewUserSettings(path, nil)
	if err := u.SetLanPlay(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("应无 .tmp 残留，err=%v", err)
	}
}
