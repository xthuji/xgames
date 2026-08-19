package games

import (
	"context"
	"testing"

	"xgames/internal/platform/protocol"
)

// stubGame 最小游戏描述符桩（注册表单测用）
type stubGame struct{ id string }

func (s *stubGame) ID() string                        { return s.id }
func (s *stubGame) Name() string                      { return s.id }
func (s *stubGame) MinPlayers() int                   { return 2 }
func (s *stubGame) MaxPlayers() int                   { return 2 }
func (s *stubGame) SupportsBots() bool                { return false }
func (s *stubGame) NewBotController() BotController   { return nil }
func (s *stubGame) NewSession(env SessionEnv) Session { return nil }
func (s *stubGame) RestoreSession(env SessionEnv, snapshot []byte) (Session, error) {
	return nil, nil
}

// withTestRegistry 隔离全局注册表执行测试（避免污染其他用例）
func withTestRegistry(t *testing.T, fn func()) {
	t.Helper()
	regMu.Lock()
	saved := registry
	registry = map[string]Game{}
	regMu.Unlock()
	defer func() {
		regMu.Lock()
		registry = saved
		regMu.Unlock()
	}()
	fn()
}

// TestRegistryGet 注册后按 ID 查询
func TestRegistryGet(t *testing.T) {
	withTestRegistry(t, func() {
		Register(&stubGame{id: "aaa"})
		Register(&stubGame{id: "bbb"})

		if g, ok := Get("aaa"); !ok || g.ID() != "aaa" {
			t.Fatalf("按 ID 查询失败: %v %v", g, ok)
		}
		if _, ok := Get("nope"); ok {
			t.Fatal("未注册游戏不应命中")
		}
	})
}

// TestRegistryList 列表按 ID 排序
func TestRegistryList(t *testing.T) {
	withTestRegistry(t, func() {
		Register(&stubGame{id: "bbb"})
		Register(&stubGame{id: "aaa"})

		list := List()
		if len(list) != 2 || list[0].ID() != "aaa" || list[1].ID() != "bbb" {
			t.Fatalf("列表应按 ID 排序: %v", list)
		}
	})
}

// TestRegistryDuplicatePanics 重复注册应 panic（程序错误，启动期暴露）
func TestRegistryDuplicatePanics(t *testing.T) {
	withTestRegistry(t, func() {
		Register(&stubGame{id: "aaa"})
		defer func() {
			if recover() == nil {
				t.Fatal("重复注册应 panic")
			}
		}()
		Register(&stubGame{id: "aaa"})
	})
}

// TestCodeOf 错误码提取：携带时用其码，否则回退未知错误
func TestCodeOf(t *testing.T) {
	if got := CodeOf(NewCodedError(3002, "")); got != 3002 {
		t.Fatalf("CodeOf = %d, want 3002", got)
	}
	if got := CodeOf(context.Canceled); got != protocol.ErrCodeUnknown {
		t.Fatalf("CodeOf = %d, want %d", got, protocol.ErrCodeUnknown)
	}
}

// TestRegisterErrorMessages 游戏私有错误文案并入平台表
func TestRegisterErrorMessages(t *testing.T) {
	RegisterErrorMessages(map[int]string{9901: "测试文案"})
	defer delete(protocol.ErrorMessages, 9901)
	if protocol.ErrorMessages[9901] != "测试文案" {
		t.Fatal("错误文案未并入平台表")
	}
}
