#!/usr/bin/env bash
# =============================================================================
# XGames 斗地主 - 游戏模拟测试
# =============================================================================
# 用法:
#   ./tests/run_game_test.sh              # 交互菜单
#   ./tests/run_game_test.sh flow         # WebSocket 流程测试（人机对战）
#   ./tests/run_game_test.sh e2e          # Playwright 浏览器 E2E 测试
#   ./tests/run_game_test.sh all          # 运行全部测试
#   ./tests/run_game_test.sh e2e-headed   # E2E 有头模式（可视化）
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_DIR"

# --- 彩色输出 ---
BLUE='\033[0;34m'; GREEN='\033[0;32m'; RED='\033[0;31m'
YELLOW='\033[1;33m'; BOLD='\033[1m'; NC='\033[0m'
log_info()    { echo -e "${BLUE}[INFO]${NC} $*"; }
log_success() { echo -e "${GREEN}[OK]${NC} $*"; }
log_warn()    { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $*"; }

# --- 依赖检查 ---
check_go() {
  command -v go &>/dev/null || { log_error "Go 未安装"; return 1; }
}

check_npm() {
  command -v npm &>/dev/null || { log_error "Node.js / npm 未安装"; return 1; }
}

# --- 确保后端已构建 ---
ensure_backend() {
  check_go
  log_info "构建后端..."
  go build -o ddz .
  log_success "后端构建完成"
}

# --- 确保前端已构建 ---
ensure_frontend() {
  check_npm
  if [ ! -f "$PROJECT_DIR/web/dist/index.html" ]; then
    log_info "前端未构建，正在构建..."
    cd "$PROJECT_DIR/web"
    npm install --no-audit --no-fund
    npm run build
    cd "$PROJECT_DIR"
    log_success "前端构建完成"
  fi
}

# --- 确保 E2E 依赖已安装 ---
ensure_e2e_deps() {
  check_npm
  if [ ! -d "$SCRIPT_DIR/e2e/node_modules" ]; then
    log_info "安装 E2E 测试依赖..."
    cd "$SCRIPT_DIR/e2e"
    npm install --no-audit --no-fund
    cd "$PROJECT_DIR"
    log_success "E2E 依赖安装完成"
  fi
}

# =============================================================================
# 测试命令
# =============================================================================

# WebSocket 流程测试：连接后端 → 自动叫分出牌 → 验证完整对局链路
cmd_flow() {
  check_go; check_npm
  log_info "=== WebSocket 流程测试 ==="
  log_info "启动后端服务..."

  # 确保后端和前端就绪
  ensure_backend
  ensure_frontend

  # 启动后端（后台）
  local log_file="$SCRIPT_DIR/flow/flow-test.log"
  : > "$log_file"
  ./ddz -no-window -no-open > "$log_file" 2>&1 &
  local server_pid=$!

  # 等待服务就绪
  log_info "等待服务启动..."
  local retries=0
  while ! curl -sf http://localhost:3030/healthz >/dev/null 2>&1; do
    sleep 0.5
    retries=$((retries + 1))
    if [ $retries -ge 20 ]; then
      log_error "服务启动超时"
      kill $server_pid 2>/dev/null || true
      exit 1
    fi
  done
  log_success "服务已就绪 (PID=$server_pid)"

  # 确保测试依赖已安装
  if [ ! -d "$SCRIPT_DIR/flow/node_modules" ]; then
    cd "$SCRIPT_DIR/flow"
    npm install --no-audit --no-fund
    cd "$PROJECT_DIR"
  fi

  # 运行流程测试
  log_info "运行流程测试..."
  local exit_code=0
  cd "$SCRIPT_DIR/flow"
  node practice-flow.mjs || exit_code=$?
  cd "$PROJECT_DIR"

  # 停止后端
  log_info "停止后端..."
  kill $server_pid 2>/dev/null || true
  wait $server_pid 2>/dev/null || true

  if [ $exit_code -eq 0 ]; then
    log_success "流程测试通过"
  else
    log_error "流程测试失败 (exit=$exit_code)"
  fi

  return $exit_code
}

# Playwright 浏览器 E2E 测试
cmd_e2e() {
  check_go; check_npm
  log_info "=== Playwright 浏览器 E2E 测试 ==="

  ensure_backend
  ensure_frontend
  ensure_e2e_deps

  log_info "运行 E2E 测试..."
  cd "$SCRIPT_DIR/e2e"
  local exit_code=0
  npx playwright test "$@" || exit_code=$?
  cd "$PROJECT_DIR"

  if [ $exit_code -eq 0 ]; then
    log_success "E2E 测试通过"
  else
    log_error "E2E 测试失败 (exit=$exit_code)"
  fi

  return $exit_code
}

# E2E 有头模式（可视化调试）
cmd_e2e_headed() {
  check_go; check_npm
  log_info "=== Playwright E2E 测试 (有头模式) ==="

  ensure_backend
  ensure_frontend
  ensure_e2e_deps

  log_info "运行 E2E 测试 (有头模式)..."
  cd "$SCRIPT_DIR/e2e"
  local exit_code=0
  npx playwright test --headed "$@" || exit_code=$?
  cd "$PROJECT_DIR"

  if [ $exit_code -eq 0 ]; then
    log_success "E2E 测试通过"
  else
    log_error "E2E 测试失败 (exit=$exit_code)"
  fi

  return $exit_code
}

# 运行全部测试
cmd_all() {
  log_info "=== 运行全部游戏模拟测试 ==="
  echo ""

  local failed=0

  # 1. WebSocket 流程测试
  log_info "[1/2] WebSocket 流程测试"
  cmd_flow || failed=1
  echo ""

  # 2. E2E 浏览器测试
  log_info "[2/2] Playwright E2E 测试"
  cmd_e2e || failed=1
  echo ""

  if [ $failed -eq 0 ]; then
    log_success "=== 全部测试通过 ==="
  else
    log_error "=== 部分测试失败 ==="
  fi

  return $failed
}

# =============================================================================
# 交互菜单
# =============================================================================
show_menu() {
  echo ""
  echo "=========================================="
  echo "    XGames 斗地主 - 游戏模拟测试"
  echo "=========================================="
  echo "  1|flow)         WebSocket 流程测试（人机对战）"
  echo "  2|e2e)          Playwright 浏览器 E2E 测试"
  echo "  3|e2e-headed)   E2E 有头模式（可视化调试）"
  echo "  4|all)          运行全部测试"
  echo "  0|q|exit)       退出"
  echo "=========================================="
  echo -n "请输入选择 [0-4]: "
}

show_help() {
  cat <<EOF
XGames 斗地主 - 游戏模拟测试

用法: $0 [命令]

命令:
  (无参数)      显示交互菜单
  flow          WebSocket 流程测试（自动叫分出牌，验证完整对局链路）
  e2e           Playwright 浏览器 E2E 测试（UI 自动化 + 状态一致性验证）
  e2e-headed    E2E 有头模式（可视化调试，可看到浏览器操作）
  all           运行全部测试

说明:
  - flow 测试：通过 WebSocket 直连后端，模拟完整人机对战流程
  - e2e 测试：通过 Playwright 控制浏览器，验证 UI 与后端状态一致性
  - 首次运行会自动安装依赖和构建前后端

测试覆盖:
  - 完整人机对战流程（大厅 → 匹配 → 叫分 → 出牌 → 结算）
  - URL hash 与场景切换同步
  - GameScene/RoomScene 无状态时自动回退大厅
  - 玩家身份持久化与清理
EOF
}

main() {
  local cmd="${1:-}"

  if [ -z "$cmd" ]; then
    show_menu
    read -r choice
    cmd="${choice:-}"
  fi

  case "$cmd" in
    1|flow)         cmd_flow ;;
    2|e2e)          cmd_e2e ;;
    3|e2e-headed)   cmd_e2e_headed ;;
    4|all)          cmd_all ;;
    0|q|exit|quit)  echo "退出..."; exit 0 ;;
    help|-h|--help) show_help ;;
    *)              show_help ;;
  esac
}

main "$@"
