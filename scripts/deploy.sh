#!/usr/bin/env bash
# 이미지를 빌드해서 Artifact Registry에 올리고 Cloud Run에 배포한다.
#
#   scripts/deploy.sh web     웹 서버(Cloud Run 서비스)
#   scripts/deploy.sh batch   일일 배치(Cloud Run Job)
#
# 환경 변수와 비밀 값 연결은 서비스와 Job에 남아서 다음 배포에도 이어진다.
# DB_URL이 환경 변수에 있으면(로컬에서 .envrc를 읽은 경우) 그 값으로 바꾸고, 없으면(GitHub Actions) 그대로 둔다.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=gcp-env.sh
source "$root/scripts/gcp-env.sh"
cd "$root"

target="${1:-}"
case "$target" in
  web) region="$WEB_REGION" ;;
  batch) region="$BATCH_REGION" ;;
  *)
    echo "사용법: $0 <web|batch>" >&2
    exit 2
    ;;
esac

tag="$(git rev-parse --short=12 HEAD)"
# 추적하지 않는 파일(GitHub Actions의 gha-creds-*.json 등)은 이미지에 들어가지 않으므로 보지 않는다.
if [[ -n "$(git status --porcelain --untracked-files=no)" ]]; then
  tag="${tag}-dirty-$(date +%Y%m%d%H%M%S)"
fi
registry="${region}-docker.pkg.dev"
image="${registry}/${PROJECT}/${REPO}/${target}:${tag}"

gcloud auth configure-docker "$registry" --quiet >/dev/null 2>&1
docker build -f "Dockerfile.${target}" -t "$image" .
docker push "$image"

# 값에 쉼표가 들어가도 되도록 구분자를 |로 바꾼다.
env_vars=()
[[ -n "${DB_URL:-}" ]] && env_vars+=("DB_URL=${DB_URL}")

if [[ "$target" == web ]]; then
  flags=()
  secrets="DB_PWD=${DB_PWD_SECRET}:latest"
  # 관리자 로그인. GOOGLE_CLIENT_ID가 비어 있으면 관리자 로그인 없이 배포한다.
  if [[ -n "$GOOGLE_CLIENT_ID" ]]; then
    env_vars+=("GOOGLE_CLIENT_ID=${GOOGLE_CLIENT_ID}" "SITE_URL=${SITE_URL}")
    secrets+=",GOOGLE_CLIENT_SECRET=${GOOGLE_SECRET}:latest,SESSION_SECRET=${SESSION_SECRET_NAME}:latest"
  fi
  # 공개 접근은 처음 만들 때만 설정한다. GitHub Actions의 배포 계정은 IAM 정책을 바꾸지 못한다.
  if ! gcloud run services describe "$WEB_SERVICE" --region "$region" --project "$PROJECT" >/dev/null 2>&1; then
    flags+=(--allow-unauthenticated)
  fi
  ((${#env_vars[@]})) && flags+=(--update-env-vars "^|^$(IFS='|'; echo "${env_vars[*]}")")
  gcloud run deploy "$WEB_SERVICE" --image "$image" --region "$region" --project "$PROJECT" \
    --service-account "$WEB_SA" \
    --min-instances 0 --max-instances 3 \
    --set-secrets "$secrets" \
    "${flags[@]}"
  gcloud run services describe "$WEB_SERVICE" --region "$region" --project "$PROJECT" --format 'value(status.url)'
else
  env_vars+=("BAND_STATE_PATH=${BAND_STATE_MOUNT}")
  for name in ANTHROPIC_FEDERATION_RULE_ID ANTHROPIC_ORGANIZATION_ID ANTHROPIC_SERVICE_ACCOUNT_ID ANTHROPIC_WORKSPACE_ID; do
    [[ -n "${!name}" ]] && env_vars+=("${name}=${!name}")
  done
  secrets="DB_PWD=${DB_PWD_SECRET}:latest,${BAND_STATE_MOUNT}=${BAND_STATE_SECRET}:latest"
  # Workload Identity Federation 값이 없으면 Claude API 키를 쓴다.
  [[ -z "$ANTHROPIC_FEDERATION_RULE_ID" ]] && secrets+=",CLAUDE_API_KEY=${CLAUDE_KEY_SECRET}:latest"
  # 자동 재시도는 하지 않는다. 실패한 글은 다음 날 배치가 다시 시도한다.
  gcloud run jobs deploy "$BATCH_JOB" --image "$image" --region "$region" --project "$PROJECT" \
    --service-account "$BATCH_SA" \
    --cpu 2 --memory 2Gi --task-timeout 1h --max-retries 0 --tasks 1 \
    --set-secrets "$secrets" \
    --update-env-vars "^|^$(IFS='|'; echo "${env_vars[*]}")"
fi
