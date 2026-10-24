# 할 일

구성과 절차는 `docs/spec.md`의 '운영 환경'과 '배포'를 따른다. 끝낸 일은 지우고 git 기록에 남긴다.

## 곧 확인할 일

- [ ] 2026-10-25 03:00 첫 예약 실행 결과를 확인한다(`make status`, Cloud Run Job 로그). 밴드 세션 파일과 Claude API 키를 운영에서 처음 쓰는 실행이다.
- [ ] 북마크 배포 뒤 운영에서 사용자 로그인, 북마크, 카테고리 편집, 공개 주소(`/shared/{code}`)를 데스크톱과 휴대폰 화면에서 한 번씩 써 본다. Google 동의 화면에 메일 주소 요청이 나오지 않는지도 본다.
- [ ] 휴대폰에서 https://abc-lib.web.app 을 앱으로 설치해 본다. Android Chrome의 '앱 설치'와 iOS Safari의 '홈 화면에 추가'에서 아이콘과 이름이 제대로 나오는지, 비행기 모드에서 열어 본 화면과 오프라인 안내가 나오는지 확인한다.

## Claude API 비용(사용자)

- [ ] claude.ai **Settings → Billing → API credits**에서 Max 크레딧을 받을 Console 조직을 연결했는지 확인한다.
- [ ] Claude Console에서 `abc-library` 워크스페이스를 만들고 월 지출 한도($20)를 건 뒤, 그 워크스페이스의 API 키로 `claude-api-key` 비밀을 바꾼다(`gcloud secrets versions add claude-api-key --data-file=-`).

## 배치 개선

- [ ] 7일이 지난 글에 달린 새 댓글은 다시 확인하지 않는다. 게시글 목록 화면의 댓글 수를 저장해 두고, 바뀐 글만 다시 읽는 방식을 검토한다.

## 나중에(선택)

- [ ] Claude 인증을 API 키에서 Workload Identity Federation으로 바꾼다.
  - Claude Console **워크로드 ID → 워크로드 연결 → Google Cloud**에서 `abc-batch` 서비스 계정(`uniqueId` `116966074936228036004`)으로 규칙을 만든다(audience `https://api.anthropic.com`)
  - 나온 ID를 `scripts/gcp-env.sh`의 `ANTHROPIC_*`에 넣고 `make deploy-batch`를 실행하면 Job에서 API 키 연결이 빠진다. 그 뒤 `claude-api-key` 비밀을 지운다
- [ ] 사용자 도메인을 연결한다. Cloud Run 도메인 매핑이 싱가포르에서 안 되면 Firebase Hosting이나 Cloudflare를 앞에 둔다.
- [ ] 응답 gzip 압축. Cloud Run 직접 연결에서는 Google Frontend가 압축해 주지 않는다(`../../benelog/til`의 `compress.go` 참고).

## 수시로

- 새 모임 주제가 정해지면 관리 화면(`/admin/tags`)에서 모임 주제로 더하고 `make batch ARGS=-redo-from=1`로 다시 추출한다.
