# Google Cloud 배포 스크립트가 함께 쓰는 값이다. 다른 스크립트에서 source로 읽는다.
# 비밀 값은 없다. 비밀 값은 Secret Manager에 두고 이름으로만 가리킨다.

# gcloud가 PATH에 없으면 기본 설치 경로를 잡는다.
command -v gcloud >/dev/null 2>&1 || PATH="$HOME/google-cloud-sdk/bin:$PATH"

PROJECT=abc-library-512606
WEB_REGION=asia-southeast1   # 싱가포르. Neon DB와 같은 지역이다.
BATCH_REGION=asia-northeast3 # 서울. 밴드 세션이 한국 IP에서 쓰이게 한다.

# Artifact Registry 저장소. 이미지를 실행하는 지역마다 같은 이름으로 하나씩 둔다.
REPO=abc-library

WEB_SERVICE=abc-library
# Firebase Hosting 사이트. https://abc-lib.web.app이 웹 서비스로 요청을 넘긴다.
FIREBASE_SITE=abc-lib
SITE_URL=https://abc-lib.web.app

# 관리자 로그인에 쓰는 Google OAuth 클라이언트 ID. 비밀이 아니다(로그인 화면 주소에 그대로 나온다).
# 비워 두면 웹 서버가 관리자 로그인을 쓰지 않는다. 만드는 절차는 docs/spec.md의 '관리자 로그인 설정'에 있다.
GOOGLE_CLIENT_ID=""
BATCH_JOB=abc-batch
SCHEDULER_JOB=abc-batch-daily

WEB_SA="abc-web@${PROJECT}.iam.gserviceaccount.com"
BATCH_SA="abc-batch@${PROJECT}.iam.gserviceaccount.com"
SCHEDULER_SA="abc-scheduler@${PROJECT}.iam.gserviceaccount.com"
DEPLOY_SA="abc-deployer@${PROJECT}.iam.gserviceaccount.com"

# Secret Manager의 비밀 이름
DB_PWD_SECRET=db-pwd
BAND_STATE_SECRET=band-state
# 배치가 Claude를 부를 때 쓰는 API 키. Workload Identity Federation으로 바꾸면 쓰지 않는다.
CLAUDE_KEY_SECRET=claude-api-key
# 관리자 로그인(Google OAuth 클라이언트 보안 비밀)과 세션 쿠키 서명에 쓰는 비밀. 웹 서버만 읽는다.
GOOGLE_SECRET=google-client-secret
SESSION_SECRET_NAME=session-secret
# 배치 Job에서 band-state 비밀을 파일로 올리는 경로
BAND_STATE_MOUNT=/secrets/band/band-state.json

# GitHub Actions가 서비스 계정 키 없이 배포하도록 쓰는 Workload Identity Federation
GITHUB_REPO=yonsei-alumni/abc-library
WIF_POOL=github
WIF_PROVIDER=abc-library

# 배치가 Claude를 Workload Identity Federation으로 호출할 때 쓰는 값이다. 지금은 비워 두고 API 키를 쓴다.
# Claude Console의 Workload identity 규칙에서 나온 ID라서 비밀이 아니다.
# 값을 채워서 배포하면 extract.NewClient가 API 키 대신 WIF를 쓴다.
ANTHROPIC_FEDERATION_RULE_ID=""
ANTHROPIC_ORGANIZATION_ID=""
ANTHROPIC_SERVICE_ACCOUNT_ID=""
ANTHROPIC_WORKSPACE_ID=""
