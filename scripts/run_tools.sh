#!/usr/bin/env bash
# =============================================================================
# XGames - 构建与运行工具 (跨平台: macOS / Linux / Windows Git Bash)
# =============================================================================
# 命令:
#   1|b,build     构建桌面应用 (当前平台)
#   2|r,run       构建并前台运行 (Ctrl+C 关闭)
#   3|d,dev       开发模式: Go 单进程 + 文件监听自动构建前端
#   4|t,test      运行 单元测试 + 游戏模拟测试
#   5|bot_test    机器人自战测试 (仅修改机器人出牌逻辑时运行)
#   6|c,clean     清理构建产物
#   0|q|exit        退出
# =============================================================================
# 薄入口：公共实现全部在 run_tools_master.sh（跨项目逐字节一致，禁止单项目
# 私改），项目差异全部写在本文件的「常量与命令表」与「项目专属实现」内联区；
# 全部构建/打包/测试逻辑集中在这里，CI workflow 只做 runner 引导与产物上传
#（规范 4.2）。
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_DIR"

# ============================================================
# 公共模块：母版实现（与其余桌面应用逐字节一致，请勿在此修改）
# 先于常量区加载：母版只定义函数不执行业务，差异常量声明在下一区
# ============================================================
source "$SCRIPT_DIR/run_tools_master.sh"
# ============================================================
# 项目差异区 · 常量与命令表（脚本唯一需要按项目调整的部分）
# ============================================================

# --- 项目标识 ---
APP_NAME="XGames"                    # 可执行文件 / Wails outputfilename
BUNDLE_NAME=""              # .app bundle 名（留空同 APP_NAME）
RELEASE_PREFIX=""        # release/ 产物命名前缀（留空同 APP_NAME）
APP_DISPLAY_NAME="XGames"
PROJECT_BUNDLE_ID="com.xthuji.XGames"
export PROJECT_BUNDLE_ID

# --- 目录与产物 ---
BUILD_DIR="$PROJECT_DIR/build"
FRONTEND_DIR="$PROJECT_DIR/web"
# 产物目录：默认 <frontend_dir>/dist；Vite outDir 指到别处（如 easy_diff 直写 embed
# 目录 frontend/dist）时必须按项目覆盖，否则母版 ensure_frontend 会误判为未构建
FRONTEND_DIST="$FRONTEND_DIR/dist"
WAILS_CMD=""
VERSION=""

# --- 构建策略 ---
IS_WAILS=1                     # 0=纯 Go 桌面（自行覆盖 build_impl / get_app_bundle）
PLATFORM_SCOPE="all"        # darwin=仅面向 macOS 产物
MACOS_MIN_VERSION="12.0"
MACOS_UNIVERSAL_DEFAULT=1     # 1=恒产 universal；0=本地单架构 + 环境变量开关
UNIVERSAL_ENV_VAR=""     # 例：EASYDIFF_UNIVERSAL
WAILS_BUILD_CLEAN=0
WAILS_PIN_VERSION=""                    # 留空=对齐 go.mod 的 wails 版本（真源）
NEEDS_CGO=0                   # 1=Windows 构建需要 MinGW-w64
ADHOC_SIGN=1                      # 1=macOS 构建后 ad-hoc 签名
GO_BUILD_LDFLAGS="-s -w"
VERSION_LD_VAR="main.Version"        # 版本注入的 Go 变量（ldflags -X）
LDFLAGS_EXTRA=""
GOTOOLCHAIN_VER=""

# --- 资源与配置 ---
CONFIG_SRC="$PROJECT_DIR/data/config.yaml"
CONFIG_DEST_DIR="data"
ICON_SRC="$PROJECT_DIR/data/icon.png"

# --- 菜单与分发 ---
DEFAULT_CMD="run"
FALLBACK_CMD="cmd_dev"
SRC_FIND_EXPR=( -name '*.go' -o -path '*/web/src/*' -o -name 'VERSION' -o -name 'wails.json' -o -name 'config.yaml' )             # needs_build 的源文件匹配表达式
TEST_PKGS=( ./... )
CLEAN_EXTRA=( web/.vite web/dist )
DEV_CMD=( go run . -dev -no-open )                    # 留空=母版默认 wails dev

# --- 命令表：序号|别名(逗号分隔)|说明|处理函数（菜单/帮助/分发唯一真源）---
commands=(
  "1|b,build|构建桌面应用 (当前平台)|cmd_build"
  "2|r,run|构建并前台运行 (Ctrl+C 关闭)|cmd_run"
  "3|d,dev|开发模式: Go 单进程 + 文件监听自动构建前端|cmd_dev"
  "4|t,test|运行 单元测试 + 游戏模拟测试|cmd_all_test"
  "5|bot_test|机器人自战测试 (仅修改机器人出牌逻辑时运行)|cmd_bot_test"
  "6|c,clean|清理构建产物|cmd_clean"
)

# ============================================================
# 项目专属实现（覆盖母版默认 / 追加项目专属命令）
# 内联区位于常量表之后、main "$@" 之前：顶层可安全展开差异常量
# ============================================================
# dev 模式：Go 单进程托管前端 + 文件监听自动构建（非常规 wails dev）
# 见 DEV_CMD 常量声明；此处补充端口守护进程清理与说明
cmd_all_test() {
  cmd_test
  cmd_game_test
}

cmd_test() {
  check_go
  log_info "运行 Go 单元测试 (-race, 跳过机器人自战测试)..."
  # 常规测试一律排除 TestSelfPlay_100Games：四游戏各 100 局自战耗时数十分钟，
  # 仅在修改机器人出牌/决策逻辑后用 run_tools.sh bot_test 专项运行（验证胜率梯度）。
  run go test -race -count=1 -skip '^TestSelfPlay_100Games$' ./...
  log_success "全部测试通过（自战测试未运行，如需验证机器人胜率请执行: $0 bot_test）"
}

# 机器人自战测试：四游戏各 100 局（TestSelfPlay_100Games）。
# 耗时较长（合计约 30-60 分钟），仅在修改机器人出牌/决策逻辑后运行，验证难度梯度与胜率。
# 通过环境变量 XGAMES_SELFPLAY=1 解锁自战测试，防止 agent 或常规测试误触发。
cmd_bot_test() {
  check_go
  log_info "运行机器人自战测试 (四游戏各 100 局, 预计 30-60 分钟)..."
  # 通过环境变量 XGAMES_SELFPLAY=1 解锁自战测试，防止 agent 或常规测试误触发
  run env XGAMES_SELFPLAY=1 go test -count=1 -timeout 45m -run '^TestSelfPlay_100Games$' -v \
    ./internal/games/ddz/bot/ \
    ./internal/games/mahjong/bot/ \
    ./internal/games/gomoku/bot/ \
    ./internal/games/chess/bot/
  log_success "机器人自战测试通过"
}

# 游戏模拟测试：WebSocket 流程测试 + Playwright 浏览器 E2E 测试
cmd_game_test() {
  log_info "启动游戏模拟测试..."
  run "$PROJECT_DIR/tests/run_game_test.sh" "$@"
}

# wails build 不负责生成协议，前端构建前必须先出 Go → TypeScript 协议
hook_frontend() {
  log_info "生成前端协议 (Go → TypeScript)..."
  run go run ./internal/platform/protocol/gen
}

# dev/守护进程按端口清理（3030），避免残留实例占用监听
kill_game_services() {
  local killed=0 dev_pids=""
  local app_pids
  app_pids=$(pgrep -f "${APP_NAME}" 2>/dev/null || true)
  if [ -n "$app_pids" ]; then
    log_info "停止 $APP_NAME 进程: $app_pids"
    echo "$app_pids" | xargs kill 2>/dev/null || true
    killed=1
  fi

  if command -v lsof &>/dev/null; then
    dev_pids=$(lsof -ti:3030 2>/dev/null || true)
  elif command -v fuser &>/dev/null; then
    dev_pids=$(fuser 3030/tcp 2>/dev/null || true)
  fi
  if [ -n "$dev_pids" ]; then
    log_info "停止 dev 模式进程 (端口 3030): $dev_pids"
    echo "$dev_pids" | xargs kill 2>/dev/null || true
    killed=1
  fi

  if [ "$killed" -eq 1 ]; then
    sleep 1
    log_success "游戏服务已停止"
  else
    log_info "没有运行中的游戏服务"
  fi
}

hook_extra_cleanup() { kill_game_services; }

# 帮助尾部补充开发模式说明
show_help_extra() {
  cat <<'EOF'

开发模式说明:
  - 一条命令搞定前端后端：go run . -dev -no-open
  - 访问 http://localhost:3030
  - 修改 web/src 下的 .ts 等文件，自动触发 npm run build
EOF
}

main "$@"
