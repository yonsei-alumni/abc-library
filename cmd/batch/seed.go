package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/url"
	"os"
	"slices"
	"time"

	"github.com/yonsei-alumni/abc-library/internal/book"
	"github.com/yonsei-alumni/abc-library/internal/store"
)

// seed는 로컬 DB에 가상의 데모 데이터를 넣는다. 화면을 개발하거나 도움말 화면을 캡처할 때 쓴다.
// 책의 서지 정보는 실제 책이지만 게시글, 요약, 링크는 지어낸 것이다.
// 운영 DB에 넣지 않도록 DB_URL이 이 컴퓨터를 가리킬 때만 실행한다. 같은 번호의 글을 덮어쓰므로 여러 번 실행해도 된다.
func seed(ctx context.Context, log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	admin := fs.String("admin", "", "관리 화면에 로그인할 Google 계정 메일 주소(선택)")
	_ = fs.Parse(args)
	if err := requireLocalDB(); err != nil {
		return err
	}

	st, err := openStore(ctx, log)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	posts := demoPosts(time.Now())
	for _, p := range posts {
		if err := st.SavePost(ctx, p.post, p.findings); err != nil {
			return err
		}
	}
	if *admin != "" {
		if err := st.AddAdmin(ctx, *admin, "seed"); err != nil && !errors.Is(err, store.ErrConflict) {
			return err
		}
	}
	log.Info("데모 데이터를 넣었다", "글", len(posts))
	return nil
}

// requireLocalDB는 DB_URL이 이 컴퓨터의 DB를 가리키지 않으면 오류를 돌려준다.
func requireLocalDB() error {
	u, err := url.Parse(os.Getenv("DB_URL"))
	if err != nil || !slices.Contains([]string{"localhost", "127.0.0.1", "::1"}, u.Hostname()) {
		return errors.New("데모 데이터는 로컬 DB에만 넣습니다. make db-seed로 실행하세요")
	}
	return nil
}

type demoPost struct {
	post     book.Post
	findings []book.Finding
}

// demoPosts는 now를 기준으로 최근 몇 주 동안 올라온 것처럼 꾸민 게시글이다.
// 타임라인에 새 책과 이야기 추가가 함께 나오고, 본문 언급, 댓글 언급, 링크, 표지가 없는 책을 모두 볼 수 있게 했다.
func demoPosts(now time.Time) []demoPost {
	story := book.Book{ISBN13: "9791196914806", KyoboPID: "S000001981838", Title: "당신 인생의 이야기", Authors: []string{"테드 창"},
		Publisher: "엘리", Tags: []string{"SF", "소설"}}
	breath := book.Book{Title: "숨", Authors: []string{"테드 창"}, Publisher: "엘리", Tags: []string{"SF", "소설"}}
	cosmos := book.Book{Title: "코스모스", Authors: []string{"칼 세이건"}, Publisher: "사이언스북스", Tags: []string{"과학"}}
	gene := book.Book{Title: "이기적 유전자", Authors: []string{"리처드 도킨스"}, Publisher: "을유문화사", Tags: []string{"과학"}}
	sapiens := book.Book{Title: "사피엔스", Authors: []string{"유발 하라리"}, Publisher: "김영사", Tags: []string{"역사", "인문"}}
	klara := book.Book{Title: "클라라와 태양", Authors: []string{"가즈오 이시구로"}, Publisher: "민음사", Tags: []string{"AI", "소설"}}

	day := 24 * time.Hour
	at := func(ago time.Duration) time.Time { return now.Add(-ago).Truncate(time.Minute) }
	post := func(no int, ago time.Duration, findings ...book.Finding) demoPost {
		return demoPost{post: book.Post{No: no, PostedAt: at(ago), ContentHash: "demo"}, findings: findings}
	}
	inPost := func(ago time.Duration, summary string, links ...book.Link) book.Mention {
		return book.Mention{Kind: book.InPost, At: at(ago), Summary: summary, Links: links}
	}
	inComment := func(ago time.Duration, summary string) book.Mention {
		return book.Mention{Kind: book.InComment, At: at(ago), Summary: summary}
	}

	return []demoPost{
		post(1, 20*day,
			book.Finding{Book: story, Mentions: []book.Mention{
				inPost(20*day, "단편집의 표제작을 두고 이야기를 나눴다. 외계 언어를 배우면서 시간을 다르게 느끼게 되는 주인공의 시선이 "+
					"인상 깊었다는 의견이 많았고, 영화와 비교하는 이야기도 나왔다."),
			}},
			book.Finding{Book: breath, Mentions: []book.Mention{
				inPost(20*day, "다음 모임에서 같은 작가의 두 번째 단편집도 읽어 보자는 제안이 있었다."),
				inComment(20*day-5*time.Hour, "표제작의 공기 압력 이야기가 엔트로피를 설명하는 가장 아름다운 방법이라고 추천했다."),
			}},
		),
		post(2, 9*day,
			book.Finding{Book: cosmos, Mentions: []book.Mention{
				inPost(9*day, "천문학 입문서로 다시 읽었다. 오래된 책인데도 우주의 크기를 실감하게 하는 문장들이 여전히 힘이 있다고 했다.",
					book.Link{URL: "https://example.com/cosmos-talk", Title: "코스모스 다큐멘터리 소개"}),
				inComment(9*day-2*time.Hour, "어린 시절 다큐멘터리로 먼저 접했다며 책으로 읽으니 느낌이 다르다고 했다."),
			}},
			book.Finding{Book: gene, Mentions: []book.Mention{
				inComment(9*day-6*time.Hour, "과학 교양서를 하나 더 읽는다면 이 책을 권한다며, 유전자의 관점에서 생명을 보는 방식이 신선했다고 적었다."),
			}},
		),
		post(3, 3*day,
			book.Finding{Book: sapiens, Mentions: []book.Mention{
				inPost(3*day, "인류가 허구를 함께 믿는 능력으로 큰 무리를 이뤘다는 주장을 두고 찬반이 갈렸다. 농업 혁명을 '사기'라고 부른 대목에서 토론이 길어졌다."),
				inComment(3*day-time.Hour, "역사책이라기보다 질문을 던지는 책이라서 좋았다고 했다."),
				inComment(3*day-3*time.Hour, "후속작까지 이어서 읽어 보겠다고 했다."),
			}},
		),
		post(4, day,
			book.Finding{Book: klara, Mentions: []book.Mention{
				inPost(day, "인공지능 친구의 눈으로 본 가족 이야기다. AI가 사람을 이해하려는 방식이 오히려 사람다움을 묻게 만든다는 감상을 나눴다.",
					book.Link{URL: "https://example.com/klara-review", Title: "서평 모음"}),
			}},
		),
		post(5, 3*time.Hour,
			book.Finding{Book: breath, Mentions: []book.Mention{
				inPost(3*time.Hour, "이번 달 AI 주제와 이어서 단편 '소프트웨어 객체의 생애 주기'를 다시 읽자는 이야기가 나왔다."),
				inComment(time.Hour, "디지털 생명체를 기르는 이야기가 지금 읽으니 더 현실적으로 느껴진다고 했다."),
			}},
		),
	}
}
