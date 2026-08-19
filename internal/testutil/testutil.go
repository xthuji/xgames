// Package testutil 测试公共设施（供各测试包的 TestMain 使用）。
package testutil

import (
	"log/slog"
	"os"
	"path/filepath"
)

// ChdirProjectRoot 将测试进程工作目录切换到项目根（go.mod 所在目录）。
//
// go test 的工作目录是测试包所在目录，而运行时相对路径（如复盘报告
// data/replays、配置中的 data/ddz.db）均以项目根为基准；测试不切换基准
// 会导致报告/数据散落到各包目录下的 data/ 中，与开发运行行为不一致。
// 在 TestMain 中于 m.Run 之前调用一次即可。
func ChdirProjectRoot() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if err := os.Chdir(dir); err != nil {
				slog.Warn("测试目录切换失败，相对路径仍按测试包目录解析", "dir", dir, "err", err)
			}
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return // 已到文件系统根，未找到 go.mod
		}
		dir = parent
	}
}
