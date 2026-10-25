# ABC 도서관

아카라카북클럽(ABC) 밴드에 올라온 글에서 책을 찾아 책, 저자, 태그별로 보여 주는 웹사이트다. https://abc-lib.web.app

하루 한 번 배치가 밴드 글과 댓글을 읽고, Claude로 책 정보와 개인 정보를 뺀 언급 요약을 만들어 DB에 반영한다.
관리자는 Google 계정으로 로그인해서 태그, 책, 언급, 관리자 목록을 고친다.

## 로컬 개발

Docker로 띄운 PostgreSQL(`compose.yaml`)을 쓴다. 운영 DB(Neon)는 건드리지 않는다.

```bash
make db-seed            # 로컬 DB를 띄우고 마이그레이션과 가상의 데모 데이터 적용
make web                # 로컬 DB로 웹 서버 실행(http://localhost:8080)
make check              # 포맷, 린트, 단위 테스트
make test-integration   # 로컬 DB와 실제 Claude API를 쓰는 통합 테스트
make db-reset           # 로컬 DB를 지우고 데모 데이터로 다시 시작
```

관리 화면까지 쓰려면 `.envrc`에 로그인 설정을 채우고 `make db-seed ARGS="-admin <Google 계정 메일 주소>"`로 관리자를 더한다.
밴드 글로 로컬 DB를 채워 보려면 `make band-login` 뒤에 `make batch-local ARGS="-limit 3"`을 실행한다. Claude API 비용이 든다.

## 운영 작업

`.envrc.example`을 `.envrc`로 복사해서 운영 값을 채운 뒤 실행한다. 이 명령들은 `.envrc`의 운영 DB를 쓴다.

```bash
make release      # main을 release로 옮겨서 배포(마이그레이션이 바뀌었으면 운영 DB에 먼저 적용)
make migrate      # 운영 DB 스키마 적용(make release를 쓰지 않고 release에 merge할 때)
make band-login   # 밴드에 직접 로그인해서 세션 저장
make batch        # 밴드 글 수집과 책 추출
make status       # 최근 배치 실행 기록
make web-prod     # 로컬 코드를 운영 데이터로 실행
```

## 문서

- [기능 명세](docs/spec.md): 화면, 배치 흐름, 데이터 모델, 배포
- [인프라 구성](docs/architecture.md)
- [개발 지침](AGENTS.md)
