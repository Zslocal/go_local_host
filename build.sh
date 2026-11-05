#!/usr/bin/env bash
# 交叉编译 Linux 二进制（在 Windows Git Bash / macOS / Linux 上均可执行）。
# 产物为纯静态 ELF，无 CGO、无外部依赖，scp 到服务器即可运行。
set -euo pipefail
cd "$(dirname "$0")"

# 默认版本号与 git tag 保持一致；发布新版本时可 VERSION=v0.2.0 bash build.sh
VERSION="${VERSION:-0.1.0}"
OUT="dist"
mkdir -p "$OUT"

build() {
  local goarch="$1" out="$OUT/hostmon-linux-$1"
  echo "==> building linux/$goarch ..."
  CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
    -o "$out" ./cmd/hostmon
  local size
  size=$(du -h "$out" | cut -f1)
  echo "    $out  ($size)"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$out"
  fi
}

build amd64
build arm64

echo "==> 完成。部署示例："
echo "    scp dist/hostmon-linux-arm64 user@server:/usr/local/bin/hostmon"
echo "    ssh user@server 'chmod +x /usr/local/bin/hostmon && hostmon -once'"
