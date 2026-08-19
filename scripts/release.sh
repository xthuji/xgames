#!/usr/bin/env bash
# =============================================================================
# XGames - 发布工具
# =============================================================================
# 读取根目录 VERSION 创建 git tag v{version}，推送到 GitHub 远程，
# 触发 .github/workflows/release.yml 在 macOS / Linux / Windows 三平台
# 自动构建并发布到 GitHub Release。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
VERSION_FILE="${ROOT_DIR}/VERSION"
REMOTE_NAME="${RELEASE_REMOTE:-backupstream}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

die() { echo -e "${RED}✗${NC} $*" >&2; exit 1; }
ok()  { echo -e "${GREEN}✓${NC} $*"; }
warn(){ echo -e "${YELLOW}⚠${NC} $*"; }
info(){ echo -e "${CYAN}→${NC} $*"; }

usage() {
    cat <<EOF
${BOLD}用法:${NC} $0 [--version=<ver>] [--remote=<name>] [--dry-run]

${BOLD}说明:${NC}
  读取 VERSION 创建 git tag v{version}，推送到 GitHub 远程，
  触发 .github/workflows/release.yml 自动构建三平台应用并发布。

${BOLD}选项:${NC}
  --version=<ver>  指定版本号 (覆盖 VERSION 文件)
  --remote=<name>  推送的远程名称 (默认: backupstream)
  --dry-run        仅打印命令，不实际执行
  -h, --help       显示此帮助

${BOLD}环境变量:${NC}
  RELEASE_REMOTE   远程名称 (优先级低于 --remote)

${BOLD}示例:${NC}
  $0                       # 使用 VERSION 版本号
  $0 --version=1.1.0       # 强制指定版本
  $0 --version=1.1.0 --dry-run
EOF
    exit 0
}

DRY_RUN=false
CUSTOM_VERSION=""

for arg in "$@"; do
    case "$arg" in
        -h|--help)        usage ;;
        --dry-run)        DRY_RUN=true ;;
        --version=*)      CUSTOM_VERSION="${arg#*=}" ;;
        --remote=*)       REMOTE_NAME="${arg#*=}" ;;
        *) die "未知参数: $arg (--help 查看用法)" ;;
    esac
done

cd "$ROOT_DIR"

if [[ -n "$CUSTOM_VERSION" ]]; then
    APP_VERSION="$CUSTOM_VERSION"
else
    [[ -f "$VERSION_FILE" ]] || die "未找到版本文件: $VERSION_FILE"
    APP_VERSION="$(tr -d '[:space:]' < "$VERSION_FILE")"
fi

[[ -z "$APP_VERSION" ]] && die "版本号为空"
[[ "$APP_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$ ]] || \
    die "版本号格式无效: $APP_VERSION (期望 X.Y.Z 或 X.Y.Z-tag)"

TAG_NAME="v${APP_VERSION}"

echo ""
echo -e "${CYAN}${BOLD}═══ XGames Release ═══${NC}"
echo -e "  版本: ${BOLD}${APP_VERSION}${NC}"
echo -e "  Tag:  ${BOLD}${TAG_NAME}${NC}"
echo -e "  远程: ${BOLD}${REMOTE_NAME}${NC}"
echo -e "  模式: $($DRY_RUN && echo -e "${YELLOW}DRY-RUN${NC}" || echo -e "${GREEN}实际执行${NC}")"
echo ""

run() {
    if $DRY_RUN; then
        echo -e "  ${YELLOW}[dry-run]${NC} $*"
    else
        info "$*"
        eval "$@"
    fi
}

# 检查远程是否存在
if git remote get-url "$REMOTE_NAME" &>/dev/null; then
    ok "远程仓库已配置: ${REMOTE_NAME} → $(git remote get-url "$REMOTE_NAME")"
else
    die "远程仓库 '${REMOTE_NAME}' 不存在。可选: $(git remote | tr '\n' ' ')"
fi

# 检查 tag 是否已存在
if git rev-parse "$TAG_NAME" &>/dev/null; then
    warn "Tag ${TAG_NAME} 已存在，将被强制更新"
    if ! $DRY_RUN; then
        read -rp "  确认强制更新? [y/N] " confirm
        [[ "$confirm" == "y" || "$confirm" == "Y" ]] || die "已取消"
    fi
fi

# 创建/更新 tag
run "git tag -f -a '${TAG_NAME}' -m 'Release ${TAG_NAME}'"

# 推送 tag 到远程
run "git push '${REMOTE_NAME}' 'refs/tags/${TAG_NAME}' --force"

if $DRY_RUN; then
    echo ""
    ok "dry-run 完成。去掉 --dry-run 以实际执行"
else
    echo ""
    ok "Tag ${TAG_NAME} 已推送到 ${REMOTE_NAME}"
    echo ""
    info "GitHub Actions 工作流应已触发（请在 GitHub 仓库的 Actions 页面查看）"
    info "Release 页面: 请在 GitHub 仓库的 Releases 页面查看"
fi
