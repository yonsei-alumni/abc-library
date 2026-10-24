package web

import (
	"context"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yonsei-alumni/abc-library/internal/auth"
	"github.com/yonsei-alumni/abc-library/internal/book"
	"github.com/yonsei-alumni/abc-library/internal/store"
)

// fakeStore의 사용자, 카테고리, 북마크. 필드는 web_test.go의 fakeStore에 있다.

func (f *fakeStore) UserID(_ context.Context, subjectHash string) (int64, error) {
	if f.users == nil {
		f.users = map[string]int64{}
	}
	if id, ok := f.users[subjectHash]; ok {
		return id, nil
	}
	f.hashes = append(f.hashes, subjectHash)
	id := int64(7 + len(f.users))
	f.users[subjectHash] = id
	return id, nil
}

func (f *fakeStore) UserExists(_ context.Context, id int64) (bool, error) {
	for _, u := range f.users {
		if u == id {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) DeleteUser(_ context.Context, id int64) error {
	for h, u := range f.users {
		if u == id {
			delete(f.users, h)
		}
	}
	return f.record("DeleteUser %d", id)
}

func (f *fakeStore) Categories(_ context.Context, userID int64) ([]book.Category, error) {
	var cs []book.Category
	for _, c := range f.cats {
		if c.UserID == userID {
			cs = append(cs, c)
		}
	}
	return cs, nil
}

func (f *fakeStore) Category(_ context.Context, userID, id int64) (book.Category, error) {
	for _, c := range f.cats {
		if c.ID == id && c.UserID == userID {
			return c, nil
		}
	}
	return book.Category{}, store.ErrNotFound
}

func (f *fakeStore) CategoryByCode(_ context.Context, code string) (book.Category, []book.Book, error) {
	for _, c := range f.cats {
		if c.Code == code {
			return c, []book.Book{story, noIsbn}, nil
		}
	}
	return book.Category{}, nil, store.ErrNotFound
}

func (f *fakeStore) PublicCategories(context.Context) ([]book.Category, error) {
	var cs []book.Category
	for _, c := range f.cats {
		if c.Public {
			cs = append(cs, c)
		}
	}
	return cs, nil
}

func (f *fakeStore) CategoryBooks(context.Context, int64) ([]book.Book, error) {
	return []book.Book{story}, nil
}

func (f *fakeStore) AddCategory(_ context.Context, userID int64, c book.Category) (int64, error) {
	if err := f.record("AddCategory %d %s", userID, c.Title); err != nil {
		return 0, err
	}
	c.ID, c.UserID, c.Code = int64(100+len(f.cats)), userID, book.NewCode(book.CodeLen)
	f.cats = append(f.cats, c)
	return c.ID, nil
}

func (f *fakeStore) UpdateCategory(_ context.Context, userID int64, c book.Category) error {
	f.savedCat = c
	return f.record("UpdateCategory %d %d", userID, c.ID)
}

func (f *fakeStore) DeleteCategory(_ context.Context, userID, id int64) error {
	return f.record("DeleteCategory %d %d", userID, id)
}

func (f *fakeStore) Bookmark(_ context.Context, userID, bookID int64) (bool, []int64, error) {
	ids, ok := f.marks[[2]int64{userID, bookID}]
	return ok, ids, nil
}

func (f *fakeStore) SetBookmark(_ context.Context, userID, bookID int64, categoryIDs []int64) error {
	if bookID > 3 {
		return store.ErrNotFound
	}
	return f.record("SetBookmark %d %d %v", userID, bookID, categoryIDs)
}

func (f *fakeStore) DeleteBookmark(_ context.Context, userID, bookID int64) error {
	return f.record("DeleteBookmark %d %d", userID, bookID)
}

func (f *fakeStore) Bookmarks(_ context.Context, userID int64) ([]book.Bookmark, error) {
	var marks []book.Bookmark
	for key, ids := range f.marks {
		if key[0] == userID {
			b, _, _ := f.Book(context.Background(), key[1])
			marks = append(marks, book.Bookmark{Book: b, Categories: ids})
		}
	}
	return marks, nil
}

const memberSub = "109876543210987654321"

// read는 세션 쿠키의 내용이다.
func (a *adminSite) read(c *http.Cookie) auth.Session {
	a.t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(c)
	sess, ok := a.sessions.Read(req)
	if !ok {
		a.t.Fatal("세션 쿠키를 읽지 못했다")
	}
	return sess
}

// member는 사용자를 만들고 그 사용자로 로그인한 세션 쿠키를 돌려준다.
func (a *adminSite) member(subjectHash string) (int64, *http.Cookie) {
	id, _ := a.st.UserID(context.Background(), subjectHash)
	rec := httptest.NewRecorder()
	a.sessions.Set(rec, auth.Session{User: id}, time.Hour)
	return id, rec.Result().Cookies()[0]
}

func TestUserLoginStoresOnlySubjectHash(t *testing.T) {
	a := newAdminSite(t)
	a.login.email = "member@example.com"

	// 로그인하기 전에 무엇을 저장하는지 알린다.
	rec := a.get("/login?next=%2Fbooks%2F1")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	body := rec.Body.String()
	assertContains(t, body, "메일 주소는 받지 않고 DB에도 저장하지 않습니다", "단방향 암호화(HMAC-SHA256)", "Google 계정으로 로그인", "계정을 지우면")
	if strings.Contains(body, `name="admin"`) {
		t.Error("사용자 로그인 폼에 관리자 표시가 있으면 안 된다")
	}

	state, pending := a.startLogin("/books/1", false)
	if !slices.Equal(a.login.emails, []bool{false}) {
		t.Errorf("사용자 로그인은 메일 주소를 달라고 하지 않아야 한다: %v", a.login.emails)
	}
	rec = a.get("/auth/callback?code=c&state="+state, pending)
	expectRedirect(t, rec, "/books/1")
	session := rec.Result().Cookies()[0]

	// DB에는 HMAC 값만 넘기고, 쿠키에도 메일 주소가 없다.
	if len(a.st.hashes) != 1 || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(a.st.hashes[0]) || a.st.hashes[0] != a.subjects.Hash(memberSub) {
		t.Fatalf("저장한 값 = %v", a.st.hashes)
	}
	if sess := a.read(session); sess.Email != "" || sess.User == 0 {
		t.Errorf("사용자 세션 = %+v", sess)
	}

	rec = a.get("/books/1", session)
	body = rec.Body.String()
	assertContains(t, body, `href="/me"`, "내 북마크", "북마크하기", "로그인함", "로그아웃")
	for _, leak := range []string{"member@example.com", memberSub, `href="/admin/tags"`} {
		if strings.Contains(body, leak) {
			t.Errorf("사용자 화면에 %q가 있으면 안 된다", leak)
		}
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("로그인한 사람의 화면은 캐시하지 않아야 한다: %q", got)
	}

	// 같은 계정으로 다시 로그인하면 같은 사용자다.
	state, pending = a.startLogin("/", false)
	expectRedirect(t, a.get("/auth/callback?code=c&state="+state, pending), "/")
	if len(a.st.hashes) != 1 {
		t.Errorf("같은 계정이 새 사용자가 됐다: %v", a.st.hashes)
	}
}

func TestBookPageShowsMyCategories(t *testing.T) {
	a := newAdminSite(t)
	uid, me := a.member("hash-me")
	otherID, _ := a.member("hash-other")
	a.st.cats = []book.Category{
		{ID: 101, UserID: uid, Title: "올해의 SF"},
		{ID: 102, UserID: uid, Title: "나중에 읽기"},
		{ID: 103, UserID: otherID, Title: "남의 카테고리"},
	}
	a.st.marks = map[[2]int64][]int64{{uid, 1}: {101}, {uid, 2}: nil}

	body := html.UnescapeString(a.get("/books/1", me).Body.String())
	// 제목 아래에 이 책을 넣은 내 카테고리가 바로 보인다.
	mine := regexp.MustCompile(`(?s)<p class="my-categories">(.*?)</p>`).FindStringSubmatch(body)
	if mine == nil || !strings.Contains(mine[1], `href="/me/categories/101">올해의 SF</a>`) || strings.Contains(mine[1], "나중에 읽기") {
		t.Errorf("내 카테고리 표시 = %v", mine)
	}
	assertContains(t, body, `value="101" checked`, `value="102" >`, "북마크 빼기")
	if strings.Contains(body, "남의 카테고리") {
		t.Error("다른 사용자의 카테고리가 보인다")
	}

	// 북마크했지만 카테고리가 없으면 그렇게 보인다.
	assertContains(t, a.get("/books/2", me).Body.String(), "북마크함", "카테고리 없음")

	// 북마크하지 않은 책에는 표시가 없고, 로그인하지 않으면 로그인 안내가 보인다.
	if body := a.get("/books/3", me).Body.String(); strings.Contains(body, "my-categories") {
		t.Error("북마크하지 않은 책에 내 카테고리 표시가 있다")
	}
	body = a.get("/books/1").Body.String()
	assertContains(t, body, "로그인하면 이 책을 북마크할 수 있습니다")
	if strings.Contains(body, "북마크하기") {
		t.Error("로그인하지 않으면 북마크 폼이 없어야 한다")
	}
}

func TestSaveAndDeleteBookmark(t *testing.T) {
	a := newAdminSite(t)
	uid, me := a.member("hash-me")

	rec := a.post("/me/bookmarks/1", url.Values{"category": {"101", "abc", "-3", "102"}}, me)
	expectRedirect(t, rec, "/books/1?done=bookmark-saved#bookmark")
	expectCalls(t, a.st, fmt.Sprintf("SetBookmark %d 1 [101 102]", uid))

	if rec := a.post("/me/bookmarks/99", nil, me); rec.Code != http.StatusNotFound {
		t.Errorf("없는 책 code = %d", rec.Code)
	}
	if rec := a.post("/me/bookmarks/x", nil, me); rec.Code != http.StatusNotFound {
		t.Errorf("잘못된 번호 code = %d", rec.Code)
	}
	expectCalls(t, a.st)

	expectRedirect(t, a.post("/me/bookmarks/1/delete", nil, me), "/books/1?done=bookmark-deleted#bookmark")
	expectCalls(t, a.st, fmt.Sprintf("DeleteBookmark %d 1", uid))

	assertContains(t, a.get("/books/1?done=bookmark-saved", me).Body.String(), "북마크를 저장했습니다.")
}

func TestBookmarkPagesNeedUser(t *testing.T) {
	a := newAdminSite(t)
	expectRedirect(t, a.get("/me"), "/login?next=%2Fme")
	expectRedirect(t, a.get("/me/categories/101"), "/login?next=%2Fme%2Fcategories%2F101")
	for _, path := range []string{"/me/bookmarks/1", "/me/categories", "/me/delete"} {
		if rec := a.post(path, url.Values{"title": {"x"}}); rec.Code != http.StatusForbidden {
			t.Errorf("%s: 로그인하지 않은 POST code = %d", path, rec.Code)
		}
	}
	// 지운 사용자의 쿠키는 로그인하지 않은 것과 같다.
	uid, me := a.member("hash-me")
	_ = a.st.DeleteUser(context.Background(), uid)
	a.st.calls = nil
	expectRedirect(t, a.get("/me", me), "/login?next=%2Fme")
	if rec := a.post("/me/bookmarks/1", nil, me); rec.Code != http.StatusForbidden {
		t.Errorf("지운 사용자의 POST code = %d", rec.Code)
	}
	// 다른 사이트에서 보낸 POST는 받지 않는다.
	_, me = a.member("hash-again")
	req := httptest.NewRequest(http.MethodPost, "/me/bookmarks/1", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if rec := a.do(req, me); rec.Code != http.StatusForbidden {
		t.Errorf("다른 사이트의 POST code = %d", rec.Code)
	}
	expectCalls(t, a.st)
}

func TestCategoryPages(t *testing.T) {
	a := newAdminSite(t)
	uid, me := a.member("hash-me")

	if rec := a.post("/me/categories", url.Values{"title": {"  "}}, me); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "제목을 입력해 주세요.") {
		t.Errorf("빈 제목 code = %d", rec.Code)
	}
	rec := a.post("/me/categories", url.Values{"title": {" 올해의 SF "}}, me)
	expectRedirect(t, rec, "/me/categories/100?done=category-added")
	expectCalls(t, a.st, fmt.Sprintf("AddCategory %d 올해의 SF", uid))
	code := a.st.cats[0].Code

	a.st.err = store.ErrTooMany
	if rec := a.post("/me/categories", url.Values{"title": {"하나 더"}}, me); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "100개까지") {
		t.Errorf("한도를 넘은 카테고리 code = %d", rec.Code)
	}
	a.st.err, a.st.calls = nil, nil

	body := a.get("/me/categories/100?done=category-added", me).Body.String()
	assertContains(t, body, "카테고리를 만들었습니다.", `name="public_title"`, `name="body"`, "Markdown",
		"https://abc-lib.web.app/shared/"+code, "지금은 나에게만 보입니다", "밴드에서 책에 대한 글을 써주세요")

	form := url.Values{"title": {"올해의 SF"}, "body": {"# 소개\r\n좋아하는 책"}, "public_title": {"추천 SF"}, "public": {"1"}}
	expectRedirect(t, a.post("/me/categories/100", form, me), "/me/categories/100?done=category-saved")
	expectCalls(t, a.st, fmt.Sprintf("UpdateCategory %d 100", uid))
	if c := a.st.savedCat; c.Body != "# 소개\n좋아하는 책" || c.PublicTitle != "추천 SF" || !c.Public || c.Code != code {
		t.Errorf("저장한 카테고리 = %+v", c)
	}
	// 공개를 끄면 체크박스 값이 오지 않는다.
	form.Del("public")
	a.post("/me/categories/100", form, me)
	if a.st.savedCat.Public {
		t.Error("공개를 끄지 못했다")
	}
	a.st.calls = nil

	// 입력에 오류가 있으면 입력한 값을 그대로 두고 오류를 보인다.
	form.Set("body", strings.Repeat("가", book.MaxCategoryBody+1))
	form.Set("public_title", "고친 공개 제목")
	rec = a.post("/me/categories/100", form, me)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "본문은 20000자까지") || !strings.Contains(rec.Body.String(), `value="고친 공개 제목"`) {
		t.Errorf("긴 본문 code = %d, 본문 = %s", rec.Code, regexp.MustCompile(`(?s)<p class="notice[^>]*>.*?</p>`).FindString(rec.Body.String()))
	}
	expectCalls(t, a.st)

	// 폼이 상한보다 크면 잘린 값으로 저장하지 않는다.
	form.Set("body", strings.Repeat("가", maxUserForm))
	if rec := a.post("/me/categories/100", form, me); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("너무 큰 폼 code = %d", rec.Code)
	}
	expectCalls(t, a.st)
	form.Set("body", "본문")

	// 다른 사용자의 카테고리는 없는 것과 같다.
	_, other := a.member("hash-other")
	if rec := a.get("/me/categories/100", other); rec.Code != http.StatusNotFound {
		t.Errorf("남의 카테고리 보기 code = %d", rec.Code)
	}
	if rec := a.post("/me/categories/100", form, other); rec.Code != http.StatusNotFound {
		t.Errorf("남의 카테고리 고치기 code = %d", rec.Code)
	}
	expectCalls(t, a.st)

	expectRedirect(t, a.post("/me/categories/100/delete", nil, me), "/me?done=category-deleted")
	expectCalls(t, a.st, fmt.Sprintf("DeleteCategory %d 100", uid))
}

func TestMyBookmarksPage(t *testing.T) {
	a := newAdminSite(t)
	uid, me := a.member("hash-me")
	a.st.cats = []book.Category{
		{ID: 101, UserID: uid, Title: "올해의 SF", Public: true, Code: "sfq", Books: 1},
		{ID: 102, UserID: uid, Title: "나중에 읽기", Code: "prv"},
	}
	a.st.marks = map[[2]int64][]int64{{uid, 1}: {101, 102}}
	body := a.get("/me", me).Body.String()
	assertContains(t, body, "카테고리 2개", `href="/me/categories/101"`, "https://abc-lib.web.app/shared/sfq", "비공개",
		"북마크 1권", "당신 인생의 이야기", "올해의 SF, 나중에 읽기", "계정 지우기")
	if strings.Contains(body, "/shared/prv") {
		t.Error("공개하지 않은 카테고리의 주소가 목록에 보인다")
	}
}

func TestSharedCategory(t *testing.T) {
	a := newAdminSite(t)
	uid, me := a.member("hash-me")
	a.st.cats = []book.Category{
		{ID: 101, UserID: uid, Title: "나만 보는 제목", PublicTitle: "함께 읽을 SF", Public: true, Code: "sfq",
			Body: "**굵게**\n둘째 줄\n\n<script>alert(1)</script>\n\n[나쁜 링크](javascript:alert(1)) [좋은 링크](https://example.com/a)"},
		{ID: 102, UserID: uid, Title: "비공개", Code: "prv"},
	}

	rec := a.get("/shared/sfq")
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	assertContains(t, body, "<title>함께 읽을 SF · ABC 도서관</title>", "<strong>굵게</strong><br>", "책 2권",
		`href="/books/1"`, `href="/books/2"`, `<a href="https://example.com/a" target="_blank" rel="noopener nofollow ugc">좋은 링크</a>`)
	// 새 책 안내는 만든 사람의 카테고리 화면에만 둔다.
	for _, bad := range []string{"<script>alert", "javascript:", "나만 보는 제목", "편집</a>", "밴드에서 책에 대한 글을 써주세요"} {
		if strings.Contains(body, bad) {
			t.Errorf("공개 화면에 %q가 있으면 안 된다", bad)
		}
	}

	// 공개하지 않은 카테고리는 만든 사람에게만 보인다.
	if rec := a.get("/shared/prv"); rec.Code != http.StatusNotFound {
		t.Errorf("비공개 카테고리 code = %d", rec.Code)
	}
	_, other := a.member("hash-other")
	if rec := a.get("/shared/prv", other); rec.Code != http.StatusNotFound {
		t.Errorf("다른 사용자가 본 비공개 카테고리 code = %d", rec.Code)
	}
	rec = a.get("/shared/prv", me)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "나에게만 보입니다") || !strings.Contains(rec.Body.String(), `href="/me/categories/102"`) {
		t.Errorf("만든 사람이 본 비공개 카테고리 code = %d", rec.Code)
	}
	// 대문자로 적은 주소도 찾는다.
	if rec := a.get("/shared/SFQ"); rec.Code != http.StatusOK {
		t.Errorf("대문자 주소 code = %d", rec.Code)
	}
	for _, path := range []string{"/shared/zzz", "/shared/ab", "/shared/sfq0", "/shared/sf-", "/shared/abcdefghi", "/s/sfq"} {
		if rec := a.get(path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: code = %d", path, rec.Code)
		}
	}
}

func TestPublicCategoryList(t *testing.T) {
	a := newAdminSite(t)
	uid, me := a.member("hash-me")
	otherID, _ := a.member("hash-other")
	a.st.cats = []book.Category{
		{ID: 101, UserID: uid, Title: "나만 보는 제목", PublicTitle: "함께 읽을 SF", Public: true, Code: "sfq", Books: 3},
		{ID: 102, UserID: uid, Title: "비공개 목록", Code: "prv"},
		{ID: 103, UserID: otherID, Title: "다른 회원의 목록", Public: true, Code: "oth"},
	}
	for _, c := range []*http.Cookie{nil, me} {
		var cookies []*http.Cookie
		if c != nil {
			cookies = append(cookies, c)
		}
		rec := a.get("/shared", cookies...)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d", rec.Code)
		}
		assertContains(t, body, `<a href="/shared" aria-current="page">공개 북마크</a>`, `href="/shared/sfq">함께 읽을 SF</a>`, "책 3권", `href="/shared/oth">다른 회원의 목록</a>`)
		for _, bad := range []string{"비공개 목록", "/shared/prv", "나만 보는 제목"} {
			if strings.Contains(body, bad) {
				t.Errorf("공개 목록에 %q가 있으면 안 된다", bad)
			}
		}
	}
	a.st.cats = nil
	assertContains(t, a.get("/shared").Body.String(), "아직 공개한 카테고리가 없습니다")

	// 사용자 로그인을 쓰지 않는 서버에는 공개 북마크가 없다.
	if code, body := get(t, &fakeStore{}, "/shared"); code != http.StatusNotFound || strings.Contains(body, "공개 북마크") {
		t.Errorf("사용자 로그인 없는 서버의 /shared code = %d", code)
	}
}

func TestDeleteAccount(t *testing.T) {
	a := newAdminSite(t)
	uid, me := a.member("hash-me")
	rec := a.post("/me/delete", nil, me)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "계정을 지웠습니다") {
		t.Fatalf("code = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `href="/me"`) {
		t.Error("지운 계정으로 로그인한 것처럼 그리면 안 된다")
	}
	if c := rec.Result().Cookies()[0]; c.MaxAge >= 0 {
		t.Error("계정을 지우면 쿠키를 지워야 한다")
	}
	expectCalls(t, a.st, fmt.Sprintf("DeleteUser %d", uid))
}

func TestAdminOnlyLoginWithoutUserSecret(t *testing.T) {
	sessions, _ := auth.NewSessions(strings.Repeat("k", auth.MinSecretLen), true)
	login := &fakeLogin{sub: memberSub, email: adminAddr}
	st := &fakeStore{}
	s, err := New(st, Config{BandNo: "102765569", Login: login, Sessions: sessions}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	a := &adminSite{t: t, st: st, login: login, sessions: sessions, handler: s.Handler()}

	// 사용자 로그인을 쓰지 않으면 로그인은 모두 관리자 로그인이고, 북마크 화면은 없다.
	body := a.get("/login").Body.String()
	assertContains(t, body, "관리자 로그인", `name="admin" value="1"`)
	if strings.Contains(body, "HMAC") {
		t.Error("사용자를 저장하지 않는데 HMAC 안내가 있다")
	}
	state, c := a.startLogin("/", false)
	if !slices.Equal(login.emails, []bool{true}) {
		t.Errorf("관리자 로그인은 메일 주소를 달라고 해야 한다: %v", login.emails)
	}
	session := a.get("/auth/callback?code=c&state="+state, c).Result().Cookies()[0]
	if sess := a.read(session); sess.Email != adminAddr || sess.User != 0 || len(st.hashes) != 0 {
		t.Errorf("세션 = %+v, 사용자 %v", sess, st.hashes)
	}
	if rec := a.get("/me", session); rec.Code != http.StatusNotFound {
		t.Errorf("/me code = %d", rec.Code)
	}
	if body := a.get("/books/1").Body.String(); strings.Contains(body, "북마크") || strings.Contains(body, `href="/login?next=`) {
		t.Error("사용자 로그인을 쓰지 않으면 북마크 안내와 로그인 메뉴가 없어야 한다")
	}

	subjects, _ := auth.NewSubjects(strings.Repeat("u", auth.MinSecretLen))
	if _, err := New(st, Config{Subjects: subjects}, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Error("Login 없이 Subjects만 있으면 오류여야 한다")
	}
}
