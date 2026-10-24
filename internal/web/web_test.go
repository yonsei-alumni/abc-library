package web

import (
	"context"
	"errors"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/yonsei-alumni/abc-library/internal/book"
	"github.com/yonsei-alumni/abc-library/internal/store"
)

var (
	story = book.Book{ID: 1, ISBN13: "9791196914806", Title: "당신 인생의 이야기", Authors: []string{"테드 창"},
		Publisher: "엘리", KyoboPID: "S000001981838", Tags: []string{"SF", "소설"}}
	noIsbn = book.Book{ID: 2, Title: "무수면 사회", Authors: []string{"김찬호"}}
	posted = time.Date(2026, 10, 21, 1, 24, 0, 0, seoul)
)

type fakeStore struct {
	filter  store.Filter
	offsets []int
	entries int
	fail    bool
}

func (f *fakeStore) Timeline(_ context.Context, limit, offset int) ([]book.Entry, error) {
	f.offsets = append(f.offsets, offset)
	var es []book.Entry
	for i := offset; i < f.entries && len(es) < limit; i++ {
		es = append(es, book.Entry{Book: story, PostNo: 184, PostedAt: posted, IsNew: i == 0})
	}
	return es, nil
}

func (f *fakeStore) Books(_ context.Context, filter store.Filter, _, _ int) ([]book.Shelved, error) {
	f.filter = filter
	if f.fail {
		return nil, errors.New("DB 오류")
	}
	if filter.Query == "없는 책" {
		return nil, nil
	}
	return []book.Shelved{{Book: story, LastPostedAt: posted, PostCount: 2}, {Book: noIsbn, LastPostedAt: posted, PostCount: 1}}, nil
}

func (f *fakeStore) Book(_ context.Context, id int64) (book.Book, []book.Mention, error) {
	switch id {
	case 1:
		return story, []book.Mention{
			{PostNo: 184, Kind: book.InPost, At: posted, Summary: "본문 첫 줄\n본문 둘째 줄",
				Links: []book.Link{{URL: "https://news.example.com/a", Title: "관련 기사"}}},
			{PostNo: 184, Kind: book.InComment, At: posted.Add(time.Hour), Summary: "같은 글의 댓글 요약"},
			{PostNo: 190, Kind: book.InComment, At: posted.Add(48 * time.Hour), Summary: "다른 글의 댓글 요약",
				Links: []book.Link{{URL: "https://www.youtube.com/watch?v=x", Title: "강연 영상"}}},
		}, nil
	case 2:
		return noIsbn, nil, nil
	}
	return book.Book{}, nil, store.ErrNotFound
}

func (f *fakeStore) TagCounts(context.Context) ([]book.Count, error) {
	return []book.Count{{Name: "소설", Books: 5}, {Name: "SF", Books: 3}, {Name: "AI", Books: 2}}, nil
}

func (f *fakeStore) AuthorCounts(context.Context) ([]book.Count, error) {
	return []book.Count{{Name: "테드 창", Books: 2}}, nil
}

func get(t *testing.T, st Store, path string, headers ...string) (int, string) {
	t.Helper()
	s, err := New(st, "102765569", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Body)
	if strings.Contains(string(body), "no value") {
		t.Errorf("%s: 템플릿에 없는 값이 찍혔다", path)
	}
	return rec.Code, string(body)
}

func assertContains(t *testing.T, body string, wants ...string) {
	t.Helper()
	body = html.UnescapeString(body)
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("응답에 %q가 없다", w)
		}
	}
}

func TestTimelinePagesWithHTMX(t *testing.T) {
	st := &fakeStore{entries: pageSize + 3}
	code, body := get(t, st, "/")
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	assertContains(t, body, "<title>최근 소식 · ABC 서재</title>", "새 책", "이야기 추가", "/books/1",
		"https://contents.kyobobook.co.kr/sih/fit-in/240x0/pdt/9791196914806.jpg", `hx-get="/?page=2"`, "2026년 10월 21일")

	code, body = get(t, st, "/?page=2", "HX-Request", "true", "HX-Target", "timeline-more")
	if code != http.StatusOK || strings.Contains(body, "<html") {
		t.Errorf("더 보기 요청에는 목록 조각만 그려야 한다: %d", code)
	}
	if strings.Contains(body, "hx-get") {
		t.Error("마지막 페이지에는 더 보기 버튼이 없어야 한다")
	}
	if st.offsets[1] != pageSize {
		t.Errorf("두 번째 페이지 offset = %d", st.offsets[1])
	}

	// hx-boost로 이동하거나 검색창이 보낸 요청은 전체 화면을 그린다(htmx가 필요한 부분을 고른다).
	_, body = get(t, st, "/", "HX-Request", "true", "HX-Boosted", "true")
	assertContains(t, body, "<html")
}

func TestBooksFilterAndSearch(t *testing.T) {
	st := &fakeStore{}
	code, body := get(t, st, "/books?tag=SF")
	if code != http.StatusOK || st.filter.Tag != "SF" {
		t.Fatalf("code = %d, filter = %+v", code, st.filter)
	}
	assertContains(t, body, `<h1 class="page-title">#SF</h1>`, "pdt/9791196914806.jpg", "cover-blank", "게시글 2개", "모든 책 보기")

	_, body = get(t, st, "/books?author=%ED%85%8C%EB%93%9C+%EC%B0%BD")
	assertContains(t, body, "테드 창의 책")

	_, body = get(t, st, "/books?q=+%ED%85%8C%EB%93%9C+", "HX-Request", "true", "HX-Target", "content")
	if st.filter.Query != "테드" {
		t.Errorf("검색어는 앞뒤 공백을 빼야 한다: %q", st.filter.Query)
	}
	assertContains(t, body, "‘테드’ 검색 결과", `id="content"`, `value="테드"`)

	_, body = get(t, st, "/books?q=%EC%97%86%EB%8A%94+%EC%B1%85")
	assertContains(t, body, "검색 결과가 없습니다.")
}

func TestBookDetailShowsThreads(t *testing.T) {
	code, body := get(t, &fakeStore{}, "/books/1")
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	assertContains(t, body,
		"https://product.kyobobook.co.kr/detail/S000001981838", "교보문고에서 보기", "pdt/9791196914806.jpg",
		"이야기 3개", "본문 첫 줄\n본문 둘째 줄", "관련 기사", "강연 영상",
		"https://band.us/band/102765569/post/184", "https://band.us/band/102765569/post/190",
		"/books?tag=SF", "#SF", "/books?author=%ed%85%8c%eb%93%9c%20%ec%b0%bd")

	// 184번 글은 본문 아래에 그 글의 댓글이 붙고, 190번 글은 댓글만 있다.
	threads := strings.Split(body, `<li class="thread">`)
	if len(threads) != 3 {
		t.Fatalf("게시글 묶음 수 = %d", len(threads)-1)
	}
	first, second := threads[1], threads[2]
	if !strings.Contains(first, "본문 첫 줄") || !strings.Contains(first, `class="replies"`) || !strings.Contains(first, "같은 글의 댓글 요약") {
		t.Error("첫 묶음은 본문과 그 아래 댓글이어야 한다")
	}
	if strings.Contains(second, ">게시글<") || !strings.Contains(second, "다른 글의 댓글 요약") {
		t.Error("둘째 묶음은 댓글만 있어야 한다")
	}

	// 교보문고 상품을 확정하지 못한 책은 교보문고 검색으로 연결한다.
	_, body = get(t, &fakeStore{}, "/books/2")
	assertContains(t, body, "https://search.kyobobook.co.kr/search?keyword=%EB%AC%B4%EC%88%98%EB%A9%B4+%EC%82%AC%ED%9A%8C+%EA%B9%80%EC%B0%AC%ED%98%B8", "cover-blank", "이야기 0개")
}

func TestTagsPutsTopicsFirst(t *testing.T) {
	_, body := get(t, &fakeStore{}, "/tags")
	topics, others, ok := strings.Cut(body, "그 밖의 태그")
	if !ok {
		t.Fatal("모임 주제와 그 밖의 태그를 나눠야 한다")
	}
	// 모임 주제는 TopicTags 순서(AI, SF)로, 나머지는 따로 둔다.
	if i, j := strings.Index(topics, "#AI"), strings.Index(topics, "#SF"); i < 0 || j < 0 || i > j || !strings.Contains(topics, "모임 주제") {
		t.Errorf("모임 주제 순서가 잘못됐다")
	}
	if !strings.Contains(others, "#소설") || strings.Contains(others, "#SF") {
		t.Error("그 밖의 태그에는 모임 주제가 없어야 한다")
	}
}

func TestNotFoundAndErrors(t *testing.T) {
	for _, path := range []string{"/books/99", "/books/abc", "/nothing"} {
		if code, _ := get(t, &fakeStore{}, path); code != http.StatusNotFound {
			t.Errorf("%s: code = %d", path, code)
		}
	}
	if code, body := get(t, &fakeStore{fail: true}, "/books"); code != http.StatusInternalServerError || strings.Contains(body, "DB 오류") {
		t.Errorf("내부 오류 내용을 화면에 노출하지 않아야 한다: %d", code)
	}
}

func TestStaticURLsAreVersioned(t *testing.T) {
	_, body := get(t, &fakeStore{}, "/")
	if !regexp.MustCompile(`href="/static/style\.css\?v=[0-9a-f]{8}"`).MatchString(body) ||
		!regexp.MustCompile(`src="/static/htmx\.min\.js\?v=[0-9a-f]{8}"`).MatchString(body) {
		t.Error("정적 파일 주소에 내용 해시가 붙어야 브라우저 캐시를 피한다")
	}
}

func TestAuthorsAndStatic(t *testing.T) {
	_, body := get(t, &fakeStore{}, "/authors")
	assertContains(t, body, "/books?author=%ed%85%8c%eb%93%9c%20%ec%b0%bd", ">2<")
	if code, body := get(t, &fakeStore{}, "/static/htmx.min.js"); code != http.StatusOK || !strings.Contains(body, "htmx") {
		t.Errorf("정적 파일 code = %d", code)
	}
}

func TestBlocksSearchEngines(t *testing.T) {
	s, err := New(&fakeStore{}, "102765569", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/books/1", "/nothing", "/static/style.css"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, nofollow" {
			t.Errorf("%s: X-Robots-Tag = %q", path, got)
		}
	}
	if code, body := get(t, &fakeStore{}, "/robots.txt"); code != http.StatusOK || !strings.Contains(body, "Disallow: /\n") {
		t.Errorf("robots.txt가 모든 경로를 막아야 한다: %d %q", code, body)
	}
}

func TestHealth(t *testing.T) {
	for _, path := range []string{"/health", "/healthz"} {
		if code, body := get(t, &fakeStore{fail: true}, path); code != http.StatusOK || body != "ok" {
			t.Errorf("%s: DB 상태와 상관없이 ok여야 한다: %d %q", path, code, body)
		}
	}
}
