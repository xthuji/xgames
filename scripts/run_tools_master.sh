# =============================================================================
# run_tools_master.sh - 跨项目公共实现模块（母版）
# =============================================================================
# 由各项目 scripts/run_tools.sh 薄入口 source 加载，不直接执行。
# 本文件随 9 个桌面应用仓库分发（git 跟踪），各项目副本必须逐字节一致；
# 唯一编辑入口：skill assets 母版 + gen_run_tools.py 重新分发 + verify_all.sh 对账，
# 禁止在任何单个项目里私自修改本文件（source 机制下结构上不再存在副本分叉）。
# 需要项目特化时，一律在入口脚本的「常量与命令表」或「专属实现模块」中声明
#（Bash 同名函数后定义覆盖，覆盖点清单见 SKILL 3.6），保证 9 个桌面应用
# 共享同一套构建流程。
# =============================================================================

# --- 平台检测 ---
HOST_OS=""
HOST_ARCH=""
EXT=""
# --platform=<os>/<arch> 会改写 HOST_OS/HOST_ARCH，跨 OS/跨架构判定需保留真实宿主值
NATIVE_OS=""
NATIVE_ARCH=""

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
    x86_64|amd64)  HOST_ARCH="amd64" ;;
    arm64|aarch64) HOST_ARCH="arm64" ;;
    *)             HOST_ARCH="amd64" ;;
  esac

  if [ "$HOST_OS" = "windows" ]; then
    EXT=".exe"
  else
    EXT=""
  fi

  NATIVE_OS="$HOST_OS"
  NATIVE_ARCH="$HOST_ARCH"
}

detect_platform

# --- 彩色输出（风格 A：标签式，日志量大靠标签过滤）---
BLUE='\033[0;34m'; GREEN='\033[0;32m'; RED='\033[0;31m'
YELLOW='\033[1;33m'; BOLD='\033[1m'; NC='\033[0m'
log_info()    { echo -e "${BLUE}[INFO]${NC} $*"; }
log_success() { echo -e "${GREEN}[OK]${NC} $*"; }
log_warn()    { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

# dry-run：由 main 解析 --dry-run 置位；所有外部命令一律经 run() 派发
# （直接传参数数组，绝不 eval，避免内容被 shell 二次解析）
DRY_RUN=false
run() {
  if $DRY_RUN; then
    log_warn "[dry-run] $*"
    return 0
  fi
  "$@"
}

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
    # 版本默认对齐 go.mod 的 wails 依赖（单一真源），避免 CLI 与库版本漂移
    local pin="${WAILS_PIN_VERSION:-$(wails_version_from_gomod)}"
    run go install "github.com/wailsapp/wails/v2/cmd/wails@${pin:-latest}"
    WAILS_CMD="$HOME/go/bin/wails"
  fi
  [ -x "$WAILS_CMD" ] || log_error "Wails CLI 安装失败"
}

# 从 go.mod 读 wails v2 依赖版本（形如 v2.10.2），读不到返回空由调用方兜底 latest
wails_version_from_gomod() {
  awk '/^[[:space:]]*github.com\/wailsapp\/wails\/v2[[:space:]]/{print $2; exit}' \
    "$PROJECT_DIR/go.mod" 2>/dev/null || true
}

# ============================================================
# 构建依赖准备（跨平台，全部在脚本内完成，CI workflow 只做 runner 引导）
#   Linux:   apt 安装 GTK3 / WebKit2GTK(+pkg-config/gcc/zip)，并为
#            Ubuntu 24.04+ 生成 webkit2gtk-4.0.pc 兼容别名 (Wails 硬编码 4.0)
#   Windows: 仅 CGO 项目探测/安装 MinGW-w64；纯 Go 走 WebView2 无需 gcc
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
  DEBIAN_FRONTEND=noninteractive $SUDO apt-get install -y --no-install-recommends "$@" \
    || log_error "apt-get install 失败: $*"
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
      log_warn "未定位到 webkit2gtk-4.1.pc（pcfiledir=${pc_dir}）"
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
  # Wails v2 在 Windows 使用系统 WebView2，纯 Go 构建不需要 gcc/CGO；
  # 仅当项目显式声明 NEEDS_CGO=1（如内嵌 sqlite）时才准备 MinGW。
  if [ "${NEEDS_CGO:-0}" != "1" ]; then
    log_info "Windows: 纯 Go 构建（WebView2），无需 CGO 工具链"
    return 0
  fi
  locate_mingw && return 0
  command -v choco &>/dev/null || log_error "Wails(Windows) CGO 构建需要 gcc。请安装 MinGW-w64 并保证其在 PATH 中"
  log_info "通过 chocolatey 安装 MinGW-w64 ..."
  choco install mingw -y --no-progress || log_error "choco install mingw 失败"
  locate_mingw || log_error "MinGW-w64 安装后仍找不到 gcc"
  log_success "Windows 构建依赖就绪"
}

ensure_macos_deps() {
  command -v clang &>/dev/null \
    || log_error "缺少 clang，请安装 Xcode Command Line Tools: xcode-select --install"
}

# 桌面产物依赖宿主本机工具链（Linux 的 GTK/WebKit、macOS 的 Cocoa/WebKit），
# 无法跨 OS 编译；Windows 在非 CGO 项目（纯 Go 链接）下允许从其他 OS 交叉产出。
cross_os_guard() {
  [ "$HOST_OS" = "$NATIVE_OS" ] && return 0
  case "$HOST_OS" in
    windows) [ "${NEEDS_CGO:-0}" != "1" ] && return 0 ;;
  esac
  log_error "不支持跨 OS 构建 ${NATIVE_OS}/${NATIVE_ARCH} → ${HOST_OS}/${HOST_ARCH}：${HOST_OS} 产物需本机 C 工具链，请在 ${HOST_OS} 机器或对应 CI runner 上构建（同 OS 跨架构可以）"
}

ensure_build_deps() {
  # --skip-deps（母版全局选项）：环境已就绪时跳过系统依赖探测/安装
  if [ "${SKIP_DEPS:-false}" = "true" ]; then
    log_info "--skip-deps: 跳过系统构建依赖自动安装"
    return 0
  fi
  case "$HOST_OS" in
    linux)   ensure_linux_deps ;;
    windows) ensure_windows_deps ;;
    darwin)  ensure_macos_deps ;;
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

# VERSION 是版本唯一真源，构建前同步进 wails.json（幂等，已一致则跳过）
# wails.json 缺 productVersion 时补写进 info 块，保证 tag 版本 = 产物版本
sync_wails_version() {
  local version
  version=$(get_version)
  local wails_json="$PROJECT_DIR/wails.json"
  [ -f "$wails_json" ] || return 0

  if ! grep -q '"productVersion"' "$wails_json"; then
    if grep -q '"info" *:' "$wails_json"; then
      if $DRY_RUN; then
        log_warn "[dry-run] wails.json 补写 productVersion: $version"
        return 0
      fi
      portable_sed_i "s|\"info\": *{|\"info\": {\"productVersion\": \"$version\",|" "$wails_json"
      log_info "wails.json 补写 productVersion: $version"
    else
      log_warn "wails.json 无 productVersion 且无 info 块，跳过版本同步"
    fi
    return 0
  fi

  local current
  current=$(grep '"productVersion"' "$wails_json" | head -1 | sed 's/.*: *"\([^"]*\)".*/\1/')
  if [ "$current" = "$version" ]; then
    return 0
  fi

  if $DRY_RUN; then
    log_warn "[dry-run] wails.json productVersion/version → $version"
    return 0
  fi
  portable_sed_i "s|\"productVersion\": *\"[^\"]*\"|\"productVersion\": \"$version\"|" "$wails_json"
  portable_sed_i "s|\"version\": *\"[^\"]*\"|\"version\": \"$version\"|" "$wails_json"
  log_info "版本已同步到 wails.json: $version"
}

# ============================================================
# 平台特定路径
# ============================================================
get_app_bundle() {
  echo "$BUILD_DIR/bin/${BUNDLE_NAME:-$APP_NAME}.app"
}

get_build_binary() {
  if [ "$HOST_OS" = "windows" ]; then
    echo "$BUILD_DIR/bin/${APP_NAME}.exe"
  elif [ "$HOST_OS" = "darwin" ] && [ -f "$(get_app_bundle)/Contents/MacOS/${APP_NAME}" ]; then
    # Wails 在 macOS 上的真实二进制位于 .app 内，build/bin/APP 并不存在；
    # needs_build 以它为时间基准，指错路径会导致每次都误判为需要重建
    echo "$(get_app_bundle)/Contents/MacOS/${APP_NAME}"
  else
    echo "$BUILD_DIR/bin/${APP_NAME}"
  fi
}

get_wails_platform() {
  case "$HOST_OS" in
    darwin)  echo "darwin/$(macos_arch)" ;;
    linux)   echo "linux/${HOST_ARCH}" ;;
    windows) echo "windows/${HOST_ARCH}" ;;
    *)       echo "unknown" ;;
  esac
}

# macOS 架构策略：MACOS_UNIVERSAL_DEFAULT=1 恒产 universal（与 CI 产物一致）；
# 声明为 0 时本地只编当前架构（编译量减半），CI 用 ${UNIVERSAL_ENV_VAR}=1 显式请求 universal
macos_arch() {
  if [ "${MACOS_UNIVERSAL_DEFAULT:-1}" = "1" ] || [ "${MACOS_UNIVERSAL_FORCE:-0}" = "1" ]; then
    echo "universal"; return
  fi
  if [ -n "${UNIVERSAL_ENV_VAR:-}" ] && [ "${!UNIVERSAL_ENV_VAR:-0}" = "1" ]; then
    echo "universal"; return
  fi
  echo "$HOST_ARCH"
}

# macOS only: 同步配置文件到 .app/Contents/Resources/${CONFIG_DEST_DIR}/
# 无配置文件的项目 CONFIG_SRC 留空即自动跳过
sync_config() {
  [ "$HOST_OS" = "darwin" ] || return 0
  local app_path resources
  app_path=$(get_app_bundle)
  resources="$app_path/Contents/Resources"
  [ -d "$app_path" ] || return 0
  [ -n "${CONFIG_SRC:-}" ] && [ -f "$CONFIG_SRC" ] || return 0
  local dest_dir="$resources"
  [ -n "${CONFIG_DEST_DIR:-}" ] && dest_dir="$resources/$CONFIG_DEST_DIR"
  mkdir -p "$dest_dir"
  run cp -f "$CONFIG_SRC" "$dest_dir/$(basename "$CONFIG_SRC")"
  log_info "$(basename "$CONFIG_SRC") → $dest_dir/"
}

# macOS only: 修复 Info.plist 中的 Bundle ID / 版本号 / 最低系统版本
# 一律用 plutil 读写，不用 sed 改 plist（sed 匹配到 </key> 之间的空白会破坏结构）
fix_app_info() {
  [ "$HOST_OS" = "darwin" ] || return 0
  local version
  version=$(get_version)
  local plist
  plist="$(get_app_bundle)/Contents/Info.plist"
  [ -f "$plist" ] || return 0

  local changed=0
  local cur_bundle cur_ver cur_name cur_min_ver
  cur_bundle=$(plutil -extract CFBundleIdentifier raw "$plist" 2>/dev/null || echo "")
  cur_ver=$(plutil -extract CFBundleShortVersionString raw "$plist" 2>/dev/null || echo "")
  cur_name=$(plutil -extract CFBundleName raw "$plist" 2>/dev/null || echo "")
  cur_min_ver=$(plutil -extract LSMinimumSystemVersion raw "$plist" 2>/dev/null || echo "")

  if [ "$cur_bundle" != "$PROJECT_BUNDLE_ID" ]; then
    run plutil -replace CFBundleIdentifier -string "$PROJECT_BUNDLE_ID" "$plist"
    changed=1
  fi
  if [ "$cur_ver" != "$version" ]; then
    run plutil -replace CFBundleShortVersionString -string "$version" "$plist"
    run plutil -replace CFBundleVersion -string "$version" "$plist"
    changed=1
  fi
  if [ "$cur_name" != "$APP_NAME" ]; then
    run plutil -replace CFBundleName -string "$APP_NAME" "$plist"
    changed=1
  fi
  # 强制锁定最低 macOS 版本（Go 1.26 是最后一个支持 macOS 12 的版本）
  if [ "$cur_min_ver" != "${MACOS_MIN_VERSION}.0" ]; then
    run plutil -replace LSMinimumSystemVersion -string "${MACOS_MIN_VERSION}.0" "$plist"
    changed=1
  fi

  if [ "$changed" -eq 1 ]; then
    log_info "Info.plist 已更新: version=$version bundle=$PROJECT_BUNDLE_ID minVersion=${MACOS_MIN_VERSION}.0"
  fi
}

# ============================================================
# 构建步骤
# ============================================================
# 同步 App 图标：${ICON_SRC} → build/appicon.png（Wails 构建时自动转 .icns）
sync_app_icon() {
  mkdir -p "$BUILD_DIR"
  if [ -n "${ICON_SRC:-}" ] && [ -f "$ICON_SRC" ]; then
    run cp -f "$ICON_SRC" "$BUILD_DIR/appicon.png"
    log_info "图标已同步: ${ICON_SRC#$PROJECT_DIR/} → build/appicon.png"
  else
    log_warn "未找到图标源文件 ${ICON_SRC:-<未配置>}，使用默认图标"
  fi
}

# 前端增量标记：上一次成功构建前端的时间点（build/ 不入库，clean 后自然失效）。
# 不能再用「dist/*.js 是否存在」判定：Vite 产物在 dist/assets/ 下，顶层启发式
# 对 easy_diff / easy_image / easy_rss 恒为假，会每次重复全量构建。
# 本模块是纯定义模块（先于差异常量被 source）：顶层不展开任何差异常量，
# FRONTEND_STAMP / FRONTEND_STATE / SRC_STATE 在 main() 入口时赋值
FRONTEND_STAMP=""
FRONTEND_STATE=""
# 本次进程内前端是否真的重建过：cmd_build 据此强制重新编译，
# 保证 go:embed 的 dist 内容一定进入二进制
FRONTEND_REBUILT=0

# 前端源码清单（排除依赖 / 产物 / 生成绑定，排序后逐行输出）
# wailsjs 必须排除：wails 每次 build 都会无条件重写 bindings，
# 本套项目前端在运行期通过 window.go 取用绑定，生成物不参与打包
frontend_file_list() {
  find "$FRONTEND_DIR" \
    \( -name node_modules -o -name dist -o -name wailsjs -o -name .git \) -prune -o \
    -type f -print 2>/dev/null | LC_ALL=C sort
}

build_frontend() {
  log_info "构建前端 (Vite)..."
  check_npm
  cd "$FRONTEND_DIR"
  if [ ! -d node_modules ]; then
    log_info "首次安装前端依赖..."
    run npm install --no-audit --no-fund
  fi
  run npm run build
  cd "$PROJECT_DIR"
  FRONTEND_REBUILT=1
  if $DRY_RUN; then return 0; fi
  mkdir -p "$BUILD_DIR"
  touch "$FRONTEND_STAMP"
  # 清单写失败不让一次成功的前端构建失败（下次会按清单缺失再跑一次，宁可多跑不可漏跑）
  frontend_file_list > "$FRONTEND_STATE" || true
  log_success "前端构建完成 → ${FRONTEND_DIST#$PROJECT_DIR/} ($(du -sh "$FRONTEND_DIST" | cut -f1))"
}

# 前端源码是否有改动（相对上次成功构建）
#   - 排除 node_modules / dist：依赖与产物不算源码
#   - 排除 wailsjs：wails 每次 build 都会无条件重写 bindings，而本套项目前端是在
#     运行期通过 window.go 取用绑定（只有 runtime 是静态文件），生成的 wailsjs 不参与
#     打包；若算作源码，会导致每次构建都误判为“前端有改动”
frontend_stale() {
  [ "${IS_WAILS:-1}" = "1" ] || return 1
  [ -d "$FRONTEND_DIR" ] || return 1
  # 没有 package.json = 前端为入库静态资源（class_manager / easy_download / launchpad），
  # 不存在 npm 构建，恒为「未变」，静态资源变更由 needs_build 的 SRC_FIND_EXPR 负责
  [ -f "$FRONTEND_DIR/package.json" ] || return 1
  [ -f "$FRONTEND_STAMP" ] || return 0
  [ -d "$FRONTEND_DIST" ] || return 0
  # 清单缺失（首次引入 / 上一次构建未落盘）按有改动处理，避免漏掉期间删除的文件
  [ -f "$FRONTEND_STATE" ] || return 0
  if ! diff -q <(frontend_file_list) "$FRONTEND_STATE" >/dev/null 2>&1; then
    return 0
  fi
  [ -n "$(find "$FRONTEND_DIR" \
      \( -name node_modules -o -name dist -o -name wailsjs -o -name .git \) -prune -o \
      -type f -newer "$FRONTEND_STAMP" -print -quit 2>/dev/null)" ]
}

# 母版默认钩子：项目差异区「专属实现」中定义同名函数即覆盖
#   hook_frontend      前端构建前的项目前置产物（Go→TS 协议生成、资源抽取等）
#   hook_post_build    编译完成后、归档前的附加动作（内嵌资源、额外签名等）
#   hook_extra_cleanup clean 时 kill_existing 之后、删目录之前的附加动作（按端口杀守护进程等）
hook_frontend()        { :; }
hook_post_build()      { :; }
hook_extra_cleanup()   { :; }

# 确保前置产物与前端 dist 已就绪（wails build 不负责生成协议）
# 前端只在源码有改动时构建；wails 侧由 build_impl 的 -s 跳过重复构建
ensure_frontend() {
  hook_frontend
  if frontend_stale; then
    build_frontend
  else
    log_info "前端未变更，跳过构建（清单与 mtime 均未变，基准 ${FRONTEND_STAMP#$PROJECT_DIR/}）"
  fi
}

# macOS only: ad-hoc 签名（个人项目无 Developer ID，不公证）
# 顺序固定：清 xattr → 内嵌 Mach-O 逐个签 → 整包签 → 校验。
# 坑：codesign --deep 不会给 Resources 下的内嵌工具补签名（实测仍 not signed at all），
# 而未签名的内嵌二进制、以及仓库文件带进来的 quarantine 属性，都会让下载分发的
# App 被 Gatekeeper 判为「已损坏，无法打开」——必须在打包源头处理。
adhoc_sign() {
  [ "$HOST_OS" = "darwin" ] || return 0
  [ "${ADHOC_SIGN:-0}" = "1" ] || return 0
  command -v codesign &>/dev/null || { log_warn "无 codesign，跳过签名"; return 0; }
  local target
  target=$(get_app_bundle)
  [ -d "$target" ] || target=$(get_build_binary)
  if [ -d "$target" ]; then
    # 仓库里拷进来的文件可能带 Sourcetree/浏览器留下的 quarantine，原样进 bundle 会污染安装包
    run xattr -cr "$target" 2>/dev/null || true
    local f
    while IFS= read -r f; do
      case "$(file -b "$f" 2>/dev/null)" in
        *Mach-O*) run codesign --force --sign - --timestamp=none "$f" 2>/dev/null \
                    || log_warn "内嵌二进制签名失败: ${f#"$target"/}" ;;
      esac
    done < <(find "$target/Contents" -type f -perm -u+x 2>/dev/null)
  fi
  run codesign --force --sign - "$target" || log_warn "ad-hoc 签名失败（本机运行可忽略）"
  # 校验门禁：签名已坏却继续打包，下载端只会看到「已损坏」，极难回溯
  if ! $DRY_RUN; then
    codesign --verify --deep --strict "$target" >/dev/null 2>&1 \
      || log_warn "签名校验未通过: $(basename "$target")（分发前必须排查，否则安装后无法打开）"
  fi
}

# 将构建产物复制到 release 目录
# macOS 例外：release 只保留成品 DMG（build_dmg 直接从 build/bin 取 .app），
# 不再同步 .app，并清理历史残留
sync_release() {
  local release_dir="$PROJECT_DIR/release"
  mkdir -p "$release_dir"

  if [ "$HOST_OS" = "darwin" ]; then
    # run 包裹：dry-run 不能删掉上一次的成品
    run rm -rf "${release_dir}/${BUNDLE_NAME:-$APP_NAME}.app"
  else
    local bin_path
    bin_path=$(get_build_binary)
    [ -f "$bin_path" ] || return 0
    local release_bin="${release_dir}/${RELEASE_PREFIX:-$APP_NAME}${EXT}"
    run cp -f "$bin_path" "$release_bin"
    chmod +x "$release_bin" 2>/dev/null || true
    log_success "Release Bin  → $release_bin"
  fi
}

# macOS only: 用 hdiutil 生成 DMG（文件名带版本号）
# 卷内包含 App + Applications 快捷方式，支持拖拽安装；不碰 Finder 定制，打包全程无窗口弹出
build_dmg() {
  [ "$HOST_OS" = "darwin" ] || return 0
  local version
  version=$(get_version)
  local app_path bundle_name release_dir release_dmg
  bundle_name="${BUNDLE_NAME:-$APP_NAME}"
  app_path=$(get_app_bundle)
  release_dir="$PROJECT_DIR/release"
  release_dmg="${release_dir}/${RELEASE_PREFIX:-$APP_NAME}_v${version}.dmg"

  # 直接取构建目录的 app，release 目录只落 DMG
  local source_app="$app_path"
  [ -d "$source_app" ] || return 0

  mkdir -p "$release_dir"
  run rm -f "$release_dmg"

  # staging 目录：App + Applications 符号链接（hdiutil 会将其写入为
  # Finder 别名，用户拖到此处即完成安装）
  local staging
  staging="$(mktemp -d "${TMPDIR:-/tmp}/${bundle_name}_dmg.XXXXXX")"
  trap "rm -rf '$staging'" EXIT
  run ditto "$source_app" "$staging/${bundle_name}.app"
  run ln -s /Applications "$staging/Applications"

  # 暂存副本再校验一次：DMG 里签名失效，等于用户装完打不开
  if ! $DRY_RUN; then
    codesign --verify --deep --strict "$staging/${bundle_name}.app" >/dev/null 2>&1 \
      || log_warn "DMG 暂存副本签名校验失败，分发后可能无法打开"
  fi

  log_info "生成 DMG: ${RELEASE_PREFIX:-$APP_NAME}_v${version}.dmg ..."
  run hdiutil create \
    -volname "$APP_NAME" \
    -srcfolder "$staging" \
    -ov \
    -format UDZO \
    "$release_dmg" > /dev/null

  if $DRY_RUN; then rm -rf "$staging"; trap - EXIT; return 0; fi
  rm -rf "$staging"
  trap - EXIT

  # 清理 release 中历史同步残留的 .app，只保留 DMG 成品
  run rm -rf "${release_dir}/${bundle_name}.app"

  log_success "Release DMG  → $release_dmg"
  # ad-hoc 签名无 Developer ID：下载转发后首启可能被 Gatekeeper 拦（不是签名坏）
  log_info "首次打开若提示「无法验证开发者」：右键 → 打开；或执行："
  log_info "  xattr -dr com.apple.quarantine \"/Applications/${bundle_name}.app\""
}

# Windows / Linux: 生成 zip 包 (含可执行文件 + 配置 + 图标)
build_zip() {
  [ "$HOST_OS" = "darwin" ] && return 0
  local version
  version=$(get_version)
  local release_dir="$PROJECT_DIR/release"
  local zip_name="${RELEASE_PREFIX:-$APP_NAME}_v${version}_${HOST_OS}_${HOST_ARCH}.zip"
  local zip_path="${release_dir}/${zip_name}"
  local stage_dir="${BUILD_DIR}/zip_stage"

  run rm -rf "$stage_dir"
  mkdir -p "$stage_dir"

  local bin_path
  bin_path=$(get_build_binary)
  [ -f "$bin_path" ] || return 0
  run cp -f "$bin_path" "$stage_dir/"

  if [ -n "${CONFIG_SRC:-}" ] && [ -f "$CONFIG_SRC" ]; then
    local cfg_dir
    cfg_dir="$(basename "$(dirname "$CONFIG_SRC")")"
    mkdir -p "$stage_dir/$cfg_dir"
    run cp -f "$CONFIG_SRC" "$stage_dir/$cfg_dir/$(basename "$CONFIG_SRC")"
  fi

  if [ -n "${ICON_SRC:-}" ] && [ -f "$ICON_SRC" ]; then
    run cp -f "$ICON_SRC" "$stage_dir/$(basename "$ICON_SRC")"
  fi

  mkdir -p "$release_dir"
  run rm -f "$zip_path"

  log_info "生成 ZIP: ${zip_name} ..."
  if command -v zip &>/dev/null; then
    # -q + run：真实构建不刷文件列表，dry-run 又能打印出打包命令
    (cd "$stage_dir" && run zip -qr "$zip_path" .)
  elif [ "$HOST_OS" = "windows" ] && command -v powershell &>/dev/null; then
    local stage_win zip_win
    stage_win=$(cygpath -w "$stage_dir" 2>/dev/null || echo "$stage_dir")
    zip_win=$(cygpath -w "$zip_path" 2>/dev/null || echo "$zip_path")
    run powershell -NoProfile -Command "Compress-Archive -Path '${stage_win}\\*' -DestinationPath '${zip_win}' -Force"
  else
    rm -rf "$stage_dir"
    log_error "zip 命令不可用，请安装 zip 或使用 Windows PowerShell"
  fi
  run rm -rf "$stage_dir"

  log_success "Release ZIP  → $zip_path"
}

# ============================================================
# 增量构建检查
# ============================================================

# 上一次成功编译时的源文件清单：mtime 只反映「改过」，看不到「删除 / 改名」，
# 因此路径集合也要落盘比对（build/ 不入库，clean 后自动失效）
SRC_STATE=""   # main() 入口时展开 BUILD_DIR（本模块先于差异常量加载）

# 参与后端增量判定的源文件清单（排序后逐行输出，供与上次清单 diff）
src_file_list() {
  find "$PROJECT_DIR" \
    \( ${SRC_FIND_EXPR[@]+"${SRC_FIND_EXPR[@]}"} \) \
    -not -path '*/build/*' \
    -not -path '*/release/*' \
    -not -path '*/node_modules/*' \
    -not -path '*/.git/*' \
    -type f -print 2>/dev/null | LC_ALL=C sort
}

# 检查是否需要重新构建：产物存在性 → 源文件清单 → 源文件 mtime
# 返回 0 = 需要构建, 1 = 可跳过
# SRC_FIND_EXPR 为项目差异区声明的源文件匹配表达式（-o 优先级低于隐式 AND，
# 必须由本函数用 \( \) 分组）
needs_build() {
  local binary
  binary=$(get_build_binary)

  if [ "$HOST_OS" = "darwin" ]; then
    [ -d "$(get_app_bundle)" ] || return 0
  fi
  # .app 存在但可执行文件缺失（半截产物）同样要重建
  [ -f "$binary" ] || return 0

  # 架构策略也是构建输入：macos_arch 解析出的目标架构（含 TARGET_ARCH /
  # dist 强制 universal / --platform 跨架构）必须能在产物里找到，否则重编。
  # 用 macos_arch 而非具体环境变量，才能覆盖差异区对它的重定义
  if [ "$HOST_OS" = "darwin" ]; then
    local want_arch have_archs
    want_arch=$(macos_arch)
    have_archs=$(lipo -archs "$binary" 2>/dev/null || true)
    if [ -n "$have_archs" ]; then
      case "$want_arch" in
        universal) case "$have_archs" in
                     *x86_64*arm64*|*arm64*x86_64*) ;;
                     *) return 0 ;;
                   esac ;;
        arm64) case " $have_archs " in *" arm64 "*) ;; *) return 0 ;; esac ;;
        amd64|x86_64) case " $have_archs " in *" x86_64 "*) ;; *) return 0 ;; esac ;;
      esac
    fi
  fi

  # 清单缺失（首次引入 / build 被手工清理）按需要构建处理
  [ -f "$SRC_STATE" ] || return 0
  if ! diff -q <(src_file_list) "$SRC_STATE" >/dev/null 2>&1; then
    return 0
  fi

  local newer
  newer=$(find "$PROJECT_DIR" \
    \( ${SRC_FIND_EXPR[@]+"${SRC_FIND_EXPR[@]}"} \) \
    -not -path '*/build/*' \
    -not -path '*/release/*' \
    -not -path '*/node_modules/*' \
    -not -path '*/.git/*' \
    -type f -newer "$binary" \
    -print -quit 2>/dev/null)

  [ -n "$newer" ]
}

# ============================================================
# 进程清理
# ============================================================
kill_existing() {
  $DRY_RUN && return 0
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

# ============================================================
# 命令实现（build / run / clean 为母版固定流程；
# dev / test 由项目差异区「专属实现」提供）
# ============================================================
cmd_build() {
  cross_os_guard
  check_go; ensure_build_deps
  if [ "${IS_WAILS:-1}" = "1" ]; then check_wails; fi

  if [ "${PLATFORM_SCOPE:-all}" = "darwin" ] && [ "$HOST_OS" != "darwin" ]; then
    log_error "本项目仅支持在 macOS 上构建（产物为 macOS 专属）"
  fi

  local version
  version=$(get_version)
  sync_wails_version

  # 图标变更也要重编：wails 在编译期把 build/appicon.png 转成 .icns，
  # 跳过编译会让新图标静默丢失
  local icon_changed=0
  if [ -n "${ICON_SRC:-}" ] && [ -f "$ICON_SRC" ]; then
    if [ ! -f "$BUILD_DIR/appicon.png" ] || ! cmp -s "$ICON_SRC" "$BUILD_DIR/appicon.png"; then
      icon_changed=1
    fi
  fi

  FRONTEND_REBUILT=0
  if [ "${IS_WAILS:-1}" = "1" ]; then ensure_frontend; fi

  local platform
  platform=$(get_wails_platform)
  log_info "构建 $APP_NAME ($platform, version=$version)..."
  sync_app_icon

  # macOS: 强制兼容目标系统（Go 1.26 是最后一个支持 macOS 12 的版本，
  # clang 默认用当前 SDK，不设置会导致二进制 LC_BUILD_VERSION.minos 高于目标系统）
  # 版本注入变量名由差异区 VERSION_LD_VAR 声明（默认 main.Version）
  local ldflags="${GO_BUILD_LDFLAGS:--s -w} -X ${VERSION_LD_VAR:-main.Version}=${version} ${LDFLAGS_EXTRA:-}"
  if [ "$HOST_OS" = "darwin" ]; then
    export MACOSX_DEPLOYMENT_TARGET="$MACOS_MIN_VERSION"
    export CGO_CFLAGS="-mmacosx-version-min=$MACOS_MIN_VERSION"
    export CGO_LDFLAGS="-mmacosx-version-min=$MACOS_MIN_VERSION"
    log_info "macOS 兼容目标: MACOSX_DEPLOYMENT_TARGET=$MACOS_MIN_VERSION"
  fi
  # 项目需要固定 Go 工具链时（如规避 Go 1.22 LC_UUID 缺失）在差异区声明 GOTOOLCHAIN_VER
  if [ -n "${GOTOOLCHAIN_VER:-}" ] && [ "$HOST_OS" = "darwin" ]; then
    export GOTOOLCHAIN="$GOTOOLCHAIN_VER"
    log_info "固定工具链: GOTOOLCHAIN=$GOTOOLCHAIN_VER"
  fi

  # 后端增量：前端重建（go:embed 内容变了）、图标变了或源码有更新才重编，
  # 否则复用已有产物，直接进入配置同步 / 签名 / 打包
  if $DRY_RUN || [ "$FRONTEND_REBUILT" = "1" ] || [ "$icon_changed" = "1" ] || needs_build; then
    build_impl "$ldflags"
    # 编译成功后才落盘源文件清单，供下次判定「删除 / 改名」
    if ! $DRY_RUN; then src_file_list > "$SRC_STATE" || true; fi
    log_success "构建完成"
  else
    log_info "后端源码未变更，跳过编译（复用已有产物）"
  fi

  sync_config
  fix_app_info
  hook_post_build
  adhoc_sign
  sync_release
  package_platform

  local bin_path
  bin_path=$(get_build_binary)
  if [ -f "$bin_path" ]; then
    log_success "构建产物 → $bin_path"
  elif [ "$HOST_OS" = "darwin" ] && [ -d "$(get_app_bundle)" ]; then
    log_success "构建产物 → $(get_app_bundle)"
  fi
}

# 成品归档分派：macOS 只落 DMG，Linux/Windows 落 zip；
# 产 tar.gz 等特殊归档的项目在差异区覆盖 package_platform
package_platform() {
  if [ "$HOST_OS" = "darwin" ]; then
    build_dmg
  else
    build_zip
  fi
}

# dist：发布级构建（macOS 强制 universal，产物与 CI 完全一致）
cmd_dist() {
  MACOS_UNIVERSAL_FORCE=1
  cmd_build "$@"
}

# 实际编译动作：非 Wails 项目在差异区覆盖 build_impl
build_impl() {
  local ldflags="$1"
  local extra=()
  if [ "${WAILS_BUILD_CLEAN:-0}" = "1" ]; then extra+=("-clean"); fi
  # -s: 前端已由 ensure_frontend 按需构建就绪，跳过 wails 内部的
  # frontend:install / frontend:build；否则每次构建前端都会跑两遍（脚本一次 + wails 一次）
  run "$WAILS_CMD" build -platform "$(get_wails_platform)" "${extra[@]}" -s -ldflags "$ldflags"
}

cmd_run() {
  if [ "$HOST_OS" = "windows" ]; then
    log_error "run 命令不支持 Windows，请直接运行 build/bin/${APP_NAME}.exe"
  fi

  local log_file="$BUILD_DIR/run.log"
  mkdir -p "$BUILD_DIR"

  kill_existing

  # 前端有改动也必须走 cmd_build：dist 是 go:embed 输入，需要重新编译
  if needs_build || frontend_stale; then
    cmd_build
  else
    log_success "源文件无变化，跳过构建"
  fi

  local binary
  binary=$(get_run_binary)

  log_info "启动 $APP_NAME (前台模式)..."
  log_info "按 Ctrl+C 关闭 App"
  echo ""

  if $DRY_RUN; then
    log_warn "[dry-run] $binary | tee -a $log_file"
    return 0
  fi
  : > "$log_file"
  run "$binary" 2>&1 | tee -a "$log_file"
  local exit_code=${PIPESTATUS[0]}

  echo ""
  if [ "$exit_code" -eq 0 ]; then
    log_success "App 已正常退出"
  else
    log_warn "App 退出码: $exit_code"
  fi
}

get_run_binary() {
  if [ "$HOST_OS" = "darwin" ]; then
    echo "$(get_app_bundle)/Contents/MacOS/${APP_NAME}"
  else
    echo "$(get_build_binary)"
  fi
}

cmd_clean() {
  log_info "清理构建产物..."
  if $DRY_RUN; then
    log_warn "[dry-run] kill_existing → rm -rf build dist release ${CLEAN_EXTRA[*]-}"
    return 0
  fi
  kill_existing 2>/dev/null || true
  hook_extra_cleanup
  rm -rf build dist release ${CLEAN_EXTRA[@]+"${CLEAN_EXTRA[@]}"}
  find . -name '*.test' -delete 2>/dev/null || true
  log_success "清理完成"
}

# dev / test / clean 默认实现：项目在「专属实现」区覆盖同名函数即可
cmd_dev() {
  check_go
  mkdir -p "$BUILD_DIR"
  local log_file="$BUILD_DIR/dev.log"

  local dev_cmd=()
  if [ ${#DEV_CMD[@]} -gt 0 ]; then
    dev_cmd=("${DEV_CMD[@]}")
  else
    if [ "${IS_WAILS:-1}" = "1" ]; then check_wails; fi
    dev_cmd=("$WAILS_CMD" dev)
    # DEV_EXTRA_ARGS：静态前端项目（wails.json 的 frontend:dev:watcher 为空）传
    # -assetdir ./frontend/dist。不加时 wails 每个增量周期都会去轮询自身的
    # /wails/assetdir（devserver 未提供该路由），日志刷屏且改 dist 不触发重载。
    if [ -z "${DEV_EXTRA_ARGS+x}" ]; then DEV_EXTRA_ARGS=(); fi
    if [ ${#DEV_EXTRA_ARGS[@]} -gt 0 ]; then
      dev_cmd+=("${DEV_EXTRA_ARGS[@]}")
    fi
  fi

  log_info "启动开发模式: ${dev_cmd[*]}，按 Ctrl+C 停止"
  echo ""

  if $DRY_RUN; then
    log_warn "[dry-run] ${dev_cmd[*]} | tee -a $log_file"
    return 0
  fi
  : > "$log_file"
  # 临时关闭 -e：Ctrl+C (130) 属正常退出路径
  set +e
  run "${dev_cmd[@]}" 2>&1 | tee -a "$log_file"
  local exit_code=${PIPESTATUS[0]}
  set -e

  echo ""
  if [ "$exit_code" -eq 0 ] || [ "$exit_code" -eq 130 ]; then
    log_success "开发模式已停止"
  else
    log_warn "退出码: $exit_code"
  fi
}

cmd_test() {
  check_go
  log_info "运行 Go 单元测试 (-race)..."
  run go test -race -count=1 ${TEST_PKGS[@]+"${TEST_PKGS[@]}"}
  log_success "全部测试通过"
}

# ============================================================
# 交互菜单 / 帮助 / 分发（命令表 commands 为唯一真源）
# commands 格式: "序号|别名1,别名2|说明|处理函数"
# ============================================================
show_menu() {
  echo ""
  echo "=========================================="
  echo "      ${APP_DISPLAY_NAME:-$APP_NAME} - 构建工具 ($HOST_OS/$HOST_ARCH)"
  echo "=========================================="
  local entry num aliases desc fn
  for entry in "${commands[@]}"; do
    IFS='|' read -r num aliases desc fn <<< "$entry"
    printf "  %s) %-12s %s\n" "$num" "${aliases%%,*}" "$desc"
  done
  echo "  0) exit        退出"
  echo "=========================================="
  echo -n "请输入选择 [0-${#commands[@]}]: "
}

show_help() {
  cat <<EOF
${APP_DISPLAY_NAME:-$APP_NAME} 构建工具

用法: $0 [命令] [参数...]

当前平台: $HOST_OS/$HOST_ARCH

命令:
  (无参数)      显示交互菜单（回车默认 ${DEFAULT_CMD:-run}）
EOF
  local entry num aliases desc fn
  for entry in "${commands[@]}"; do
    IFS='|' read -r num aliases desc fn <<< "$entry"
    printf '  %s|%-16s %s\n' "$num" "${aliases//,/ }" "$desc"
  done
  cat <<EOF
  0|exit            退出
  help              显示本帮助

全局选项 (可写在命令前后):
  --dry-run        仅打印将执行的动作，不实际执行
  --platform=<os>/<arch>
                   指定构建目标（如 linux/arm64），默认当前宿主平台
  --skip-deps      跳过系统构建依赖自动安装（环境已就绪时用）

平台说明:
EOF
  if [ "${PLATFORM_SCOPE:-all}" = "darwin" ]; then
    cat <<EOF
  macOS:   构建 .app + .dmg (darwin/universal)，release/ 只保留 .dmg
  本项目产物为 macOS 专属，其他平台仅可跑测试
EOF
  else
    cat <<EOF
  macOS:   构建 .app + .dmg (darwin/universal)，release/ 只保留 .dmg
  Linux:   构建 ELF 二进制 + .zip (linux/$HOST_ARCH)
  Windows: 构建 .exe + .zip (windows/$HOST_ARCH)
EOF
  fi
  cat <<EOF

项目路径:
  构建产物:  $(get_build_binary)
  Release:   release/
  运行日志:  $BUILD_DIR/run.log
  配置文件:  ${CONFIG_SRC:-<无>}
  图标源:    ${ICON_SRC:-<无>}
  版本文件:  $PROJECT_DIR/VERSION
  Bundle ID: $PROJECT_BUNDLE_ID
EOF
  if declare -F show_help_extra >/dev/null; then
    show_help_extra
  fi
}

# 分发：序号或任一别名均可；--platform / --dry-run 为母版全局选项，
# 在命令前后均可；未知命令按 FALLBACK_CMD 兜底，未声明则报错
main() {
  # 入口时差异常量已全部就位，展开增量判定状态文件路径（模块加载期不碰）
  FRONTEND_STAMP="$BUILD_DIR/.frontend.stamp"    # 前端上次构建时间点
  FRONTEND_STATE="$BUILD_DIR/.frontend.list"     # 前端源码路径清单（捕捉删除/改名）
  SRC_STATE="$BUILD_DIR/.src.list"               # 后端源码路径清单
  local global_args=() cmd="" args=() a p
  for a in "$@"; do
    case "$a" in
      --dry-run)        global_args+=("$a") ;;
      --platform=*)     global_args+=("$a") ;;
      --skip-deps)      global_args+=("$a") ;;
      *)                if [ -z "$cmd" ]; then cmd="$a"; else args+=("$a"); fi ;;
    esac
  done

  for a in "${global_args[@]}"; do
    case "$a" in
      --dry-run) DRY_RUN=true ;;
      --skip-deps) SKIP_DEPS=true ;;
      # --platform=linux/arm64 让本地也能指定产出其他平台产物（跨架构构建）
      --platform=*)
        p="${a#*=}"
        HOST_OS="${p%%/*}"; HOST_ARCH="${p##*/}"
        [ "$HOST_OS" = "windows" ] && EXT=".exe" || EXT=""
        log_info "指定构建目标: $HOST_OS/$HOST_ARCH" ;;
    esac
  done

  if [ -z "$cmd" ]; then
    show_menu
    read -r choice
    cmd="${choice:-${DEFAULT_CMD:-run}}"
  fi

  case "$cmd" in
    0|q|exit|quit)  echo "退出..."; exit 0 ;;
    help|-h|--help) show_help; exit 0 ;;
  esac

  local entry num aliases desc fn a alias_arr
  for entry in "${commands[@]}"; do
    IFS='|' read -r num aliases desc fn <<< "$entry"
    IFS=',' read -r -a alias_arr <<< "$aliases"
    for a in "${alias_arr[@]}"; do
      if [ "$cmd" = "$num" ] || [ "$cmd" = "$a" ]; then
        "$fn" "${args[@]}"
        return
      fi
    done
  done

  if [ -n "${FALLBACK_CMD:-}" ]; then
    log_info "未识别命令 '$cmd'，按默认流程执行 $FALLBACK_CMD"
    "$FALLBACK_CMD" "${args[@]}"
  else
    log_error "未知命令: ${cmd}（help 查看用法）"
  fi
}
