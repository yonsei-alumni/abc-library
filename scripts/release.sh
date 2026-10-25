#!/usr/bin/env bash
# main에 push한 커밋을 release 브랜치로 옮겨서 운영에 배포한다.
# GitHub Actions가 release push를 받아 make ci를 통과한 뒤 웹과 배치 중 바뀐 쪽만 배포한다.
#
#   scripts/release.sh           origin/main을 배포한다
#   scripts/release.sh <커밋>     origin/main에 있는 그 커밋까지만 배포한다
#
# 웹 서버는 마이그레이션을 적용하지 않으므로, 마이그레이션이 바뀌었으면 release에 올리기 전에 운영 DB에 먼저 적용한다.
# 그때는 운영 DB에 연결해야 하므로 .envrc를 읽은 셸(DB_URL, DB_PWD)에서 실행한다.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=gcp-env.sh
source "$root/scripts/gcp-env.sh"
cd "$root"

git fetch --quiet origin
target="$(git rev-parse --verify "${1:-origin/main}^{commit}")"
short="$(git rev-parse --short "$target")"
if ! git merge-base --is-ancestor "$target" origin/main; then
  echo "${short}는 origin/main에 없습니다. main에 push해서 CI를 통과한 커밋만 배포합니다." >&2
  exit 1
fi

migrations=internal/store/migrations
if git rev-parse --verify --quiet origin/release >/dev/null; then
  if [[ "$(git rev-parse origin/release)" == "$target" ]]; then
    echo "release가 이미 ${short}입니다."
    exit 0
  fi
  if ! git merge-base --is-ancestor origin/release "$target"; then
    echo "release에 ${short}에 없는 커밋이 있습니다. release를 직접 고친 것인지 확인하세요." >&2
    exit 1
  fi
  echo "release에 올릴 커밋:"
  git log --oneline origin/release.."$target" | sed 's/^/  /'
  migrate=false
  git diff --quiet origin/release "$target" -- "$migrations" || migrate=true
else
  echo "release 브랜치를 새로 만듭니다: $(git log --oneline -1 "$target")"
  # 이전에 배포한 커밋을 모르므로 마이그레이션을 적용해 둔다. 이미 적용한 것은 건너뛴다.
  migrate=true
fi

if [[ "$migrate" == true ]]; then
  if [[ -z "${DB_URL:-}" || -z "${DB_PWD:-}" ]]; then
    echo "운영 DB에 마이그레이션을 먼저 적용해야 합니다. .envrc를 읽은 셸에서 다시 실행하세요." >&2
    exit 1
  fi
  # 작업 트리의 고치는 중인 마이그레이션이 섞이지 않게, 배포할 커밋을 따로 꺼내서 적용한다.
  tree="$(mktemp -d)"
  trap 'git worktree remove --force "$tree" >/dev/null 2>&1 || rm -rf "$tree"' EXIT
  git worktree add --quiet --detach "$tree" "$target"
  echo "운영 DB에 ${short}의 마이그레이션을 적용합니다."
  (cd "$tree" && go run ./cmd/batch migrate)
fi

git push origin "${target}:refs/heads/release"
echo "GitHub Actions가 배포합니다: https://github.com/${GITHUB_REPO}/actions (gh run watch로 지켜볼 수 있다)"
