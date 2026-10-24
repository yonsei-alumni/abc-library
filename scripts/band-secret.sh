#!/usr/bin/env bash
# 로컬의 밴드 세션 파일을 Secret Manager의 새 버전으로 올리고, 이전 버전은 사용 중지한다.
# 세션이 만료되면 make band-login으로 다시 로그인한 뒤 실행한다.
#
#   make band-secret
#
# 배치 Job은 비밀의 latest 버전을 쓰므로 다시 배포하지 않아도 다음 실행부터 새 세션을 쓴다.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=gcp-env.sh
source "$root/scripts/gcp-env.sh"

path="${BAND_STATE_PATH:-band-state.json}"
[[ "$path" = /* ]] || path="$root/$path"
if [[ ! -s "$path" ]]; then
  echo "세션 파일 $path가 없습니다. make band-login으로 먼저 로그인하세요." >&2
  exit 1
fi

new="$(gcloud secrets versions add "$BAND_STATE_SECRET" --project "$PROJECT" --data-file "$path" --format 'value(name)')"
new="${new##*/}"
echo "$BAND_STATE_SECRET 비밀에 버전 $new을 올렸습니다."

for v in $(gcloud secrets versions list "$BAND_STATE_SECRET" --project "$PROJECT" --filter 'state=ENABLED' --format 'value(name)'); do
  v="${v##*/}"
  [[ "$v" == "$new" ]] && continue
  gcloud secrets versions disable "$v" --secret "$BAND_STATE_SECRET" --project "$PROJECT" --quiet >/dev/null
  echo "이전 버전 $v을 사용 중지했습니다."
done
