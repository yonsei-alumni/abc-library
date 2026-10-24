# ABC 도서관

아카라카북클럽(ABC) 밴드에 올라온 글에서 책을 찾아 책, 저자, 태그별로 보여 주는 웹사이트다. https://abc-lib.web.app

하루 한 번 배치가 밴드 글과 댓글을 읽고, Claude로 책 정보와 개인 정보를 뺀 언급 요약을 만들어 DB에 반영한다.
관리자는 Google 계정으로 로그인해서 태그, 책, 언급, 관리자 목록을 고친다.

## 로컬 실행

`.envrc.example`을 `.envrc`로 복사해서 값을 채운 뒤 실행한다.

```bash
make migrate      # DB 스키마 적용
make band-login   # 밴드에 직접 로그인해서 세션 저장
make batch        # 밴드 글 수집과 책 추출
make web          # 웹 서버 실행
make check        # 포맷, 린트, 테스트
```

## 문서

- [기능 명세](docs/spec.md): 화면, 배치 흐름, 데이터 모델, 배포
- [인프라 구성](docs/architecture.md)
- [개발 지침](AGENTS.md)
