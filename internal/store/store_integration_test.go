//go:build integration

package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yonsei-alumni/abc-library/internal/book"
	"github.com/yonsei-alumni/abc-library/internal/config"
)

// openTestStore는 실제 DB에 임시 스키마를 만들어 연결한다. 테스트가 끝나면 스키마를 지운다.
// 실행: make test-integration
func openTestStore(t *testing.T) *Store {
	t.Helper()
	dsn, err := config.DatabaseURL()
	if err != nil {
		t.Skip(err)
	}
	schema := fmt.Sprintf("it_%d", time.Now().UnixNano())
	admin, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := admin.db.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.db.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close()
	})

	// Neon은 연결 문자열의 search_path 파라미터를 무시하므로, 연결을 하나로 묶고 SET으로 지정한다.
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.db.SetMaxOpenConns(1)
	s.db.SetConnMaxIdleTime(0)
	if _, err := s.db.ExecContext(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	var current string
	if err := s.db.QueryRowContext(ctx, "SELECT current_schema()").Scan(&current); err != nil || current != schema {
		t.Fatalf("임시 스키마로 바뀌지 않았다: %q, %v", current, err)
	}
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func day(d int) time.Time { return time.Date(2026, 10, d, 9, 0, 0, 0, time.UTC) }

func mention(kind book.MentionKind, d int, summary string, links ...book.Link) book.Mention {
	return book.Mention{Kind: kind, At: day(d), Summary: summary, Links: links}
}

func TestSavePostAndQueries(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	story := book.Book{ISBN13: "9791196914806", Title: "당신 인생의 이야기", Authors: []string{"테드 창"}, Publisher: "엘리", KyoboPID: "S000001981838", Tags: []string{"SF"}}
	if err := s.SavePost(ctx, book.Post{No: 1, PostedAt: day(1), ContentHash: "h1"}, []book.Finding{{
		Book: story,
		Mentions: []book.Mention{
			mention(book.InPost, 1, "본문 요약", book.Link{URL: "https://example.com/a", Title: "기사"}),
			mention(book.InComment, 2, "댓글 요약"),
		},
	}}); err != nil {
		t.Fatal(err)
	}

	// 다른 판본 ISBN이지만 같은 작품이면 같은 책으로 묶이고 태그가 더해진다.
	otherEdition := story
	otherEdition.ISBN13 = "9788956057842"
	otherEdition.Tags = []string{"소설", "AI"}
	if err := s.SavePost(ctx, book.Post{No: 2, PostedAt: day(3), ContentHash: "h2"}, []book.Finding{
		{Book: otherEdition, Mentions: []book.Mention{mention(book.InComment, 4, "두 번째 글의 댓글")}},
	}); err != nil {
		t.Fatal(err)
	}

	shelf, err := s.Books(ctx, Filter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(shelf) != 1 || shelf[0].PostCount != 2 || shelf[0].Book.ISBN13 != "9791196914806" || !shelf[0].LastPostedAt.Equal(day(4)) {
		t.Fatalf("같은 작품은 책 한 권이어야 한다: %+v", shelf)
	}
	if got := strings.Join(shelf[0].Book.Tags, ","); got != "AI,SF,소설" {
		t.Errorf("태그가 합쳐지고 모임 주제가 앞에 와야 한다: %s", got)
	}

	b, mentions, err := s.Book(ctx, shelf[0].Book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "당신 인생의 이야기" || len(mentions) != 3 {
		t.Fatalf("상세 = %+v %+v", b, mentions)
	}
	if mentions[0].Kind != book.InPost || mentions[1].Kind != book.InComment || mentions[2].PostNo != 2 {
		t.Errorf("언급은 시각 순이어야 한다: %+v", mentions)
	}
	if len(mentions[0].Links) != 1 || mentions[0].Links[0].Title != "기사" {
		t.Errorf("링크 = %+v", mentions[0].Links)
	}

	entries, err := s.Timeline(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 1번 글의 본문과 댓글은 소식 하나로 묶인다.
	if len(entries) != 2 || entries[0].PostNo != 2 || entries[0].IsNew || !entries[1].IsNew || !entries[1].PostedAt.Equal(day(1)) {
		t.Errorf("타임라인 = %+v", entries)
	}
	// 소식의 요약은 본문 언급이 있으면 그것을, 댓글만 있으면 첫 댓글 언급을 쓴다.
	if len(entries) == 2 && (entries[0].Summary != "두 번째 글의 댓글" || entries[1].Summary != "본문 요약") {
		t.Errorf("소식 요약 = %q, %q", entries[0].Summary, entries[1].Summary)
	}
	// 소식마다 그 글에서 이 책을 다룬 댓글 수와 이 책의 모든 언급 수를 센다.
	if len(entries) == 2 && (entries[0].Comments != 1 || entries[1].Comments != 1 || entries[0].Mentions != 3 || entries[1].Mentions != 3) {
		t.Errorf("소식의 댓글 수와 언급 수 = %+v", entries)
	}

	if no, err := s.LastPostNo(ctx); err != nil || no != 2 {
		t.Errorf("LastPostNo = %d, %v", no, err)
	}
	if h, ok, err := s.PostHash(ctx, 1); err != nil || !ok || h != "h1" {
		t.Errorf("PostHash = %q %v %v", h, ok, err)
	}
	if _, ok, _ := s.PostHash(ctx, 99); ok {
		t.Error("없는 글은 ok가 거짓이어야 한다")
	}

	// 수정된 글에서 책이 빠지면 언급이 지워지고, 언급이 없어진 책도 지워진다.
	for _, no := range []int{1, 2} {
		if err := s.SavePost(ctx, book.Post{No: no, PostedAt: day(no), ContentHash: "edited"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if shelf, _ := s.Books(ctx, Filter{}, 10, 0); len(shelf) != 0 {
		t.Errorf("언급이 없는 책은 지워져야 한다: %+v", shelf)
	}
	if _, _, err := s.Book(ctx, b.ID); err != ErrNotFound {
		t.Errorf("지워진 책은 ErrNotFound여야 한다: %v", err)
	}
}

func TestSavePostDoesNotDuplicateMentions(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// 원제와 번역 제목으로 따로 뽑혔지만 ISBN이 같아서 DB에서는 같은 책이다.
	story := book.Book{ISBN13: "9791196914806", Title: "당신 인생의 이야기", Authors: []string{"테드 창"}}
	original := book.Book{ISBN13: "9791196914806", Title: "Stories of Your Life", Authors: []string{"Ted Chiang"}}
	findings := []book.Finding{
		{Book: story, Mentions: []book.Mention{mention(book.InPost, 1, "본문 요약"), mention(book.InComment, 2, "댓글 요약")}},
		{Book: original, Mentions: []book.Mention{mention(book.InPost, 1, "같은 본문의 다른 요약"), mention(book.InComment, 2, "같은 댓글"), mention(book.InComment, 3, "다른 댓글")}},
	}
	// 같은 글을 여러 번 다시 추출해도 언급 수가 그대로여야 한다.
	for range 3 {
		if err := s.SavePost(ctx, book.Post{No: 1, PostedAt: day(1), ContentHash: "h"}, findings); err != nil {
			t.Fatal(err)
		}
	}
	shelf, err := s.Books(ctx, Filter{}, 10, 0)
	if err != nil || len(shelf) != 1 {
		t.Fatalf("책은 한 권이어야 한다: %+v, %v", shelf, err)
	}
	_, mentions, err := s.Book(ctx, shelf[0].Book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(mentions) != 3 || mentions[0].Summary != "본문 요약" || mentions[1].Summary != "댓글 요약" || mentions[2].Summary != "다른 댓글" {
		t.Errorf("본문 언급 하나와 서로 다른 댓글 언급 둘만 남아야 한다: %+v", mentions)
	}
}

func TestSearchCountsAndFilter(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	err := s.SavePost(ctx, book.Post{No: 10, PostedAt: day(1), ContentHash: "x"}, []book.Finding{
		{Book: book.Book{Title: "무수면 사회", Authors: []string{"김찬호"}, Tags: []string{"사회·정치", "인문"}}, Mentions: []book.Mention{mention(book.InPost, 1, "요약")}},
		{Book: book.Book{Title: "투명한 나선", Authors: []string{"히가시노 게이고"}, Tags: []string{"추리·미스터리"}}, Mentions: []book.Mention{mention(book.InComment, 2, "요약")}},
		{Book: book.Book{Title: "AI 2041", Authors: []string{"리카이푸"}, Tags: []string{"AI"}}, Mentions: []book.Mention{mention(book.InPost, 1, "요약")}},
		// 언급이 없는 결과는 저장하지 않는다.
		{Book: book.Book{Title: "언급 없음"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tags, err := s.TagCounts(ctx)
	if err != nil || len(tags) != 4 {
		t.Fatalf("태그 = %+v, %v", tags, err)
	}
	authors, err := s.AuthorCounts(ctx)
	if err != nil || len(authors) != 3 {
		t.Fatalf("저자 = %+v, %v", authors, err)
	}

	titles := func(f Filter) string {
		t.Helper()
		shelf, err := s.Books(ctx, f, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		var ts []string
		for _, sh := range shelf {
			ts = append(ts, sh.Book.Title)
		}
		return strings.Join(ts, ",")
	}
	tests := []struct {
		filter Filter
		want   string
	}{
		{Filter{Tag: "인문"}, "무수면 사회"},
		{Filter{Author: "히가시노 게이고"}, "투명한 나선"},
		{Filter{Query: "무수면사회"}, "무수면 사회"}, // 공백을 무시한다
		{Filter{Query: "히가시노"}, "투명한 나선"},  // 저자
		{Filter{Query: "미스터리"}, "투명한 나선"},  // 태그
		{Filter{Query: "ai"}, "AI 2041"},   // 대소문자를 무시한다
		{Filter{Query: "100%"}, ""},        // LIKE 특수 문자는 글자 그대로 찾는다
		{Filter{Query: "없는 책"}, ""},
		{Filter{Query: "  "}, "투명한 나선,AI 2041,무수면 사회"}, // 공백뿐이면 조건이 없다
	}
	for _, tt := range tests {
		if got := titles(tt.filter); !sameSet(got, tt.want) {
			t.Errorf("Books(%+v) = %q, want %q", tt.filter, got, tt.want)
		}
	}
}

func sameSet(a, b string) bool {
	as, bs := strings.Split(a, ","), strings.Split(b, ",")
	if len(as) != len(bs) {
		return false
	}
	seen := map[string]int{}
	for _, s := range as {
		seen[s]++
	}
	for _, s := range bs {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestRunsAndPending(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	id, err := s.StartRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(ctx, Run{ID: id, LastPostNo: 33, LatestPostNo: 187, Checked: 10, Extracted: 8, Failed: 1, Error: "게시글 1개를 처리하지 못했습니다"}); err != nil {
		t.Fatal(err)
	}
	runs, err := s.RecentRuns(ctx, 5)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	if r := runs[0]; r.LastPostNo != 33 || r.LatestPostNo != 187 || r.Failed != 1 || r.FinishedAt.IsZero() || r.StartedAt.IsZero() || r.Error == "" {
		t.Errorf("실행 기록 = %+v", r)
	}

	for range 2 {
		if err := s.MarkPending(ctx, 50, PendingFailed, "API 오류"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MarkPending(ctx, 60, PendingMissing, ""); err != nil {
		t.Fatal(err)
	}
	ps, err := s.PendingPosts(ctx)
	if err != nil || len(ps) != 2 || ps[0].Attempts != 2 || ps[0].LastError != "API 오류" || ps[1].Reason != PendingMissing {
		t.Fatalf("pending = %+v, %v", ps, err)
	}
	if nos, _ := s.PendingPostNos(ctx, 2); len(nos) != 1 || nos[0] != 60 {
		t.Errorf("시도 횟수가 상한에 이른 글은 빼야 한다: %v", nos)
	}

	// 처리에 성공해서 저장하면 다시 시도할 글에서 빠진다.
	if err := s.SavePost(ctx, book.Post{No: 50, PostedAt: day(1), ContentHash: "h"}, nil); err != nil {
		t.Fatal(err)
	}
	if nos, _ := s.PendingPostNos(ctx, 10); len(nos) != 1 || nos[0] != 60 {
		t.Errorf("저장한 글은 지워야 한다: %v", nos)
	}
}
