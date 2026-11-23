#!/bin/bash
# 发版：在 origin/master 的头提交上跑门禁，打下一个版本的 tag（vX.Y.Z），推送，再发 GitHub Release。
# 版本从 v0.0.1 起，默认只升 patch；minor、major 只在维护者明说要升时用。
# 用法：bash scripts/release.sh [patch|minor|major]
# 要求：检出的就是 origin/master 的头提交、工作树干净、这个提交还没发过版；gh 已登录。
set -euo pipefail

bump=${1:-patch}
case $bump in
  patch | minor | major) ;;
  *)
    echo "用法：bash scripts/release.sh [patch|minor|major]" >&2
    exit 2
    ;;
esac

cd "$(git rev-parse --show-toplevel)"
git fetch -q --tags origin
head=$(git rev-parse origin/master)
if [[ $(git rev-parse HEAD) != "$head" ]]; then
  echo "检出的不是 origin/master 的头提交（${head:0:12}），不发版" >&2
  exit 1
fi
if [[ -n $(git status --porcelain) ]]; then
  echo "工作树不干净，不发版" >&2
  exit 1
fi
if tagged=$(git tag --points-at "$head" | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$'); then
  echo "头提交已经发过版：$tagged" >&2
  exit 1
fi

last=$(git tag -l 'v*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n 1 || true)
IFS=. read -r major minor patch <<<"${last#v}"
major=${major:-0} minor=${minor:-0} patch=${patch:-0}
case $bump in
  patch) patch=$((patch + 1)) ;;
  minor) minor=$((minor + 1)) patch=0 ;;
  major) major=$((major + 1)) minor=0 patch=0 ;;
esac
version=v$major.$minor.$patch

bash scripts/check.sh

if [[ -n $last ]]; then
  notes=$(git log --no-merges --format='- %s' "$last..$head" | grep -v '^- chore(gate)' || true)
  notes=${notes:-"- 只有门禁文件的同步"}
else
  notes="第一个正式版本。"
fi

git tag -a "$version" -m "claude-switch $version" "$head"
git push -q origin "$version"
gh release create "$version" --verify-tag --title "$version" --notes "$notes"
# 让 Go 模块代理马上收录这一版，别的模块升级时不用等它自己抓取
(cd "$(mktemp -d)" && GOPROXY=https://proxy.golang.org go list -m "github.com/dzshzx/claude-switch@$version" >/dev/null)
echo "已发版 $version（${head:0:12}）"
