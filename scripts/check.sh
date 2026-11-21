#!/usr/bin/env bash
# 本仓门禁：格式与 shellcheck（scripts/format.sh）、工具链为 go.mod 的 go 行固定的版本、go vet、staticcheck、
# go test（-race）、函数认知复杂度上限、只链接标准库，再按 linux/amd64 与 linux/arm64 各编一份静态程序。
# 用法：bash scripts/check.sh，失败非零退出。
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$ROOT"

# 工具版本、复杂度上限与共用检查函数在 scripts/gatelib.sh。
# shellcheck source=scripts/gatelib.sh
source scripts/gatelib.sh
# 仓内不放 Python 文件，有就列出并拒绝。
gate_python_allowlist

bash scripts/format.sh --check
bash scripts/format.sh --lint

# 版本只写在 go.mod 的 go 行，不许有 toolchain 行；之后都用这份工具链的绝对路径。
gate_go_version .
GO="$(go env GOROOT)/bin/go"

"$GO" vet ./...
# staticcheck 用默认规则集。
gate_go_run "$STATICCHECK" ./...
# 测试在临时 HOME 里跑，起的假 Claude Code 进程用完即杀；-count=1 不走测试缓存（测试读 /proc，缓存看不到这类输入）。
"$GO" test -count=1 -race -vet=off ./...
# gocognit 只读源码，有函数超线时列出并非零退出。复杂度不认代码里的豁免标记，gocognit 没有关掉它的开关，这里直接查标记。
gate_go_run "$GOCOGNIT" -over "$MAX_COGNITIVE" .
gate_no_marker 'gocognit:ignore' '*.go'
# 第三方依赖与仓根 go-deps-allowlist.txt 逐行相同（现在一个也没有）。
gate_go_deps go-deps-allowlist.txt ./...

# 两个架构各编一份静态程序，确认能交叉编译；本机架构那一份要能打印版本。
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT
for arch in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$GO" build -trimpath -o "$out/claude-switch-$arch" .
done
"$out/claude-switch-$("$GO" env GOARCH)" version
echo "check: ok"
