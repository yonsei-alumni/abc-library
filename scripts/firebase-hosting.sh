#!/usr/bin/env bash
# Firebase Hosting 사이트(https://abc-lib.web.app)가 모든 요청을 Cloud Run 웹 서비스로 넘기게 한다.
#
#   make firebase-hosting
#
# Firebase CLI 없이 gcloud 로그인 토큰으로 Hosting REST API를 부른다. 사이트가 없으면 만들고,
# rewrite 설정만 담은 새 버전을 배포한다. Cloud Run을 다시 배포해도 이 설정은 바꿀 필요가 없다.
# 프로젝트에 Firebase를 추가하는 일(약관 동의, 요금제 확인)은 Firebase 콘솔에서 사람이 한 번 한다.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=gcp-env.sh
source "$root/scripts/gcp-env.sh"

api=https://firebasehosting.googleapis.com/v1beta1
token="$(gcloud auth print-access-token)"
call() {
  curl -sS --fail-with-body -H "Authorization: Bearer $token" -H "x-goog-user-project: $PROJECT" \
    -H "Content-Type: application/json" "$@"
}
field() { python3 -c "import json,sys; print(json.load(sys.stdin)['$1'])"; }

if ! call "$api/projects/$PROJECT/sites/$FIREBASE_SITE" >/dev/null 2>&1; then
  call -X POST -d '{}' "$api/projects/$PROJECT/sites?siteId=$FIREBASE_SITE" >/dev/null
  echo "사이트 $FIREBASE_SITE을 만들었습니다."
fi

config=$(printf '{"config":{"rewrites":[{"glob":"**","run":{"serviceId":"%s","region":"%s"}}]}}' "$WEB_SERVICE" "$WEB_REGION")
version="$(call -X POST -d "$config" "$api/sites/$FIREBASE_SITE/versions" | field name)"
call -X PATCH -d '{"status":"FINALIZED"}' "$api/$version?update_mask=status" >/dev/null
call -X POST "$api/sites/$FIREBASE_SITE/releases?versionName=$version" >/dev/null
echo "https://${FIREBASE_SITE}.web.app → Cloud Run $WEB_SERVICE ($WEB_REGION) 배포 완료"
