# abc-library 기능 명세

## 화면

모든 화면 위쪽에 검색창이 있다. 제목, 저자, 태그에서 대소문자와 공백을 무시하고 찾으며, 입력하는 동안 HTMX로 결과를 바꾼다.

### 홈: 타임라인

- 최근 소식을 시각의 역순으로 보여 준다. 같은 글의 본문과 댓글이 같은 책을 다뤘으면 소식 하나로 묶는다.
- 소식은 두 종류다.
  - 새 책: 처음 언급된 책이 등록됐다.
  - 이야기 추가: 이미 등록된 책이 다른 글이나 댓글에서 다시 언급됐다.
- 각 항목에는 날짜, 표지, 제목, 저자를 넣고 책 상세로 연결한다.
- 다음 페이지는 HTMX로 이어 붙인다.

### 책 목록

- 표지 썸네일, 제목, 저자, 다룬 게시글 수, 마지막 언급일을 격자로 보여 준다.
- 태그(`?tag=`), 저자(`?author=`), 검색어(`?q=`)로 거를 수 있다.

### 책 상세

- 위쪽에 표지, 제목, 저자, 출판사, 태그, 교보문고 구매 링크를 둔다.
  - 구매 링크는 교보문고 상품 페이지다. 상품을 확정하지 못한 책은 교보문고 검색 결과로 연결한다.
- 아래쪽에는 이 책에 대한 언급을 댓글처럼 모아 보여 준다.
  - 게시글별로 묶는다. 본문 언급이 있으면 그것을 위에 두고, 그 글의 댓글 언급을 아래에 답글처럼 붙인다. 댓글에서만 다룬 글은 댓글 언급만 보인다.
  - 각 언급에는 날짜, AI 요약, 그 언급에 들어 있던 링크를 넣는다. 묶음마다 밴드 원문 링크를 둔다.
  - 요약은 개인 정보를 뺀 것이다. 원문과 작성자 이름은 보여 주지 않는다.
  - 원문 링크 형식은 `https://band.us/band/102765569/post/{post_no}`이다. 비공개 밴드라서 멤버만 열어 볼 수 있다.

### 태그, 저자

- 태그 화면은 모임 주제를 정한 순서로 먼저 보여 주고, 그 밖의 태그를 책 수 순서로 보여 준다.
- 저자 화면은 저자와 책 수를 보여 준다. 누르면 책 목록으로 거른다.

## 일일 배치

1. 밴드에서 지난 수집 이후 새로 올라온 글, 지난 실행에서 처리하지 못한 글, 최근 7일 사이 글 중 본문이 수정되거나 댓글이 달린 글을 읽는다. 접힌 이전 댓글은 펼쳐서 읽는다.
   - '지난 수집'의 기준은 저장된 게시글 번호의 최댓값이다. 처리하지 못한 글은 `pending_posts`에 남겨서 최대 5번까지 다시 시도한다.
2. 내용(본문, 링크, 사진, 댓글)이 바뀐 글만 Claude에 보낸다. Claude는 본문, 링크, 첨부 사진, 댓글로 책을 찾고, `search_kyobo` 도구로 교보문고를 검색해서 ISBN을 고르고, 책마다 본문 언급과 댓글 언급의 요약을 만든다.
   - 댓글이 본문의 책에 대한 것인지, 댓글에서 새로 소개한 다른 책에 대한 것인지는 Claude가 판정한다.
3. 코드는 Claude의 결과를 검증하고 보탠다.
   - 교보문고 검색 결과에 없던 ISBN, 정해진 목록 밖의 태그, 없는 댓글 번호는 버린다.
   - 언급에 들어 있던 링크를 서점 구매 링크만 빼고 모두 붙인다. 본문이 책 한 권만 다뤘으면 본문 링크는 모두 그 책의 본문 언급에 붙이고, 여러 권을 다뤘으면 Claude가 고른 번호를 따른다. 댓글 링크는 그 댓글의 언급에 붙인다.
4. 책과 언급을 저장한다. 수정된 글은 그 글의 언급을 새 결과로 바꾸고, 어느 언급에도 남지 않게 된 책은 지운다.

같은 게시글을 여러 번 처리해도 결과가 같아야 한다. 한 번에 추출하는 게시글 수에는 상한(기본 30)을 둔다.
실행마다 `batch_runs`에 실행 전 마지막 글, 밴드 최신 글, 처리 건수, 오류를 남긴다. `make status`로 최근 실행과 다시 시도할 글을 본다.
프롬프트나 태그 목록을 바꾼 뒤에는 `-redo-from 1`로 저장한 글을 다시 추출한다.

## 태그

- 책 한 권에 태그를 1~5개 단다.
- 모임 주제 태그(`book.TopicTags`, 현재 AI, SF, 과학)는 책의 내용이 그 주제와 직접 관련되거나 그 주제의 모임 도서로 다룬 경우에 붙인다.
- 갈래 태그(`book.KindTags`)는 소설, 인문, 역사 같은 책의 종류다.

## 책 식별

- ISBN-13이 있으면 ISBN으로 같은 책을 찾는다.
- 같은 작품의 다른 판본(양장본, 개정판 등)은 ISBN이 달라도 작품 키로 묶는다. 작품 키는 괄호 속 판본 표기와 부제를 뺀 제목에 대표 저자를 붙여 정규화한 값이다.
- ISBN을 확정하지 못한 책도 작품 키로 저장하고, 나중에 ISBN을 찾으면 채운다.

## 데이터 모델

| 테이블 | 주요 컬럼 | 비고 |
|---|---|---|
| `posts` | `band_post_no`(유일), `posted_at`, `content_hash`, `processed_at` | 본문, 댓글, 작성자는 저장하지 않는다 |
| `books` | `isbn13`(유일), `work_key`(유일), `title`, `authors`(배열), `publisher`, `kyobo_pid` | 표지 주소는 ISBN으로 만든다 |
| `book_tags` | `book_id`, `tag` | 책 한 권에 태그 여러 개 |
| `mentions` | `book_id`, `post_id`, `kind`(`post`/`comment`), `mentioned_at`, `summary`, `links`(JSON) | 책 상세의 언급 목록과 타임라인의 원천 |
| `batch_runs` | `started_at`, `finished_at`, `last_post_no`, `latest_post_no`, 처리 건수, `error` | 배치 실행 기록 |
| `pending_posts` | `band_post_no`, `reason`(`failed`/`missing`), `attempts`, `last_error` | 다음 실행에서 다시 시도할 글 |

## 운영 환경

| 구성 요소 | 위치 |
|---|---|
| 웹 서버 | Cloud Run, `asia-southeast1`(싱가포르) |
| DB | Neon 프로젝트 `abc-library`, `aws-ap-southeast-1`(싱가포르), Postgres 18, DB 이름 `abc_library` |
| 일일 배치 | Cloud Run Job, `asia-northeast3`(서울). 밴드 세션이 한국 IP에서 쓰이게 한다. Cloud Scheduler로 하루 한 번 실행한다 |
| 비밀 값 | Secret Manager(`DB_PWD`, 밴드 `storageState`, Claude API 키) |
| Claude 인증 | `CLAUDE_API_KEY`. 운영 배치는 Secret Manager의 `claude-api-key`. Workload Identity Federation은 코드만 준비되어 있다 |
| 이미지 | Artifact Registry `abc-library` 저장소(웹은 `asia-southeast1`, 배치는 `asia-northeast3`). 최근 5개만 남긴다 |
| 실행 일정 | Cloud Scheduler `abc-batch-daily`가 매일 03:00(KST)에 Job을 실행한다. Job이 실패하면 Cloud Monitoring이 메일을 보낸다 |

## 설정 속성

| 이름 | 설명 |
|---|---|
| `DB_URL` | 비밀번호를 뺀 Postgres 연결 URL |
| `DB_PWD` | DB 비밀번호 |
| `CLAUDE_API_KEY` | Claude API 키 |
| `ANTHROPIC_FEDERATION_RULE_ID` 외 | Workload Identity Federation 설정. 설정하면 `CLAUDE_API_KEY` 대신 쓴다 |
| `BAND_NO` | 밴드 번호(기본 `102765569`) |
| `BAND_STATE_PATH` | 밴드 로그인 세션 파일(기본 `band-state.json`) |
| `PORT` | 웹 서버 포트(기본 `8080`) |

## 배포

Google Cloud 프로젝트 `benelog`에 배포한다. 값은 `scripts/gcp-env.sh`에 있다.

| 구성 요소 | 이름 |
|---|---|
| 웹 서버 | Cloud Run 서비스 `abc-library`, 서비스 계정 `abc-web`, 최소 인스턴스 0, 공개 접근 |
| 일일 배치 | Cloud Run Job `abc-batch`, 서비스 계정 `abc-batch`, 2 vCPU, 2GiB, 제한 시간 1시간, 재시도 없음 |
| 비밀 값 | `db-pwd`(웹, 배치), `band-state`(배치, `/secrets/band/band-state.json`에 파일로 올림), `claude-api-key`(배치) |
| 실행 계정 | `abc-scheduler`(Job 실행), `abc-deployer`(GitHub Actions 배포) |

처음 한 번은 `.envrc`를 읽은 셸에서 다음 순서로 실행한다. 모두 다시 실행해도 된다.

```bash
make gcp-setup                  # API, 저장소, 서비스 계정, 비밀, GitHub Actions WIF
make band-secret                # band-state.json을 Secret Manager에 올림
make deploy-web deploy-batch    # 이미지 빌드, 푸시, 배포(DB_URL도 이때 설정)
make gcp-schedule               # 매일 실행과 실패 알림
```

그 뒤에는 `main`에 push하면 GitHub Actions가 배포한다. 환경 변수와 비밀 값 연결은 서비스와 Job에 남아 있어서 워크플로는 이미지만 바꾼다.

- 밴드 세션이 만료되면 Job이 실패하고 알림 메일이 온다. `make band-login` 뒤에 `make band-secret`을 실행한다. Job은 비밀의 최신 버전을 읽으므로 다시 배포하지 않아도 된다.
- Claude API 키를 바꾸려면 `gcloud secrets versions add claude-api-key --data-file=-`로 새 버전을 올린다.
- 웹 서버는 `robots.txt`와 `X-Robots-Tag: noindex`로 검색엔진 수집을 막는다.
- Cloud Run 앞단의 Google Frontend가 정확히 `/healthz`를 가로채므로 상태 확인은 `/health`로 한다.
