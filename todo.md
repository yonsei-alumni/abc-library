# 할 일

## Google Cloud 배포

구성은 `docs/spec.md`의 '운영 환경'을 따른다. 웹은 Cloud Run(`asia-southeast1`), 배치는 Cloud Run Job(`asia-northeast3`)이다.

### 사용자가 할 일

- [ ] 배포할 GCP 프로젝트를 정하고 결제 계정을 연결한다. 새로 만들지 기존 프로젝트를 쓸지 정한다.
- [ ] `gcloud` CLI를 설치하고 `gcloud auth login`, `gcloud auth application-default login`으로 로그인한다.
- [ ] claude.ai **Settings → Billing → API credits**에서 Max 크레딧을 받을 Console 조직을 연결한다. 연결 여부를 아직 확인하지 않았다.

### 준비

- [ ] 필요한 API를 켠다: Cloud Run, Artifact Registry, Secret Manager, Cloud Scheduler, Cloud Build.
- [ ] Artifact Registry Docker 저장소를 만든다.
- [ ] 서비스 계정을 만든다.
  - `abc-web`: Secret Manager의 `DB_PWD` 읽기
  - `abc-batch`: `DB_PWD`와 밴드 세션 읽기. Claude WIF에도 쓴다(기본 Compute 서비스 계정은 쓰지 않는다)
  - `abc-scheduler`: 배치 Job 실행(`roles/run.invoker`)
- [ ] Secret Manager에 비밀 값을 올린다.
  - `db-pwd`: `DB_PWD`
  - `band-state`: `band-state.json`. 세션이 만료되면 로컬에서 `make band-login`을 다시 하고 새 버전으로 올린다. 이 절차를 `make band-secret` 같은 명령으로 만든다

### 웹 서버

- [ ] `Dockerfile.web`: Go 바이너리 하나를 distroless 이미지에 담는다. 템플릿과 정적 파일은 바이너리에 포함되어 있다.
- [ ] Cloud Run 서비스로 배포한다(`asia-southeast1`, 최소 인스턴스 0, 공개 접근).
  - 환경 변수 `DB_URL`, 비밀 값 `DB_PWD`
- [ ] 검색엔진 수집을 허용할지 정한다(`robots.txt` 또는 `noindex`).
- [ ] (선택) 사용자 도메인을 연결한다. Cloud Run 도메인 매핑이 싱가포르에서 안 되면 Firebase Hosting이나 Cloudflare를 앞에 둔다.

### 일일 배치

- [ ] `Dockerfile.batch`: playwright-go 버전에 맞는 Playwright 공식 이미지에 Go 바이너리를 담는다. 브라우저를 이미지 빌드 때 설치해서, 실행 때마다 내려받지 않게 한다.
- [ ] Cloud Run Job으로 배포한다(`asia-northeast3`, 2 vCPU, 2GiB, 제한 시간 1시간, 자동 재시도 없음).
  - 환경 변수 `DB_URL`, `BAND_STATE_PATH`(비밀 값 볼륨 경로), 비밀 값 `DB_PWD`와 `band-state` 파일
  - 실행 명령은 `batch run`이다. 마이그레이션도 이때 적용된다
- [ ] Claude 인증을 Workload Identity Federation으로 설정한다.
  - Claude Console **Workload identity → Connect workload → Google Cloud**에서 `abc-batch` 서비스 계정의 `uniqueId`와 이메일로 규칙을 만든다(audience `https://api.anthropic.com`)
  - Job에 `ANTHROPIC_FEDERATION_RULE_ID`, `ANTHROPIC_ORGANIZATION_ID`, `ANTHROPIC_SERVICE_ACCOUNT_ID`, `ANTHROPIC_WORKSPACE_ID`를 넣는다. `CLAUDE_API_KEY`는 넣지 않는다
  - Console에서 `abc-library` 워크스페이스를 만들고 월 지출 한도를 건다
- [ ] Cloud Scheduler로 하루 한 번(예: 새벽 3시 KST) Job을 실행한다.
- [ ] Job이 실패하면 메일을 받도록 Cloud Monitoring 알림을 건다. 밴드 세션이 만료되면 Job이 실패하므로 이 알림으로 알 수 있다.

### 자동화

- [ ] GitHub Actions: push마다 `make ci`를 실행한다.
- [ ] GitHub Actions: `main`에 push하면 이미지를 빌드해서 배포한다(GitHub → GCP Workload Identity Federation).

## 데이터와 배치

- [ ] 댓글 판정 규칙을 프롬프트에 넣기 전에 처리한 글을 `make batch ARGS="-limit 0 -redo-from 1"`로 다시 추출할지 정한다.
- [x] 첫 전체 수집에서 '없는 글'로 건너뛴 19, 30, 42, 115, 124번은 삭제된 글임을 확인했다(밴드가 '삭제되었거나 찾을 수 없습니다' 알림을 띄운다).
- [ ] 7일이 지난 글에 달린 새 댓글은 다시 확인하지 않는다. 게시글 목록 화면의 댓글 수를 저장해 두고, 바뀐 글만 다시 읽는 방식을 검토한다.
- [ ] 새 모임 주제가 정해지면 `book.TopicTags` 맨 앞에 더하고 다시 추출한다.
