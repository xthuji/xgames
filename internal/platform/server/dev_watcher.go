package server

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// EnsureFrontendBuilt 开发模式启动时确保前端已构建：
// 若 web/dist/index.html 不存在，自动执行 npm install + npm run build。
func EnsureFrontendBuilt(webDir string, logger *slog.Logger) error {
	indexFile := filepath.Join(webDir, "dist", "index.html")
	if _, err := os.Stat(indexFile); err == nil {
		return nil
	}

	logger.Info("前端 dist 不存在，开始首次构建...", "dir", webDir)

	if _, err := os.Stat(filepath.Join(webDir, "node_modules")); os.IsNotExist(err) {
		logger.Info("首次启动：安装前端依赖 (npm install)...")
		cmd := exec.Command("npm", "install", "--no-audit", "--no-fund")
		cmd.Dir = webDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("npm install 失败: %w", err)
		}
	}

	logger.Info("执行 npm run build...")
	cmd := exec.Command("npm", "run", "build")
	cmd.Dir = webDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("npm run build 失败: %w", err)
	}

	logger.Info("前端构建完成 ✓")
	return nil
}

// DevWatcher 监听前端文件变化并自动触发构建
type DevWatcher struct {
	webDir  string
	logger  *slog.Logger
	watcher *fsnotify.Watcher
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	mu       sync.Mutex
	building bool
	timer    *time.Timer
}

// NewDevWatcher 创建文件监听器
func NewDevWatcher(webDir string, logger *slog.Logger) (*DevWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &DevWatcher{
		webDir:  webDir,
		logger:  logger,
		watcher: w,
	}, nil
}

// Start 启动监听（异步）
func (d *DevWatcher) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel

	// 监听 web/src 目录（递归添加子目录）
	srcDir := filepath.Join(d.webDir, "src")
	if err := d.addRecursive(srcDir); err != nil {
		return fmt.Errorf("添加监听目录失败: %w", err)
	}

	// 监听关键文件
	extraFiles := []string{
		filepath.Join(d.webDir, "index.html"),
		filepath.Join(d.webDir, "package.json"),
		filepath.Join(d.webDir, "vite.config.ts"),
		filepath.Join(d.webDir, "tsconfig.json"),
	}
	for _, f := range extraFiles {
		if _, err := os.Stat(f); err == nil {
			if err := d.watcher.Add(f); err != nil {
				d.logger.Warn("添加文件监听失败", "file", f, "err", err)
			}
		}
	}

	d.logger.Info("开发模式：前端文件监听已启动", "src", srcDir)

	d.wg.Add(1)
	go d.watchLoop(ctx)

	return nil
}

// Stop 停止监听
func (d *DevWatcher) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
	if d.timer != nil {
		d.timer.Stop()
	}
	d.watcher.Close()
	d.wg.Wait()
}

func (d *DevWatcher) addRecursive(dir string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && !strings.HasPrefix(info.Name(), ".") && info.Name() != "node_modules" {
			return d.watcher.Add(path)
		}
		return nil
	})
}

func (d *DevWatcher) watchLoop(ctx context.Context) {
	defer d.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-d.watcher.Events:
			if !ok {
				return
			}
			d.handleEvent(event)
		case err, ok := <-d.watcher.Errors:
			if !ok {
				return
			}
			d.logger.Warn("文件监听错误", "err", err)
		}
	}
}

func (d *DevWatcher) handleEvent(event fsnotify.Event) {
	ext := strings.ToLower(filepath.Ext(event.Name))

	if !isBuildRelevant(ext) {
		return
	}

	d.logger.Info("检测到前端文件变化", "op", event.Op.String(), "name", filepath.Base(event.Name))
	d.scheduleBuild()
}

func isBuildRelevant(ext string) bool {
	switch ext {
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs",
		".html", ".css", ".scss", ".sass", ".less",
		".json":
		return true
	}
	return false
}

// scheduleBuild 防抖触发构建（1 秒）
func (d *DevWatcher) scheduleBuild() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.building {
		return
	}
	if d.timer != nil {
		d.timer.Stop()
	}

	d.timer = time.AfterFunc(1*time.Second, func() {
		d.triggerBuild()
	})
}

func (d *DevWatcher) triggerBuild() {
	d.mu.Lock()
	if d.building {
		d.mu.Unlock()
		return
	}
	d.building = true
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		d.building = false
		d.mu.Unlock()
	}()

	start := time.Now()
	d.logger.Info("前端文件变化，开始构建...")

	cmd := exec.Command("npm", "run", "build")
	cmd.Dir = d.webDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		d.logger.Error("前端构建失败", "err", err)
		return
	}

	d.logger.Info("前端构建完成 ✓", "duration", time.Since(start).Round(time.Millisecond))
}
