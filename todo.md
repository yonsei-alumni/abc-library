# 할 일

## Google Cloud 배포

구성과 절차는 `docs/spec.md`의 '운영 환경'과 '배포'를 따른다. 2026-10-24에 웹 서버, 배치 Job, 스케줄러, 실패 알림까지 배포했고, 같은 날 프로젝트를 `benelog`에서 `abc-library-512606`으로 옮겼다.

- [ ] 2026-10-25 03:00 첫 예약 실행 결과를 확인한다(`make status`, Cloud Run Job 로그). 밴드 세션 파일과 Claude API 키를 운영에서 처음 쓰는 실행이다.
- [ ] 이 변경을 `main`에 push한 뒤 GitHub Actions의 CI와 배포가 통과하는지 확인한다.
- [ ] claude.ai **Settings → Billing → API credits**에서 Max 크레딧을 받을 Console 조직을 연결했는지 확인한다(사용자).
- [ ] Claude Console에서 `abc-library` 워크스페이스를 만들고 월 지출 한도($20)를 건 뒤, 그 워크스페이스의 API 키로 `claude-api-key` 비밀을 바꾼다.
- [ ] (나중에) Claude 인증을 API 키에서 Workload Identity Federation으로 바꾼다.
  - Claude Console **워크로드 ID → 워크로드 연결 → Google Cloud**에서 `abc-batch` 서비스 계정(`uniqueId` `116966074936228036004`)으로 규칙을 만든다(audience `https://api.anthropic.com`)
  - 나온 ID를 `scripts/gcp-env.sh`의 `ANTHROPIC_*`에 넣고 `make deploy-batch`를 실행하면 Job에서 API 키 연결이 빠진다. 그 뒤 `claude-api-key` 비밀을 지운다
- [ ] (선택) 사용자 도메인을 연결한다. Cloud Run 도메인 매핑이 싱가포르에서 안 되면 Firebase Hosting이나 Cloudflare를 앞에 둔다.
- [ ] (선택) 응답 gzip 압축. Cloud Run 직접 연결에서는 Google Frontend가 압축해 주지 않는다(`../../benelog/til`의 `compress.go` 참고).

## 관리자 기능

- [ ] 운영 DB에 `20261024000003_admin` 마이그레이션을 적용한다(`make migrate`). 적용하기 전에 관리자 기능 커밋을 push하면 배포된 웹 서버가 `tags` 테이블과 `mentions.hidden` 컬럼을 찾지 못한다.
- [ ] Google OAuth 클라이언트를 만들고 `scripts/gcp-env.sh`의 `GOOGLE_CLIENT_ID`를 채운 뒤 `make gcp-setup`, `make deploy-web`을 실행한다(`docs/spec.md`의 '관리자 로그인 설정').
- [ ] https://abc-lib.web.app 에서 benelog@gmail.com으로 로그인해서 태그 화면과 책 편집을 확인한다.

## 데이터와 배치

- [ ] 저장한 글 전체를 새 프롬프트로 다시 추출한다(`make batch ARGS="-limit 0 -redo-from 1"`, 로컬). 댓글 판정, 관련 없는 링크 제외, 시리즈 한 권(세트) 규칙, '대화방 주간 요약' 호칭이 기존 글에 반영된다. 밴드 글 187개를 다시 읽으므로 그날 예약 실행과 겹치지 않게 한다. 2026-10-24에 수확자(세트로 합침)와 '주간 대화방' 문구만 DB에서 직접 고쳤다.
- [x] 첫 전체 수집에서 '없는 글'로 건너뛴 19, 30, 42, 115, 124번은 삭제된 글임을 확인했다(밴드가 '삭제되었거나 찾을 수 없습니다' 알림을 띄운다).
- [ ] 7일이 지난 글에 달린 새 댓글은 다시 확인하지 않는다. 게시글 목록 화면의 댓글 수를 저장해 두고, 바뀐 글만 다시 읽는 방식을 검토한다.
- [ ] 새 모임 주제가 정해지면 관리 화면(`/admin/tags`)에서 모임 주제로 더하고 다시 추출한다.
