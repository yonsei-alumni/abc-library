package web

import (
	"context"
	"errors"
	"fmt"
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
	posted = time.Date(2026, 10, 21, 1, 24, 0, 0, book.Seoul)
)

// defaultTags는 fakeStore의 태그 목록이다.
var defaultTags = book.TagList{{Name: "AI", Kind: book.TopicTag}, {Name: "SF", Kind: book.TopicTag}, {Name: "소설", Kind: book.KindTag}}

type fakeStore struct {
	filter  store.Filter
	offsets []int
	entries int
	fail    bool

	// 관리 화면이 부른 수정 메서드와 인자를 차례로 남긴다.
	calls []string
	// 수정 메서드가 돌려줄 오류다.
	err     error
	updated book.Book
	links   []book.Link

	// 사용자(HMAC 값 → 번호)와 받은 HMAC 값, 카테고리, 북마크([사용자, 책] → 카테고리 번호)다.
	users    map[string]int64
	hashes   []string
	cats     []book.Category
	marks    map[[2]int64][]int64
	savedCat book.Category
}

func (f *fakeStore) record(format string, args ...any) error {
	f.calls = append(f.calls, fmt.Sprintf(format, args...))
	return f.err
}

func (f *fakeStore) Timeline(_ context.Context, limit, offset int) ([]book.Entry, error) {
	f.offsets = append(f.offsets, offset)
	var es []book.Entry
	for i := offset; i < f.entries && len(es) < limit; i++ {
		es = append(es, book.Entry{Book: story, PostNo: 184, PostedAt: posted, IsNew: i == 0, Summary: "언어를 배우며\n시간을 다르게 본다.",
			Comments: i % 2, Mentions: 7})
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
			{ID: 5, BookID: 1, PostNo: 184, Kind: book.InPost, At: posted, Summary: "본문 첫 줄\n본문 둘째 줄",
				Links: []book.Link{{URL: "https://news.example.com/a", Title: "관련 기사"}}},
			{PostNo: 184, Kind: book.InComment, At: posted.Add(time.Hour), Summary: "같은 글의 댓글 요약"},
			{PostNo: 190, Kind: book.InComment, At: posted.Add(48 * time.Hour), Summary: "다른 글의 댓글 요약",
				Links: []book.Link{{URL: "https://www.youtube.com/watch?v=x", Title: "강연 영상"}}},
		}, nil
	case 2:
		return noIsbn, []book.Mention{{ID: 7, BookID: 2, PostNo: 30, Kind: book.InPost, At: posted, Summary: "무수면 요약"}}, nil
	case 3:
		// 관리자가 언급을 모두 지운 책
		return book.Book{ID: 3, Title: "책이 아닌 것"}, nil, nil
	}
	return book.Book{}, nil, store.ErrNotFound
}

func (f *fakeStore) TagCounts(context.Context) ([]book.Count, error) {
	return []book.Count{{Name: "소설", Books: 5}, {Name: "SF", Books: 3}, {Name: "AI", Books: 2}}, nil
}

func (f *fakeStore) AuthorCounts(context.Context) ([]book.Count, error) {
	return []book.Count{{Name: "테드 창", Books: 2}}, nil
}

func (f *fakeStore) Tags(context.Context) (book.TagList, error) { return defaultTags, nil }

func (f *fakeStore) AddTag(_ context.Context, t book.Tag) error {
	return f.record("AddTag %s %s", t.Name, t.Kind)
}

func (f *fakeStore) RenameTag(_ context.Context, old, name string) error {
	return f.record("RenameTag %s %s", old, name)
}

func (f *fakeStore) MoveTag(_ context.Context, name string, up bool) error {
	return f.record("MoveTag %s %v", name, up)
}

func (f *fakeStore) DeleteTag(_ context.Context, name string) error {
	return f.record("DeleteTag %s", name)
}

func (f *fakeStore) UpdateBook(_ context.Context, b book.Book) error {
	f.updated = b
	return f.record("UpdateBook %d", b.ID)
}

func (f *fakeStore) MergeBook(_ context.Context, from, into int64) error {
	return f.record("MergeBook %d %d", from, into)
}

func (f *fakeStore) DeleteBook(_ context.Context, id int64) error {
	return f.record("DeleteBook %d", id)
}

func (f *fakeStore) Mention(_ context.Context, id int64) (book.Mention, error) {
	if id != 5 {
		return book.Mention{}, store.ErrNotFound
	}
	_, ms, _ := f.Book(context.Background(), 1)
	return ms[0], nil
}

func (f *fakeStore) UpdateMention(_ context.Context, id int64, summary string, links []book.Link) error {
	f.links = links
	return f.record("UpdateMention %d %s", id, summary)
}

func (f *fakeStore) MoveMention(_ context.Context, id, toBook int64) error {
	return f.record("MoveMention %d %d", id, toBook)
}

func (f *fakeStore) DeleteMention(_ context.Context, id int64) error {
	return f.record("DeleteMention %d", id)
}

const adminAddr = "admin@example.com"

func (f *fakeStore) Admins(context.Context) ([]store.Admin, error) {
	return []store.Admin{{Email: adminAddr, CreatedAt: posted}, {Email: "other@example.com", AddedBy: adminAddr, CreatedAt: posted}}, nil
}

func (f *fakeStore) IsAdmin(_ context.Context, email string) (bool, error) {
	return email == adminAddr || email == "other@example.com", nil
}

func (f *fakeStore) AddAdmin(_ context.Context, email, addedBy string) error {
	return f.record("AddAdmin %s %s", email, addedBy)
}

func (f *fakeStore) DeleteAdmin(_ context.Context, email string) error {
	return f.record("DeleteAdmin %s", email)
}

func newServer(t *testing.T, st Store) *Server {
	t.Helper()
	s, err := New(st, Config{BandNo: "102765569"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func get(t *testing.T, st Store, path string, headers ...string) (int, string) {
	t.Helper()
	s := newServer(t, st)
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

// assertInMenu는 wants가 '메뉴' 패널 안에 있는지 확인한다. 바닥글에 두면 무한 스크롤 화면에서 닿지 않는다.
func assertInMenu(t *testing.T, body string, wants ...string) {
	t.Helper()
	_, menu, ok := strings.Cut(body, `id="site-menu" popover`)
	menu, _, _ = strings.Cut(menu, "<main")
	if !ok || !strings.Contains(body, `popovertarget="site-menu"`) {
		t.Fatal("'메뉴' 패널과 여는 버튼이 없다")
	}
	for _, w := range wants {
		if !strings.Contains(menu, w) {
			t.Errorf("'메뉴' 패널에 %q가 없다", w)
		}
	}
}

func TestTimelinePagesWithHTMX(t *testing.T) {
	st := &fakeStore{entries: pageSize + 3}
	code, body := get(t, st, "/")
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	assertContains(t, body, "<title>최근 소식 · ABC 도서관</title>", "새 책", "이야기 추가", "/books/1",
		"https://contents.kyobobook.co.kr/sih/fit-in/240x0/pdt/9791196914806.jpg", `hx-get="/?page=2"`, `hx-trigger="revealed, click"`,
		`<time class="post-time" datetime="2026-10-21T01:24:00+09:00" data-relative>2026년 10월 21일</time>`,
		`<span class="post-text">언어를 배우며 시간을 다르게 본다.</span>`, "#SF", "댓글 1개", "이 책의 이야기 7개")
	// 그 글에 이 책을 다룬 댓글이 없으면 댓글 수를 적지 않는다.
	if strings.Contains(body, "댓글 0개") {
		t.Error("댓글이 없는 소식에 댓글 수가 있다")
	}
	// 새 책은 책 상세의 처음으로, 이야기 추가는 그 글의 언급 묶음으로 연결한다.
	if strings.Count(body, `href="/books/1"`) != 1 || strings.Count(body, `href="/books/1#post-184"`) != pageSize-1 {
		t.Error("이야기 추가 소식은 책 상세의 그 글 묶음으로 연결해야 한다")
	}

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
	threads := strings.Split(body, `<li class="thread" `)
	if len(threads) != 3 {
		t.Fatalf("게시글 묶음 수 = %d", len(threads)-1)
	}
	first, second := threads[1], threads[2]
	// 소식에서 '이야기 추가'를 누르면 그 글의 묶음으로 바로 간다.
	if !strings.HasPrefix(first, `id="post-184"`) || !strings.HasPrefix(second, `id="post-190"`) {
		t.Error("게시글 묶음에 글 번호로 만든 id가 있어야 한다")
	}
	if !strings.Contains(first, "본문 첫 줄") || !strings.Contains(first, `class="replies"`) || !strings.Contains(first, "같은 글의 댓글 요약") {
		t.Error("첫 묶음은 본문과 그 아래 댓글이어야 한다")
	}
	if strings.Contains(second, ">게시글<") || !strings.Contains(second, "다른 글의 댓글 요약") {
		t.Error("둘째 묶음은 댓글만 있어야 한다")
	}

	// 교보문고 상품을 확정하지 못한 책은 교보문고 검색으로 연결한다.
	_, body = get(t, &fakeStore{}, "/books/2")
	assertContains(t, body, "https://search.kyobobook.co.kr/search?keyword=%EB%AC%B4%EC%88%98%EB%A9%B4+%EC%82%AC%ED%9A%8C+%EA%B9%80%EC%B0%AC%ED%98%B8", "cover-blank", "이야기 1개")

	// 관리자가 언급을 모두 지운 책은 방문자에게 보이지 않는다.
	if code, _ := get(t, &fakeStore{}, "/books/3"); code != http.StatusNotFound {
		t.Errorf("언급이 없는 책 code = %d", code)
	}
	if strings.Contains(body, "/admin/") || strings.Contains(body, "관리자 로그인") {
		t.Error("로그인을 설정하지 않았거나 관리자가 아니면 관리 링크가 없어야 한다")
	}
}

func TestTagsPutsTopicsFirst(t *testing.T) {
	_, body := get(t, &fakeStore{}, "/tags")
	topics, others, ok := strings.Cut(body, "그 밖의 태그")
	if !ok {
		t.Fatal("모임 주제와 그 밖의 태그를 나눠야 한다")
	}
	// 모임 주제는 태그 목록의 순서(AI, SF)로, 나머지는 따로 둔다.
	if i, j := strings.Index(topics, "#AI"), strings.Index(topics, "#SF"); i < 0 || j < 0 || i > j || !strings.Contains(topics, "모임 주제") {
		t.Errorf("모임 주제 순서가 잘못됐다")
	}
	if !strings.Contains(others, "#소설") || strings.Contains(others, "#SF") {
		t.Error("그 밖의 태그에는 모임 주제가 없어야 한다")
	}
}

func TestNotFoundAndErrors(t *testing.T) {
	for _, path := range []string{"/books/99", "/books/abc", "/books/0", "/books/-1", "/nothing"} {
		if code, _ := get(t, &fakeStore{}, path); code != http.StatusNotFound {
			t.Errorf("%s: code = %d", path, code)
		}
	}
	if code, body := get(t, &fakeStore{fail: true}, "/books"); code != http.StatusInternalServerError || strings.Contains(body, "DB 오류") {
		t.Errorf("내부 오류 내용을 화면에 노출하지 않아야 한다: %d", code)
	}
}

func TestExcerpt(t *testing.T) {
	long := strings.Repeat("가", excerptLen+5)
	for in, want := range map[string]string{
		"":                              "",
		" 첫 줄\n\n 둘째   줄 ":              "첫 줄 둘째 줄",
		strings.Repeat("가", excerptLen): strings.Repeat("가", excerptLen),
		long:                            strings.Repeat("가", excerptLen) + "…",
		strings.Repeat("가", excerptLen-1) + " 나다": strings.Repeat("가", excerptLen-1) + "…",
	} {
		if got := excerpt(in); got != want {
			t.Errorf("excerpt(%q) = %q, want %q", in, got, want)
		}
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
	s := newServer(t, &fakeStore{})
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

func TestPrivacyPage(t *testing.T) {
	code, body := get(t, &fakeStore{}, "/privacy")
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	assertContains(t, body, "<title>개인정보 처리방침 · ABC 도서관</title>", "HMAC-SHA256", "처리 위탁과 국외 이전",
		"Anthropic, PBC", "싱가포르", "개인정보 보호책임자", "시행일")
	// 모든 화면 아래쪽에서 처리방침으로 갈 수 있다.
	_, home := get(t, &fakeStore{}, "/")
	assertContains(t, home, `<a href="/privacy">개인정보 처리방침</a>`)
}

func TestHelpPage(t *testing.T) {
	code, body := get(t, &fakeStore{}, "/help")
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	// 화면만 봐서는 알기 어려운 규칙과 설치 방법만 남긴다.
	assertContains(t, body, "<title>도움말 · ABC 도서관</title>", `<a href="/help" aria-current="page">도움말</a>`,
		"띄어쓰기와 대소문자를 가리지 않습니다", "밴드 멤버만", "카테고리 없음", "다음 날 아침",
		"설치 및 바로가기 만들기", "홈 화면에 추가", "웹 앱으로 열기", "/privacy")
	// 캡처는 로그인하지 않은 사람이 볼 수 없는 내 북마크 화면만 밝은 화면과 어두운 화면 한 쌍으로 둔다.
	shots := regexp.MustCompile(`/static/help/[a-z-]+\.webp\?v=[0-9a-f]{8}`).FindAllString(body, -1)
	if len(shots) != 2 || !strings.Contains(shots[0], "/bookmark-dark.webp") || !strings.Contains(shots[1], "/bookmark.webp") {
		t.Errorf("도움말 캡처 = %v", shots)
	}
	s := newServer(t, &fakeStore{})
	for _, shot := range shots {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, shot, nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/webp" {
			t.Errorf("%s: code = %d, type = %q", shot, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	// 모든 화면의 메뉴와 아래쪽에서 도움말로 갈 수 있다.
	_, home := get(t, &fakeStore{}, "/")
	assertContains(t, home, `<a href="/help" >도움말</a>`, `<a href="/help">도움말</a>`)
}

func TestPrivacyPageLeavesOutAdminEmail(t *testing.T) {
	// 관리자는 운영자 본인뿐이라서 처리방침에 관리자 메일 주소를 적지 않는다(AGENTS.md).
	_, body := get(t, &fakeStore{}, "/privacy")
	if strings.Contains(body, "관리자 메일") || strings.Contains(body, "관리자의 메일") {
		t.Error("처리방침에 관리자 메일 주소 항목이 있다")
	}
}
