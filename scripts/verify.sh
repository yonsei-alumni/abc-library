#!/usr/bin/env bash
# 에이전트가 작업을 마치려 할 때(Stop 훅) 실행한다.
# make ci(린트와 전체 테스트)가 실패하면 종료 코드 2로 작업 종료를 막는다.
# 성공하면 표준 출력에 아무것도 쓰지 않는다. Codex의 Stop 훅은 표준 출력을 JSON으로 해석한다.
set -uo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
input="$(cat)"

# 이 훅이 돌려보낸 작업을 마칠 때 다시 막아서 무한 반복하지 않게 한다.
if [[ "$(jq -r '.stop_hook_active // false' <<<"$input")" == "true" ]]; then
  exit 0
fi

# Go 소스가 아직 없으면 검사할 대상이 없다.
if [[ -z "$(find "$root" -name '*.go' -not -path '*/testdata/*' -not -path '*/.git/*' -print -quit)" ]]; then
  exit 0
fi

# push하지 않은 변경(커밋, 수정, 새 파일)이 없거나 문서뿐이면 검사하지 않는다.
# push한 코드는 CI가 검사한다. 문서의 범위는 CI의 paths-ignore와 같다(.md 파일과 docs/).
base="$(git -C "$root" rev-parse -q --verify '@{upstream}' || echo HEAD)"
changed="$(
  git -C "$root" diff --name-only "$base"
  git -C "$root" ls-files --others --exclude-standard
)"
if ! grep -qvE '^$|\.md$|^docs/' <<<"$changed"; then
  exit 0
fi

if ! out="$(make -s -C "$root" ci 2>&1)"; then
  echo "make ci 실패:" >&2
  tail -n 80 <<<"$out" >&2
  exit 2
fi
