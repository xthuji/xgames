#!/usr/bin/env bash
# =============================================================================
# Project Repository - 提交 / 推送 / 发布
# =============================================================================
# 交互式询问 commit message → 提交全部变更 → 强制推送 origin (Gitee)
# → 同步推送 backupstream (GitHub) → 调用 release.sh 打 tag 触发 CI 发布。
#
# 版本号、tag 的解析与推送均交由 release.sh 处理。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
PRIMARY_REMOTE="origin"
SYNC_REMOTE="backupstream"

MESSAGE=""
DRY_RUN=false
AUTO_YES=false
SKIP_RELEASE=false

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

die()  { echo -e "${RED}✗${NC} $*" >&2; exit 1; }
ok()   { echo -e "${GREEN}✓${NC} $*"; }
warn() { echo -e "${YELLOW}⚠${NC} $*"; }
info() { echo -e "${CYAN}→${NC} $*"; }

usage() {
    echo -e "$(cat <<EOF
${BOLD}用法:${NC} $0 [选项]

${BOLD}说明:${NC}
  1) 交互式输入 commit message，暂存并提交全部变更
  2) 强制推送到 ${PRIMARY_REMOTE}，再同步推送到 ${SYNC_REMOTE}
  3) 调用 release.sh 打 tag 并触发 GitHub Actions 发布

${BOLD}选项:${NC}
  -m, --message=<msg>  指定 commit message (跳过交互式询问)
  -y, --yes            跳过确认提示 (配合 -m 实现非交互执行)
      --no-release     只提交推送，不调用 release.sh
      --dry-run        仅打印命令，不实际执行
  -h, --help           显示此帮助

${BOLD}示例:${NC}
  $0                                    # 全流程交互式
  $0 -m "feat: 支持天气多源切换"         # 指定 message
  $0 -m "docs: 更新架构文档" --no-release # 不触发发布
  $0 --dry-run                          # 预览
EOF
)"
    exit 0
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help)    usage ;;
        -m|--message)
            [[ $# -ge 2 ]] || die "选项 $1 需要一个参数"
            MESSAGE="$2"; shift 2 ;;
        --message=*)  MESSAGE="${1#*=}"; shift ;;
        -y|--yes)     AUTO_YES=true; shift ;;
        --no-release) SKIP_RELEASE=true; shift ;;
        --dry-run)    DRY_RUN=true; shift ;;
        *)            die "未知参数: $1 (--help 查看用法)" ;;
    esac
done

# 打印并执行命令；dry-run 时只打印
run() {
    if $DRY_RUN; then
        echo -e "  ${YELLOW}[dry-run]${NC} $*"
    else
        info "$*"
        "$@"
    fi
}

confirm() {
    $AUTO_YES && return 0
    local answer
    read -rp "  $1 [y/N] " answer || return 1
    [[ "$answer" == "y" || "$answer" == "Y" ]]
}

cd "$ROOT_DIR"

git rev-parse --is-inside-work-tree &>/dev/null || die "当前目录不是 Git 仓库: $ROOT_DIR"
BRANCH="$(git rev-parse --abbrev-ref HEAD)"
[[ "$BRANCH" == "HEAD" ]] && die "当前处于分离 HEAD 状态，无法推送"
for remote in "$PRIMARY_REMOTE" "$SYNC_REMOTE"; do
    git remote get-url "$remote" &>/dev/null || \
        die "远程仓库 '${remote}' 不存在。可选: $(git remote | tr '\n' ' ')"
done

echo ""
echo -e "${CYAN}${BOLD}═══ Project Repository Commit & Push & Release ═══${NC}"
echo -e "  分支:   ${BOLD}${BRANCH}${NC}"
echo -e "  远程:   ${BOLD}${PRIMARY_REMOTE}${NC} → ${BOLD}${SYNC_REMOTE}${NC} ${RED}(强制推送)${NC}"
echo -e "  发布:   $($SKIP_RELEASE && echo -e "${YELLOW}已跳过 (--no-release)${NC}" || echo -e "${GREEN}release.sh${NC}")"
echo -e "  模式:   $($DRY_RUN && echo -e "${YELLOW}DRY-RUN${NC}" || echo -e "${GREEN}实际执行${NC}")"
echo ""

# -----------------------------------------------------------------------------
# 1. 交互式询问 commit message
# -----------------------------------------------------------------------------
if [[ -z "${MESSAGE//[[:space:]]/}" ]]; then
    while true; do
        echo -e "${BOLD}请输入 commit message${NC}:"
        printf "  ${CYAN}>${NC} "
        IFS= read -r MESSAGE || die "已取消"
        if [[ -n "${MESSAGE//[[:space:]]/}" ]]; then
            break
        fi
        warn "message 不能为空"
    done
fi

# -----------------------------------------------------------------------------
# 2. 提交变更
# -----------------------------------------------------------------------------
CHANGES="$(git status --porcelain)"
if [[ -n "$CHANGES" ]]; then
    info "待提交的变更:"
    echo "$CHANGES" | sed 's/^/    /'
else
    ok "工作区干净，无新提交（仅同步远程 + 发布）"
fi

echo ""
warn "强制推送将覆盖 ${PRIMARY_REMOTE}/${BRANCH} 的历史"
confirm "确认提交并推送到 ${PRIMARY_REMOTE} + ${SYNC_REMOTE}?" || die "已取消"

if [[ -n "$CHANGES" ]]; then
    run git add -A
    run git commit -m "$MESSAGE"
    $DRY_RUN || ok "提交完成: $(git log -1 --pretty='%h %s')"
fi

# -----------------------------------------------------------------------------
# 3. 强制推送 origin，同步推送 backupstream
# -----------------------------------------------------------------------------
echo ""
run git push "$PRIMARY_REMOTE" "$BRANCH" --force
$DRY_RUN || ok "已推送到 ${PRIMARY_REMOTE}/${BRANCH}"

echo ""
info "同步到 ${SYNC_REMOTE} ..."
run git push "$SYNC_REMOTE" "$BRANCH" --force
$DRY_RUN || ok "已同步到 ${SYNC_REMOTE}/${BRANCH}"

# -----------------------------------------------------------------------------
# 4. 调用 release.sh 打 tag 并触发发布
# -----------------------------------------------------------------------------
if $SKIP_RELEASE; then
    echo ""
    info "已跳过发布（--no-release）。需要时执行: ./scripts/release.sh"
    exit 0
fi

echo ""
if ! $AUTO_YES && ! $DRY_RUN; then
    confirm "调用 release.sh 创建并推送 release tag?" || die "已跳过发布"
fi

DRY_ARG=""
$DRY_RUN && DRY_ARG="--dry-run"

run ./scripts/release.sh $DRY_ARG

echo ""
if $DRY_RUN; then
    ok "dry-run 完成。去掉 --dry-run 以实际执行"
else
    ok "全部完成"
    info "构建进度见 GitHub 仓库的 Actions 页面"
fi
