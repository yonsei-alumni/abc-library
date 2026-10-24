#!/usr/bin/env bash
# 에이전트가 파일을 수정한 직후(PostToolUse 훅) 실행한다.
# 수정된 Go 파일을 goimports로 정리하고, 그 파일이 속한 패키지를 golangci-lint로 검사한다.
# 문제가 있으면 종료 코드 2로 끝내서 표준 에러의 내용을 에이전트에게 돌려준다.
set -uo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
input="$(cat)"
cwd="$(jq -r '.cwd // empty' <<<"$input")"
cwd="${cwd:-$root}"

# Claude Code는 tool_input.file_path로, Codex의 apply_patch는 패치 본문(tool_input.command)으로 수정한 파일을 알려 준다.
paths="$(
  jq -r '.tool_input.file_path // empty' <<<"$input"
  jq -r '.tool_input.command // empty | if type == "array" then join("\n") else tostring end' <<<"$input" |
    sed -nE 's/^\*\*\* (Add|Update) File: (.+)$/\2/p; s/^\*\*\* Move to: (.+)$/\1/p'
)"

files=()
while IFS= read -r f; do
  [[ -z "$f" ]] && continue
  [[ "$f" != /* ]] && f="$cwd/$f"
  [[ "$f" == "$root"/* && "$f" == *.go && "$f" != */testdata/* && -f "$f" ]] || continue
  files+=("$f")
done <<<"$paths"
((${#files[@]} == 0)) && exit 0

if ! out="$(goimports -w "${files[@]}" 2>&1)"; then
  echo "goimports 실패:" >&2
  echo "$out" >&2
  exit 2
fi

pkgs=()
while IFS= read -r dir; do
  rel="${dir#"$root"}"
  pkgs+=(".${rel}")
done < <(for f in "${files[@]}"; do dirname "$f"; done | sort -u)

if ! out="$(cd "$root" && golangci-lint run "${pkgs[@]}" 2>&1)"; then
  echo "golangci-lint 실패 (${pkgs[*]}):" >&2
  echo "$out" >&2
  exit 2
fi
