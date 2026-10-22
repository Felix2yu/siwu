#!/usr/bin/env bash
# 司寤构建脚本 —— 从 Go 源 + web/ 前端构建出内嵌前端的单二进制 bin/siwu
#
# 产物：bin/siwu
# 前置：Go 1.27+ / Node 26 + pnpm（版本由 web/package.json 的 packageManager 决定）
#
# 用法：
#   ./scripts/build.sh                # 完整构建
#   ./scripts/build.sh --stage-only    # 只把 web/dist 搬到 internal/web/dist（CI 用）
#   GO_BIN=/path/to/go NODE_BIN=/path/to/node ./scripts/build.sh
#
# 约定见 Felix2yu/.github/docs/build-script.md
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

step() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
die()  { printf '\033[1;31m错误：\033[0m %s\n' "$1" >&2; exit 1; }

STAGE_ONLY=0
for arg in "$@"; do
  case "$arg" in
    --stage-only) STAGE_ONLY=1 ;;
    -h|--help) sed -n '2,14p' "$0"; exit 0 ;;
    *) die "未知参数：$arg" ;;
  esac
done

# ---------------------------------------------------------------- 工具链解析

resolve_go() {
  if [ -n "${GO_BIN:-}" ]; then
    [ -x "$GO_BIN" ] || die "GO_BIN=$GO_BIN 不可执行"
    return
  fi
  local cand
  for cand in "$(command -v go 2>/dev/null || true)" /opt/homebrew/bin/go /usr/local/go/bin/go; do
    if [ -n "$cand" ] && [ -x "$cand" ]; then GO_BIN="$cand"; return; fi
  done
  die "找不到 go，可用 GO_BIN 指定"
}

resolve_node() {
  local want major
  [ -f .nvmrc ] || die "缺少 .nvmrc（前端工具链契约要求）"
  want="$(tr -d 'v \n' < .nvmrc)"
  major="${want%%.*}"

  if [ -n "${NODE_BIN:-}" ]; then
    [ -x "$NODE_BIN" ] || die "NODE_BIN=$NODE_BIN 不可执行"
  else
    local cand found=""
    for cand in "$(command -v node 2>/dev/null || true)" \
                "$HOME/.nvm/versions/node/v${want}.x/bin/node" \
                /opt/homebrew/bin/node /usr/local/bin/node; do
      if [ -n "$cand" ] && [ -x "$cand" ]; then found="$cand"; break; fi
    done
    [ -n "$found" ] || die "找不到 node（.nvmrc 要求 $want）"
    NODE_BIN="$found"
  fi

  local have
  have="$("$NODE_BIN" -v | tr -d 'v' | cut -d. -f1)"
  if [ "$have" -lt "$major" ]; then
    die "node 版本过低：$("$NODE_BIN" -v)，.nvmrc 要求 $want"
  fi
  if [ "$have" -gt "$major" ]; then
    printf '\033[1;33m警告：\033[0m node 主版本 %s 高于 .nvmrc 的 %s，继续\n' "$have" "$major"
  fi
  export PATH="$(dirname "$NODE_BIN"):$PATH"
}

stage_web() {
  printf '      web/dist → internal/web/dist\n' 
  rm -rf internal/web/dist
  mkdir -p internal/web/dist
  cp -R web/dist/. internal/web/dist/
  touch internal/web/dist/.gitkeep
}

if [ "$STAGE_ONLY" -eq 1 ]; then
  [ -d web/dist ] || die "web/dist 不存在：前端还没构建"
  stage_web
  exit 0
fi

resolve_go
resolve_node
PNPM_BIN="${PNPM_BIN:-$(command -v pnpm 2>/dev/null || true)}"
[ -n "$PNPM_BIN" ] && [ -x "$PNPM_BIN" ] || die "找不到 pnpm（可用 corepack enable 安装）"

step "[1/4] 构建前端（$(pnpm --version))"
if [ ! -d web/node_modules ]; then
  ( cd web && "$PNPM_BIN" install --frozen-lockfile )
fi
( cd web && "$PNPM_BIN" run build )

step "[2/4] 同步前端产物"
stage_web

step "[3/4] 编译 Go 二进制"
mkdir -p bin
"$GO_BIN" build -trimpath -ldflags "-s -w" -o "$ROOT/bin/siwu" ./cmd/siwu

step "[4/4] 完成"
printf '产物：%s（%s）\n' "$ROOT/bin/siwu" "$(du -h "$ROOT/bin/siwu" | cut -f1)"
