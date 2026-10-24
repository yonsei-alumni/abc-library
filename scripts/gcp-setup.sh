#!/usr/bin/env bash
# Google Cloud에 배포하기 전에 필요한 리소스를 만든다.
#
#   make gcp-setup
#
# API, Artifact Registry 저장소, 서비스 계정, Secret Manager 비밀, GitHub Actions용
# Workload Identity Federation을 만든다. 이미 있는 리소스는 건너뛰므로 다시 실행해도 된다.
# db-pwd, claude-api-key 비밀에 버전이 없으면 DB_PWD, CLAUDE_API_KEY 환경 변수 값을 첫 버전으로 올린다.
# gcp-env.sh에 GOOGLE_CLIENT_ID가 있으면 관리자 로그인용 비밀도 만든다. google-client-secret은
# GOOGLE_CLIENT_SECRET 환경 변수 값을 올리고, session-secret은 임의의 값을 만들어 올린다.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=gcp-env.sh
source "$root/scripts/gcp-env.sh"

PROJECT_NUMBER=$(gcloud projects describe "$PROJECT" --format='value(projectNumber)')
echo "프로젝트 $PROJECT (번호 $PROJECT_NUMBER)"

echo "== API 사용 설정 =="
gcloud services enable --project "$PROJECT" \
  run.googleapis.com artifactregistry.googleapis.com secretmanager.googleapis.com \
  cloudscheduler.googleapis.com monitoring.googleapis.com \
  iam.googleapis.com iamcredentials.googleapis.com sts.googleapis.com

echo "== Artifact Registry 저장소 =="
cleanup_policy="$(mktemp)"
trap 'rm -f "$cleanup_policy"' EXIT
# 최근 이미지 5개만 남기고 7일이 지난 이미지는 지운다.
cat >"$cleanup_policy" <<'EOF'
[
  {"name": "keep-recent", "action": {"type": "Keep"}, "mostRecentVersions": {"keepCount": 5}},
  {"name": "delete-old", "action": {"type": "Delete"}, "condition": {"tagState": "ANY", "olderThan": "7d"}}
]
EOF
for region in "$WEB_REGION" "$BATCH_REGION"; do
  gcloud artifacts repositories describe "$REPO" --location "$region" --project "$PROJECT" >/dev/null 2>&1 ||
    gcloud artifacts repositories create "$REPO" --location "$region" --project "$PROJECT" \
      --repository-format docker --description "abc-library 이미지"
  gcloud artifacts repositories set-cleanup-policies "$REPO" --location "$region" --project "$PROJECT" \
    --policy "$cleanup_policy" --no-dry-run >/dev/null
  echo "  $region/$REPO"
done

echo "== 서비스 계정 =="
create_sa() {
  local email="$1" name="$2"
  gcloud iam service-accounts describe "$email" --project "$PROJECT" >/dev/null 2>&1 ||
    gcloud iam service-accounts create "${email%%@*}" --project "$PROJECT" --display-name "$name"
  echo "  $email"
}
create_sa "$WEB_SA" "abc-library 웹 서버"
create_sa "$BATCH_SA" "abc-library 일일 배치"
create_sa "$SCHEDULER_SA" "abc-library 배치 실행 스케줄러"
create_sa "$DEPLOY_SA" "abc-library GitHub Actions 배포"

echo "== Secret Manager =="
create_secret() {
  gcloud secrets describe "$1" --project "$PROJECT" >/dev/null 2>&1 ||
    gcloud secrets create "$1" --project "$PROJECT" --replication-policy automatic
}
has_version() {
  [[ -n "$(gcloud secrets versions list "$1" --project "$PROJECT" --filter 'state=ENABLED' --limit 1 --format 'value(name)')" ]]
}
# 비밀이 없으면 만들고, 사용 중인 버전이 없으면 환경 변수 값을 첫 버전으로 올린다.
secret_from_env() {
  local secret="$1" var="$2"
  create_secret "$secret"
  has_version "$secret" && return
  if [[ -z "${!var:-}" ]]; then
    echo "$var 환경 변수가 없어서 $secret 비밀에 값을 올리지 못했습니다. .envrc를 읽은 셸에서 다시 실행하세요." >&2
    exit 1
  fi
  printf '%s' "${!var}" | gcloud secrets versions add "$secret" --project "$PROJECT" --data-file=- >/dev/null
  echo "  $secret: 첫 버전을 올렸습니다"
}
secret_from_env "$DB_PWD_SECRET" DB_PWD
secret_from_env "$CLAUDE_KEY_SECRET" CLAUDE_API_KEY
create_secret "$BAND_STATE_SECRET"
if ! has_version "$BAND_STATE_SECRET"; then
  echo "  $BAND_STATE_SECRET: 버전이 없습니다. make band-secret으로 밴드 세션을 올리세요"
fi

if [[ -n "$GOOGLE_CLIENT_ID" ]]; then
  secret_from_env "$GOOGLE_SECRET" GOOGLE_CLIENT_SECRET
  create_secret "$SESSION_SECRET_NAME"
  if ! has_version "$SESSION_SECRET_NAME"; then
    # 값을 바꾸면 모든 관리자가 로그아웃된다.
    openssl rand -base64 48 | tr -d '\n' | gcloud secrets versions add "$SESSION_SECRET_NAME" --project "$PROJECT" --data-file=- >/dev/null
    echo "  $SESSION_SECRET_NAME: 임의의 값으로 첫 버전을 올렸습니다"
  fi
fi

grant_secret() {
  gcloud secrets add-iam-policy-binding "$1" --project "$PROJECT" \
    --member "serviceAccount:$2" --role roles/secretmanager.secretAccessor --condition None >/dev/null
  echo "  $1 → $2"
}
grant_secret "$DB_PWD_SECRET" "$WEB_SA"
grant_secret "$DB_PWD_SECRET" "$BATCH_SA"
grant_secret "$BAND_STATE_SECRET" "$BATCH_SA"
grant_secret "$CLAUDE_KEY_SECRET" "$BATCH_SA"
if [[ -n "$GOOGLE_CLIENT_ID" ]]; then
  grant_secret "$GOOGLE_SECRET" "$WEB_SA"
  grant_secret "$SESSION_SECRET_NAME" "$WEB_SA"
fi

echo "== GitHub Actions 배포 권한 =="
gcloud iam workload-identity-pools describe "$WIF_POOL" --location global --project "$PROJECT" >/dev/null 2>&1 ||
  gcloud iam workload-identity-pools create "$WIF_POOL" --location global --project "$PROJECT" \
    --display-name "GitHub Actions"
# 저장소와 main 브랜치 조건을 건다. 이 조건이 없으면 다른 저장소나 브랜치의 워크플로도 토큰을 받는다.
gcloud iam workload-identity-pools providers describe "$WIF_PROVIDER" \
  --location global --workload-identity-pool "$WIF_POOL" --project "$PROJECT" >/dev/null 2>&1 ||
  gcloud iam workload-identity-pools providers create-oidc "$WIF_PROVIDER" \
    --location global --workload-identity-pool "$WIF_POOL" --project "$PROJECT" \
    --display-name "abc-library main" \
    --issuer-uri "https://token.actions.githubusercontent.com" \
    --attribute-mapping "google.subject=assertion.sub,attribute.repository=assertion.repository,attribute.ref=assertion.ref" \
    --attribute-condition "assertion.repository == '${GITHUB_REPO}' && assertion.ref == 'refs/heads/main'"

gcloud iam service-accounts add-iam-policy-binding "$DEPLOY_SA" --project "$PROJECT" \
  --role roles/iam.workloadIdentityUser \
  --member "principalSet://iam.googleapis.com/projects/${PROJECT_NUMBER}/locations/global/workloadIdentityPools/${WIF_POOL}/attribute.repository/${GITHUB_REPO}" >/dev/null
# 서비스와 Job을 배포할 수 있지만 IAM 정책은 바꾸지 못한다. 공개 접근은 처음 배포할 때 사람이 설정한다.
gcloud projects add-iam-policy-binding "$PROJECT" \
  --member "serviceAccount:$DEPLOY_SA" --role roles/run.developer --condition None >/dev/null
echo "  roles/run.developer"
for region in "$WEB_REGION" "$BATCH_REGION"; do
  gcloud artifacts repositories add-iam-policy-binding "$REPO" --location "$region" --project "$PROJECT" \
    --member "serviceAccount:$DEPLOY_SA" --role roles/artifactregistry.writer >/dev/null
  echo "  roles/artifactregistry.writer ($region/$REPO)"
done
# 배포할 때 서비스와 Job의 실행 서비스 계정을 지정할 수 있어야 한다.
for sa in "$WEB_SA" "$BATCH_SA"; do
  gcloud iam service-accounts add-iam-policy-binding "$sa" --project "$PROJECT" \
    --member "serviceAccount:$DEPLOY_SA" --role roles/iam.serviceAccountUser >/dev/null
  echo "  roles/iam.serviceAccountUser ($sa)"
done

echo
echo "완료. .github/workflows/ci.yml의 값과 맞는지 확인하세요."
echo "  WIF_PROVIDER: projects/${PROJECT_NUMBER}/locations/global/workloadIdentityPools/${WIF_POOL}/providers/${WIF_PROVIDER}"
echo "  DEPLOY_SA:    ${DEPLOY_SA}"
