#!/usr/bin/env bash
# 冒烟：起一个临时实例，验证健康检查、前端外壳与订阅端点。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

step() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
die()  { printf '\033[1;31m错误：\033[0m %s\n' "$1" >&2; exit 1; }

BIN="${BIN:-$ROOT/bin/siwu}"
[ -x "$BIN" ] || die "先执行 ./scripts/build.sh（缺少 $BIN）"

PORT="${PORT:-18788}"
DATA="$(mktemp -d)"
LOG="$DATA/siwu.log"
"$BIN" serve --addr ":$PORT" --data "$DATA" >"$LOG" 2>&1 &
PID=$!
trap 'kill $PID 2>/dev/null || true; rm -rf "$DATA"' EXIT

for _ in $(seq 1 40); do
  if curl -fsS --noproxy '*' "http://127.0.0.1:$PORT/api/healthz" >/dev/null 2>&1; then break; fi
  sleep 0.25
done

step "健康检查"
curl -fsS --noproxy '*' "http://127.0.0.1:$PORT/api/healthz" || die "healthz 失败"

step "前端外壳"
curl -fsS --noproxy '*' "http://127.0.0.1:$PORT/" | grep -q '<div id="root">' || die "首页不是前端外壳"

step "接口鉴权面"
code=$(curl -s --noproxy '*' -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/api/sources")
[ "$code" = "200" ] || die "GET /api/sources 返回 $code"

step "冒烟通过"
