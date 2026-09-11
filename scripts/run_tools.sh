#!/usr/bin/env bash
# =============================================================================
# XGames - 构建与运行工具 (跨平台: macOS / Linux / Windows Git Bash)
# =============================================================================
# 命令:
#   1|b|build    构建桌面应用 (Wails 生产构建, 自动适配当前平台)
#   2|r|run      构建并前台运行 (Ctrl+C 关闭)
#   3|d|dev      开发模式: Go 单进程 + 文件监听自动构建前端
#   4|t|test     运行 单元测试 + 游戏模拟测试
#   5|b|bot_test 机器人自战测试 (100 局/游戏, 仅修改机器人出牌逻辑时运行)
#   6|c|clean    清理构建产物
#   0|q|exit     退出
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_DIR"

# --- 项目常量 ---
APP_NAME="XGames"
BUILD_DIR="$PROJECT_DIR/build"
FRONTEND_DIR="$PROJECT_DIR/web"
FRONTEND_DIST="$FRONTEND_DIR/dist"
WAILS_CMD=""
VERSION=""
PROJECT_BUNDLE_ID="com.xthuji.XGames"
export PROJECT_BUNDLE_ID

# --- 平台检测 ---
HOST_OS=""
HOST_ARCH=""
EXT=""

detect_platform() {
  local os
  os="$(uname -s 2>/dev/null || echo "unknown")"
  case "$os" in
    Darwin*)  HOST_OS="darwin"  ;;
    Linux*)   HOST_OS="linux"   ;;
    MINGW*|MSYS*|CYGWIN*) HOST_OS="windows" ;;
    *)        HOST_OS="unknown" ;;
  esac

  local arch
  arch="$(uname -m 2>/dev/null || echo "unknown")"
  case "$arch" in
    x86_64|amd64)   HOST_ARCH="amd64" ;;
    arm64|aarch64)   HOST_ARCH="arm64" ;;
    *)               HOST_ARCH="amd64" ;;
  esac

  if [ "$HOST_OS" = "windows" ]; then
    EXT=".exe"
  else
    EXT=""
  fi
}

detect_platform

# --- 彩色输出 ---
BLUE='\033[0;34m'; GREEN='\033[0;32m'; RED='\033[0;31m'
YELLOW='\033[1;33m'; BOLD='\033[1m'; NC='\033[0m'
log_info()    { echo -e "${BLUE}[INFO]${NC} $*"; }
log_success() { echo -e "${GREEN}[OK]${NC} $*"; }
log_warn()    { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

# ============================================================
# 依赖检查
# ============================================================
check_go() {
  command -v go &>/dev/null || log_error "Go 未安装 (https://go.dev/dl)"
}

check_npm() {
  command -v npm &>/dev/null || log_error "Node.js / npm 未安装 (https://nodejs.org)"
}

check_wails() {
  if [ -n "$WAILS_CMD" ]; then return 0; fi
  if [ -x "$HOME/go/bin/wails" ]; then
    WAILS_CMD="$HOME/go/bin/wails"
  elif [ -n "${USERPROFILE:-}" ] && [ -x "$USERPROFILE/go/bin/wails.exe" ] 2>/dev/null; then
    WAILS_CMD="$USERPROFILE/go/bin/wails"
  elif command -v wails &>/dev/null; then
    WAILS_CMD="wails"
  else
    log_warn "Wails CLI 未安装，正在安装..."
    go install github.com/wailsapp/wails/v2/cmd/wails@latest
    WAILS_CMD="$HOME/go/bin/wails"
  fi
  [ -x "$WAILS_CMD" ] || log_error "Wails CLI 安装失败"
}

# ============================================================
# 构建依赖准备 (跨平台，全部在脚本内完成，CI workflow 不再写系统依赖)
#   Linux:   apt 安装 GTK3 / WebKit2GTK(+pkg-config/gcc/zip)，并为
#            Ubuntu 24.04+ 生成 webkit2gtk-4.0.pc 兼容别名 (Wails 硬编码 4.0)
#   Windows: 探测/安装 MinGW-w64 (CGO 所需 gcc)
#   macOS:   校验 clang (Xcode Command Line Tools)
# ============================================================
# 非 root 且有 sudo 时才用 sudo（CI runner 上是免密 sudo）
SUDO=""
if [ "$(id -u 2>/dev/null || echo 0)" != "0" ] && command -v sudo &>/dev/null; then
  SUDO="sudo"
fi

APT_INDEX_REFRESHED=0
apt_refresh_once() {
  [ "${APT_INDEX_REFRESHED:-0}" = "1" ] && return 0
  if ! command -v apt-get &>/dev/null; then
    log_warn "当前系统未检测到 apt-get，跳过自动依赖安装。请确保已手动安装: pkg-config libgtk-3-dev libwebkit2gtk-4.0-dev"
    return 1
  fi
  APT_INDEX_REFRESHED=1
  log_info "刷新 apt 索引 ..."
  $SUDO apt-get update -qq || log_warn "apt-get update 未完全成功，继续尝试安装"
  return 0
}

apt_has() {
  apt_refresh_once || return 1
  apt-cache show "$1" >/dev/null 2>&1
}

apt_install() {
  apt_refresh_once || return 1
  log_info "安装构建依赖: $*"
  # shellcheck disable=SC2086
  $SUDO apt-get install -y --no-install-recommends "$@" || log_error "apt-get install 失败: $*"
}

ensure_linux_deps() {
  local need=() webkit_alias=0

  command -v pkg-config &>/dev/null        || need+=("pkg-config")
  command -v gcc &>/dev/null               || need+=("build-essential")
  command -v zip &>/dev/null               || need+=("zip")
  pkg-config --exists gtk+-3.0 2>/dev/null || need+=("libgtk-3-dev")

  # Wails 在 Linux 依赖 webkit2gtk-4.0；Ubuntu 24.04+ 仅提供 4.1，需要生成 .pc 兼容别名
  if pkg-config --exists webkit2gtk-4.0 2>/dev/null; then
    : # 已可用
  elif pkg-config --exists webkit2gtk-4.1 2>/dev/null; then
    webkit_alias=1
  elif apt_has libwebkit2gtk-4.0-dev; then
    need+=("libwebkit2gtk-4.0-dev")
  elif apt_has libwebkit2gtk-4.1-dev; then
    need+=("libwebkit2gtk-4.1-dev"); webkit_alias=1
  else
    log_warn "未能自动匹配到 libwebkit2gtk 包，请确认开发环境依赖"
  fi

  [ ${#need[@]} -eq 0 ] || apt_install "${need[@]}"

  if [ "$webkit_alias" = "1" ]; then
    local pc_dir
    pc_dir="$(pkg-config --variable=pcfiledir webkit2gtk-4.1 2>/dev/null || true)"
    if [ -n "$pc_dir" ] && [ -f "$pc_dir/webkit2gtk-4.1.pc" ]; then
      if [ ! -f "$pc_dir/webkit2gtk-4.0.pc" ]; then
        log_info "生成 webkit2gtk-4.0.pc 兼容别名 → $pc_dir"
        $SUDO cp "$pc_dir/webkit2gtk-4.1.pc" "$pc_dir/webkit2gtk-4.0.pc" \
          || log_warn "无法写入 .pc 别名，Wails 构建可能因缺失 pkg-config 配置而失败"
      fi
    else
      log_warn "未定位到 webkit2gtk-4.1.pc（pcfiledir=$pc_dir）"
    fi
  fi

  if ! pkg-config --cflags --libs gtk+-3.0 webkit2gtk-4.0 >/dev/null 2>&1; then
    log_error "pkg-config 无法解析 gtk+-3.0/webkit2gtk-4.0：$(pkg-config --errors --exists gtk+-3.0 webkit2gtk-4.0 2>&1 | head -3)"
  fi
  log_success "Linux 构建依赖就绪 (webkit2gtk-4.0 → $(pkg-config --modversion webkit2gtk-4.0))"
}

MINGW_DIRS=(
  /c/msys64/ucrt64/bin
  /c/msys64/mingw64/bin
  /c/tools/mingw64/bin
  /c/mingw64/bin
  /c/TDM-GCC-64/bin
)

locate_mingw() {
  if command -v gcc &>/dev/null; then
    log_info "gcc 已就绪: $(command -v gcc)"
    return 0
  fi
  local d
  for d in "${MINGW_DIRS[@]}"; do
    if [ -x "$d/gcc.exe" ] || [ -x "$d/gcc" ]; then
      export PATH="$d:$PATH"
      gcc --version >/dev/null 2>&1 || { PATH="${PATH#$d:}"; return 1; }
      log_info "将 MinGW-w64 加入 PATH: $d"
      return 0
    fi
  done
  return 1
}

ensure_windows_deps() {
  locate_mingw && return 0
  command -v choco &>/dev/null || log_error "Wails(Windows) 需要 gcc（CGO 必需）。请安装 MinGW-w64 并保证其在 PATH 中"
  log_info "通过 chocolatey 安装 MinGW-w64 ..."
  choco install mingw -y --no-progress || log_error "choco install mingw 失败"
  locate_mingw || log_error "MinGW-w64 安装后仍找不到 gcc"
  log_success "Windows 构建依赖就绪"
}

ensure_build_deps() {
  case "$HOST_OS" in
    linux)   ensure_linux_deps ;;
    windows) ensure_windows_deps ;;
    darwin)  command -v clang &>/dev/null || log_error "缺少 clang，请安装 Xcode Command Line Tools: xcode-select --install" ;;
  esac
}

# ============================================================
# 版本管理
# ============================================================
get_version() {
  if [ -n "$VERSION" ]; then echo "$VERSION"; return; fi
  if [ -f "VERSION" ]; then
    VERSION=$(tr -d '[:space:]' < VERSION)
  else
    VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
  fi
  echo "$VERSION"
}

# 跨平台 sed -i: macOS 需要 -i ''，Linux/Git Bash 需要 -i
portable_sed_i() {
  if [ "$HOST_OS" = "darwin" ]; then
    sed -i '' "$@"
  else
    sed -i "$@"
  fi
}

sync_wails_version() {
  local version
  version=$(get_version)
  local wails_json="$PROJECT_DIR/wails.json"
  [ -f "$wails_json" ] || return 0

  local current
  current=$(grep '"productVersion"' "$wails_json" | head -1 | sed 's/.*: *"\([^"]*\)".*/\1/')
  if [ "$current" = "$version" ]; then
    return 0
  fi

  portable_sed_i "s|\"productVersion\": *\"[^\"]*\"|\"productVersion\": \"$version\"|" "$wails_json"
  portable_sed_i "s|\"version\": *\"[^\"]*\"|\"version\": \"$version\"|" "$wails_json"
  log_info "版本已同步到 wails.json: $version"
}

# ============================================================
# 平台特定路径
# ============================================================
get_build_binary() {
  if [ "$HOST_OS" = "windows" ]; then
    echo "$BUILD_DIR/bin/${APP_NAME}.exe"
  else
    echo "$BUILD_DIR/bin/${APP_NAME}"
  fi
}

get_wails_platform() {
  case "$HOST_OS" in
    darwin)  echo "darwin/universal" ;;
    linux)   echo "linux/${HOST_ARCH}" ;;
    windows) echo "windows/${HOST_ARCH}" ;;
    *)       echo "unknown" ;;
  esac
}

# macOS only: 同步 config.yaml 到 .app/Resources/data/
sync_config() {
  [ "$HOST_OS" = "darwin" ] || return 0
  local app_path="$BUILD_DIR/bin/${APP_NAME}.app"
  local resources="$app_path/Contents/Resources"
  [ -d "$app_path" ] || return 0
  [ -f "$PROJECT_DIR/data/config.yaml" ] || return 0
  mkdir -p "$resources/data"
  cp -f "$PROJECT_DIR/data/config.yaml" "$resources/data/config.yaml"
  log_info "data/config.yaml → $resources/data/"
}

# macOS only: 修复 Info.plist 中的 Bundle ID 和版本号
fix_app_info() {
  [ "$HOST_OS" = "darwin" ] || return 0
  local version
  version=$(get_version)
  local plist="$BUILD_DIR/bin/${APP_NAME}.app/Contents/Info.plist"
  [ -f "$plist" ] || return 0

  local changed=0
  local cur_bundle cur_ver cur_min_ver
  cur_bundle=$(plutil -extract CFBundleIdentifier raw "$plist" 2>/dev/null || echo "")
  cur_ver=$(plutil -extract CFBundleShortVersionString raw "$plist" 2>/dev/null || echo "")
  cur_min_ver=$(plutil -extract LSMinimumSystemVersion raw "$plist" 2>/dev/null || echo "")

  if [ "$cur_bundle" != "$PROJECT_BUNDLE_ID" ]; then
    plutil -replace CFBundleIdentifier -string "$PROJECT_BUNDLE_ID" "$plist"
    changed=1
  fi
  if [ "$cur_ver" != "$version" ]; then
    plutil -replace CFBundleShortVersionString -string "$version" "$plist"
    plutil -replace CFBundleVersion -string "$version" "$plist"
    changed=1
  fi
  # 强制锁定最低 macOS 版本为 12.0 (Go 1.26 最后一个支持 macOS 12 的版本)
  if [ "$cur_min_ver" != "12.0.0" ]; then
    plutil -replace LSMinimumSystemVersion -string "12.0.0" "$plist"
    changed=1
  fi

  if [ "$changed" -eq 1 ]; then
    log_info "Info.plist 已更新: version=$version bundle=$PROJECT_BUNDLE_ID minVersion=12.0.0"
  fi
}

# ============================================================
# 构建步骤
# ============================================================
gen_protocol() {
  log_info "生成前端协议 (Go → TypeScript)..."
  go run ./internal/platform/protocol/gen
}

build_frontend() {
  log_info "构建前端 (Vite)..."
  check_npm
  cd "$FRONTEND_DIR"
  if [ ! -d node_modules ]; then
    log_info "首次安装前端依赖..."
    npm install --no-audit --no-fund
  fi
  npm run build
  cd "$PROJECT_DIR"
  log_success "前端构建完成 → $FRONTEND_DIST ($(du -sh "$FRONTEND_DIST" | cut -f1))"
}

# 确保协议和前端 dist 已就绪（wails build 不负责生成协议）
ensure_frontend() {
  gen_protocol
  if ! ls "$FRONTEND_DIST"/*.js 1>/dev/null 2>&1; then
    build_frontend
  fi
}

# 同步 App 图标：data/icon.png → build/appicon.png
sync_app_icon() {
  local src_icon="$PROJECT_DIR/data/icon.png"
  local dst_icon="$BUILD_DIR/appicon.png"
  mkdir -p "$BUILD_DIR"
  if [ -f "$src_icon" ]; then
    cp -f "$src_icon" "$dst_icon"
    log_info "图标已同步: data/icon.png → build/appicon.png"
  else
    log_warn "未找到图标源文件 $src_icon，使用默认图标"
  fi
}

# 将构建产物复制到 release 目录
sync_release() {
  local release_dir="$PROJECT_DIR/release"
  mkdir -p "$release_dir"

  if [ "$HOST_OS" = "darwin" ]; then
    local app_path="$BUILD_DIR/bin/${APP_NAME}.app"
    local release_app="${release_dir}/${APP_NAME}.app"
    [ -d "$app_path" ] || return 0
    rm -rf "$release_app"
    cp -R "$app_path" "$release_app"
    log_success "Release App  → $release_app"
  else
    local bin_path
    bin_path=$(get_build_binary)
    [ -f "$bin_path" ] || return 0
    local release_bin="${release_dir}/${APP_NAME}${EXT}"
    cp -f "$bin_path" "$release_bin"
    chmod +x "$release_bin" 2>/dev/null || true
    log_success "Release Bin  → $release_bin"
  fi
}

# macOS only: 用 hdiutil 生成 DMG（文件名带版本号）
build_dmg() {
  [ "$HOST_OS" = "darwin" ] || return 0
  local version
  version=$(get_version)
  local app_path="$BUILD_DIR/bin/${APP_NAME}.app"
  local release_dir="$PROJECT_DIR/release"
  local release_app="${release_dir}/${APP_NAME}.app"
  local release_dmg="${release_dir}/${APP_NAME}_v${version}.dmg"

  # 优先使用 release 目录下已同步的 app，否则使用构建目录的
  local source_app="$release_app"
  [ -d "$source_app" ] || source_app="$app_path"
  [ -d "$source_app" ] || return 0

  mkdir -p "$release_dir"
  rm -f "$release_dmg"

  log_info "生成 DMG: ${APP_NAME}_v${version}.dmg ..."
  hdiutil create \
    -volname "$APP_NAME" \
    -srcfolder "$source_app" \
    -ov \
    -format UDZO \
    "$release_dmg" > /dev/null

  log_success "Release DMG  → $release_dmg"
}

# Windows / Linux: 生成 zip 包 (含可执行文件 + config.yaml + icon)
build_zip() {
  [ "$HOST_OS" = "darwin" ] && return 0
  local version
  version=$(get_version)
  local release_dir="$PROJECT_DIR/release"
  local zip_name="${APP_NAME}_v${version}_${HOST_OS}_${HOST_ARCH}.zip"
  local zip_path="${release_dir}/${zip_name}"
  local stage_dir="${BUILD_DIR}/zip_stage"

  rm -rf "$stage_dir"
  mkdir -p "$stage_dir"

  local bin_path
  bin_path=$(get_build_binary)
  [ -f "$bin_path" ] || return 0
  cp -f "$bin_path" "$stage_dir/"

  if [ -f "$PROJECT_DIR/data/config.yaml" ]; then
    mkdir -p "$stage_dir/data"
    cp -f "$PROJECT_DIR/data/config.yaml" "$stage_dir/data/config.yaml"
  fi

  if [ -f "$PROJECT_DIR/data/icon.png" ]; then
    cp -f "$PROJECT_DIR/data/icon.png" "$stage_dir/icon.png"
  fi

  mkdir -p "$release_dir"
  rm -f "$zip_path"

  log_info "生成 ZIP: ${zip_name} ..."
  if command -v zip &>/dev/null; then
    (cd "$stage_dir" && zip -r "$zip_path" . > /dev/null)
  elif [ "$HOST_OS" = "windows" ] && command -v powershell &>/dev/null; then
    local stage_win zip_win
    stage_win=$(cygpath -w "$stage_dir" 2>/dev/null || echo "$stage_dir")
    zip_win=$(cygpath -w "$zip_path" 2>/dev/null || echo "$zip_path")
    powershell -NoProfile -Command "Compress-Archive -Path '${stage_win}\\*' -DestinationPath '${zip_win}' -Force"
  else
    rm -rf "$stage_dir"
    log_error "zip 命令不可用，请安装 zip 或使用 Windows PowerShell"
  fi
  rm -rf "$stage_dir"

  log_success "Release ZIP  → $zip_path"
}

# ============================================================
# 增量构建检查
# ============================================================

# 检查是否需要重新构建：比较源文件修改时间与产物二进制
# 返回 0 = 需要构建, 1 = 可跳过
needs_build() {
  local binary
  binary=$(get_build_binary)

  # 产物不存在，必须构建
  [ -f "$binary" ] || return 0

  # 检查源文件是否比产物更新
  # 扫描范围: Go 源码、前端源码、配置/版本文件
  # 排除: build/(产物)、node_modules/(依赖)、web/dist/(生成物)、.git/
  # 注意: -o 优先级低于隐式 AND，必须用 \(\) 分组源文件匹配条件
  local newer
  newer=$(find "$PROJECT_DIR" \
    \( -name '*.go' -o -path '*/web/src/*' -o -name 'VERSION' -o -name 'wails.json' -o -name 'config.yaml' \) \
    -not -path '*/build/*' \
    -not -path '*/node_modules/*' \
    -not -path '*/web/dist/*' \
    -not -path '*/.git/*' \
    -newer "$binary" \
    -print -quit 2>/dev/null)

  [ -n "$newer" ]
}

# ============================================================
# 命令实现
# ============================================================

kill_game_services() {
  local killed=0

  if [ "$HOST_OS" = "darwin" ] || [ "$HOST_OS" = "linux" ]; then
    local app_pids
    app_pids=$(pgrep -f "${APP_NAME}" 2>/dev/null || true)
    if [ -n "$app_pids" ]; then
      log_info "停止 $APP_NAME 进程: $app_pids"
      echo "$app_pids" | xargs kill 2>/dev/null || true
      killed=1
    fi

    local dev_pids
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
  fi

  if [ "$killed" -eq 1 ]; then
    sleep 1
    log_success "游戏服务已停止"
  else
    log_info "没有运行中的游戏服务"
  fi
}

kill_existing() {
  local pids
  pids=$(pgrep -f "${APP_NAME}" 2>/dev/null || true)
  if [ -n "$pids" ]; then
    log_info "停止已有 App 进程: $pids"
    echo "$pids" | xargs kill 2>/dev/null || true
    sleep 1
    pids=$(pgrep -f "${APP_NAME}" 2>/dev/null || true)
    if [ -n "$pids" ]; then
      echo "$pids" | xargs kill -9 2>/dev/null || true
    fi
    log_success "旧进程已停止"
  fi
}

cmd_build() {
  check_go; check_npm; ensure_build_deps; check_wails
  local version
  version=$(get_version)
  sync_wails_version
  ensure_frontend

  local platform
  platform=$(get_wails_platform)
  log_info "构建 $APP_NAME ($platform, version=$version)..."
  sync_app_icon

  # macOS: 强制兼容 macOS 12 Monterey
  # Go 1.26 是最后一个支持 macOS 12 的版本，且 clang 默认会使用当前 SDK 版本，
  # 不设置会导致二进制 LC_BUILD_VERSION.minos 高于目标系统版本。
  local ldflags="-s -w -X main.Version=${version}"
  if [ "$HOST_OS" = "darwin" ]; then
    export MACOSX_DEPLOYMENT_TARGET="12.0"
    export CGO_CFLAGS="-mmacosx-version-min=12.0"
    export CGO_LDFLAGS="-mmacosx-version-min=12.0"
    log_info "macOS 兼容目标: MACOSX_DEPLOYMENT_TARGET=12.0"
  fi

  "$WAILS_CMD" build -platform "$platform" -ldflags "$ldflags"
  log_success "构建完成"

  sync_config
  fix_app_info
  sync_release

  if [ "$HOST_OS" = "darwin" ]; then
    build_dmg
  else
    build_zip
  fi

  local bin_path
  bin_path=$(get_build_binary)
  if [ -f "$bin_path" ]; then
    log_success "构建产物 → $bin_path"
  elif [ "$HOST_OS" = "darwin" ] && [ -d "$BUILD_DIR/bin/${APP_NAME}.app" ]; then
    log_success "构建产物 → $BUILD_DIR/bin/${APP_NAME}.app"
  fi
}

cmd_run() {
  if [ "$HOST_OS" = "windows" ]; then
    log_error "run 命令不支持 Windows，请直接运行 build/bin/${APP_NAME}.exe"
  fi

  local log_file="$BUILD_DIR/run.log"
  mkdir -p "$BUILD_DIR"

  kill_existing

  if needs_build; then
    cmd_build
  else
    log_success "源文件无变化，跳过构建"
  fi

  local binary
  if [ "$HOST_OS" = "darwin" ]; then
    binary="$BUILD_DIR/bin/${APP_NAME}.app/Contents/MacOS/${APP_NAME}"
  else
    binary=$(get_build_binary)
  fi

  log_info "启动 $APP_NAME (前台模式)..."
  log_info "按 Ctrl+C 关闭 App"
  echo ""

  : > "$log_file"
  "$binary" 2>&1 | tee -a "$log_file"
  local exit_code=${PIPESTATUS[0]}

  echo ""
  if [ "$exit_code" -eq 0 ]; then
    log_success "App 已正常退出"
  else
    log_warn "App 退出码: $exit_code"
  fi
}

# dev 模式：直接 go run -dev，Go 单进程托管前端 + 监听文件变化自动构建
cmd_dev() {
  check_go
  mkdir -p "$BUILD_DIR"
  local log_file="$BUILD_DIR/dev.log"

  log_info "启动开发模式..."
  log_info "Go 单进程：HTTP + WS + 前端托管 + 文件监听自动构建"
  log_info "打开浏览器访问 http://localhost:3030"
  log_info "按 Ctrl+C 停止"
  echo ""

  : > "$log_file"
  go run . -dev -no-open 2>&1 | tee -a "$log_file"
  local exit_code=${PIPESTATUS[0]}

  echo ""
  if [ "$exit_code" -eq 0 ] || [ "$exit_code" -eq 130 ]; then
    log_success "开发模式已停止"
  else
    log_warn "退出码: $exit_code"
  fi
}

cmd_test() {
  check_go
  log_info "运行 Go 单元测试 (-race, 跳过机器人自战测试)..."
  # 常规测试一律排除 TestSelfPlay_100Games：四游戏各 100 局自战耗时数十分钟，
  # 仅在修改机器人出牌/决策逻辑后用 run_tools.sh bot 专项运行（验证胜率梯度）。
  go test -race -count=1 -skip '^TestSelfPlay_100Games$' ./...
  log_success "全部测试通过（自战测试未运行，如需验证机器人胜率请执行: $0 bot）"
}

# 机器人自战测试：四游戏各 100 局（TestSelfPlay_100Games）。
# 耗时较长（合计约 30-60 分钟），仅在修改机器人出牌/决策逻辑后运行，验证难度梯度与胜率。
# 通过环境变量 XGAMES_SELFPLAY=1 解锁自战测试，防止 agent 或常规测试误触发。
cmd_bot_test() {
  check_go
  log_info "运行机器人自战测试 (四游戏各 100 局, 预计 30-60 分钟)..."
  XGAMES_SELFPLAY=1 go test -count=1 -timeout 45m -run '^TestSelfPlay_100Games$' -v \
    ./internal/games/ddz/bot/ \
    ./internal/games/mahjong/bot/ \
    ./internal/games/gomoku/bot/ \
    ./internal/games/chess/bot/
  log_success "机器人自战测试通过"
}

# 游戏模拟测试：WebSocket 流程测试 + Playwright 浏览器 E2E 测试
cmd_game_test() {
  log_info "启动游戏模拟测试..."
  "$PROJECT_DIR/tests/run_game_test.sh" "$@"
}

cmd_clean() {
  log_info "清理构建产物..."
  kill_game_services
  rm -rf build dist web/dist web/.vite release
  find . -name '*.test' -delete 2>/dev/null || true
  log_success "清理完成"
}

# ============================================================
# 交互菜单
# ============================================================
commands=(
  "1|b|build    |构建桌面应用 (当前平台)"
  "2|r|run      |构建并前台运行 (Ctrl+C 关闭)"
  "3|d|dev      |开发模式: Go 单进程 + 文件监听自动构建前端"
  "4|t|test     |运行 单元测试 + 游戏模拟测试"
  "5|b|bot_test |机器人自战测试 (仅修改机器人出牌逻辑时运行)"
  "6|c|clean    |清理构建产物"
  "0|q|exit     |退出"
)

show_menu() {
  echo ""
  echo "=========================================="
  echo "      XGames - 构建工具 ($HOST_OS/$HOST_ARCH)"
  echo "=========================================="
  for c in "${commands[@]}"; do
    IFS='|' read -r num name desc <<< "$c"
    printf "  %s) %-10s %s\n" "$num" "$name" "$desc"
  done
  echo "=========================================="
  echo -n "请输入选择 [0-6]: "
}

show_help() {
  cat <<EOF
XGames 构建工具

用法: $0 [命令]

当前平台: $HOST_OS/$HOST_ARCH

命令:
  (无参数)    显示交互菜单（回车默认 run）
  1|build     构建桌面应用 ($(get_wails_platform))
  2|run       构建并前台运行 App (Ctrl+C 关闭)
  3|dev       开发模式: Go 单进程 + 文件监听自动构建前端
  4|test      运行 单元测试 + 游戏模拟测试
  5|bot_test  机器人自战测试 (四游戏各 100 局, 仅修改机器人出牌逻辑时运行)
  6|clean     清理构建产物
  0|exit      退出

平台说明:
  macOS:   构建 .app + .dmg (darwin/universal)
  Linux:   构建 ELF 二进制 + .zip (linux/$HOST_ARCH)
  Windows: 构建 .exe + .zip (windows/$HOST_ARCH)

开发模式说明:
  - 一条命令搞定前端后端：go run . -dev -no-open
  - 访问 http://localhost:3030
  - 修改 web/src 下的 .ts 等文件，自动触发 npm run build

项目路径:
  构建产物:  $(get_build_binary)
  Release:   release/
  运行日志:  $BUILD_DIR/run.log
  配置文件:  $PROJECT_DIR/data/config.yaml
  版本文件:  $PROJECT_DIR/VERSION
  Bundle ID: $PROJECT_BUNDLE_ID
EOF
}

main() {
  local cmd="${1:-}"

  if [ -z "$cmd" ]; then
    show_menu
    read -r choice
    cmd="${choice:-run}"
  fi

  case "$cmd" in
    1|b|build)      cmd_build ;;
    2|r|run|"")     cmd_run ;;
    3|d|dev)        cmd_dev ;;
    4|t|test)       cmd_test && cmd_game_test ;;
    5|b|bot_test)   cmd_bot_test ;;
    6|c|clean)      cmd_clean ;;
    0|q|exit|quit)  echo "退出..."; exit 0 ;;
    help|-h|--help) show_help ;;
    *)              echo "默认 dev 模式"; cmd_dev ;;
  esac
}

main "$@"
