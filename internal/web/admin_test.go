package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yonsei-alumni/abc-library/internal/auth"
	"github.com/yonsei-alumni/abc-library/internal/book"
	"github.com/yonsei-alumni/abc-library/internal/store"
)

type fakeLogin struct {
	email string
	err   error
	codes []string
}

func (f *fakeLogin) AuthCodeURL(state string) string {
	return "https://accounts.example.com/auth?state=" + url.QueryEscape(state)
}

func (f *fakeLogin) Exchange(_ context.Context, code string) (string, error) {
	f.codes = append(f.codes, code)
	return f.email, f.err
}

type adminSite struct {
	t        *testing.T
	st       *fakeStore
	login    *fakeLogin
	sessions *auth.Sessions
	handler  http.Handler
}

func newAdminSite(t *testing.T) *adminSite {
	t.Helper()
	sessions, err := auth.NewSessions(strings.Repeat("k", auth.MinSecretLen), true)
	if err != nil {
		t.Fatal(err)
	}
	a := &adminSite{t: t, st: &fakeStore{}, login: &fakeLogin{email: adminAddr}, sessions: sessions}
	s, err := New(a.st, Config{BandNo: "102765569", Login: a.login, Sessions: sessions, SiteURL: "https://abc-lib.web.app"},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	a.handler = s.Handler()
	return a
}

// cookie는 email로 로그인한 세션 쿠키다.
func (a *adminSite) cookie(email string) *http.Cookie {
	rec := httptest.NewRecorder()
	a.sessions.Set(rec, auth.Session{Email: email}, time.Hour)
	return rec.Result().Cookies()[0]
}

func (a *adminSite) do(req *http.Request, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "no value") {
		a.t.Errorf("%s %s: 템플릿에 없는 값이 찍혔다", req.Method, req.URL)
	}
	return rec
}

func (a *adminSite) get(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return a.do(httptest.NewRequest(http.MethodGet, path, nil), cookies...)
}

// post는 같은 사이트의 화면에서 보낸 것처럼 폼을 보낸다.
func (a *adminSite) post(path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	return a.do(req, cookies...)
}

func (a *adminSite) admin() *http.Cookie { return a.cookie(adminAddr) }

func expectRedirect(t *testing.T, rec *httptest.ResponseRecorder, location string) {
	t.Helper()
	if rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
		t.Fatalf("code = %d, 본문 = %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Location"); got != location {
		t.Errorf("Location = %q, want %q", got, location)
	}
}

func expectCalls(t *testing.T, st *fakeStore, want ...string) {
	t.Helper()
	if strings.Join(st.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls = %q, want %q", st.calls, want)
	}
	st.calls = nil
}

func TestLoginFlow(t *testing.T) {
	a := newAdminSite(t)
	rec := a.get("/login?next=%2Fbooks%2F1")
	if rec.Code != http.StatusFound {
		t.Fatalf("code = %d", rec.Code)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	state := loc.Query().Get("state")
	pending := rec.Result().Cookies()[0]
	if loc.Host != "accounts.example.com" || state == "" || pending.Name != "__session" {
		t.Fatalf("Google 로그인으로 보내야 한다: %s, %+v", loc, pending)
	}

	// 쿠키가 없거나 state가 다르면 다른 사이트가 꾸민 응답일 수 있으므로 받지 않는다.
	if rec := a.get("/auth/callback?code=c&state=" + state); rec.Code != http.StatusBadRequest {
		t.Errorf("쿠키 없는 응답 code = %d", rec.Code)
	}
	if rec := a.get("/auth/callback?code=c&state=other", pending); rec.Code != http.StatusBadRequest {
		t.Errorf("state가 다른 응답 code = %d", rec.Code)
	}
	if rec := a.get("/auth/callback?error=access_denied&state="+state, pending); rec.Code != http.StatusBadRequest {
		t.Errorf("취소한 로그인 code = %d", rec.Code)
	}

	rec = a.get("/auth/callback?code=c1&state="+state, pending)
	expectRedirect(t, rec, "/books/1")
	session := rec.Result().Cookies()[0]
	if strings.Join(a.login.codes, ",") != "c1" {
		t.Errorf("Google에 code를 보내야 한다: %v", a.login.codes)
	}

	rec = a.get("/books/1", session)
	assertContains(t, rec.Body.String(), adminAddr, "로그아웃", `href="/admin/tags"`, `href="/admin/books/1"`, `href="/admin/mentions/5"`)
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("로그인한 사람의 화면은 캐시하지 않아야 한다: %q", got)
	}

	// 이미 로그인했으면 바로 돌아간다.
	expectRedirect(t, a.get("/login?next=/tags", session), "/tags")

	rec = a.post("/logout", nil, session)
	expectRedirect(t, rec, "/")
	if c := rec.Result().Cookies()[0]; c.MaxAge >= 0 {
		t.Error("로그아웃하면 쿠키를 지워야 한다")
	}
}

func TestLoginRejectsNonAdminAndBadNext(t *testing.T) {
	a := newAdminSite(t)
	start := func(next string) (string, *http.Cookie) {
		rec := a.get("/login?next=" + url.QueryEscape(next))
		loc, _ := url.Parse(rec.Header().Get("Location"))
		return loc.Query().Get("state"), rec.Result().Cookies()[0]
	}

	// 다른 사이트 주소로는 돌려보내지 않는다.
	for _, next := range []string{"//evil.example.com", "https://evil.example.com", "/\\evil.example.com", ""} {
		state, c := start(next)
		expectRedirect(t, a.get("/auth/callback?code=c&state="+state, c), "/")
	}

	a.login.email = "member@example.com"
	state, c := start("/admin/tags")
	rec := a.get("/auth/callback?code=c&state="+state, c)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "관리자로 등록되지 않은 계정") {
		t.Errorf("관리자가 아닌 계정 code = %d", rec.Code)
	}
	if c := rec.Result().Cookies()[0]; c.MaxAge >= 0 {
		t.Error("관리자가 아니면 세션을 남기지 않아야 한다")
	}

	a.login.err = errors.New("토큰 오류")
	state, c = start("/")
	if rec := a.get("/auth/callback?code=c&state="+state, c); rec.Code != http.StatusBadRequest || strings.Contains(rec.Body.String(), "토큰 오류") {
		t.Errorf("Google 확인 실패 code = %d", rec.Code)
	}
}

func TestAdminPagesNeedAdmin(t *testing.T) {
	a := newAdminSite(t)
	expectRedirect(t, a.get("/admin/tags"), "/login?next=%2Fadmin%2Ftags")
	expectRedirect(t, a.get("/admin/books/1?x=1"), "/login?next=%2Fadmin%2Fbooks%2F1%3Fx%3D1")
	// 관리자에서 빠진 계정의 세션은 로그인하지 않은 것과 같다.
	expectRedirect(t, a.get("/admin/tags", a.cookie("member@example.com")), "/login?next=%2Fadmin%2Ftags")
	for _, c := range [][]*http.Cookie{nil, {a.cookie("member@example.com")}} {
		if rec := a.post("/admin/tags", url.Values{"name": {"기후"}, "kind": {"topic"}}, c...); rec.Code != http.StatusForbidden {
			t.Errorf("관리자가 아닌 POST code = %d", rec.Code)
		}
	}
	expectCalls(t, a.st)

	// 로그인하지 않은 방문자에게는 로그인 링크만 보인다.
	body := a.get("/").Body.String()
	assertContains(t, body, `href="/login?next=%2f"`, "관리자 로그인")
	if strings.Contains(body, `href="/admin/tags"`) {
		t.Error("관리 메뉴는 관리자에게만 보여야 한다")
	}

	// 관리자 로그인을 설정하지 않은 서버에는 관리 화면이 없다.
	for _, path := range []string{"/admin/tags", "/login", "/auth/callback"} {
		if code, _ := get(t, &fakeStore{}, path); code != http.StatusNotFound {
			t.Errorf("%s: code = %d", path, code)
		}
	}
}

func TestAdminRejectsCrossSitePost(t *testing.T) {
	a := newAdminSite(t)
	for name, headers := range map[string]map[string]string{
		"다른 사이트":       {"Sec-Fetch-Site": "cross-site"},
		"같은 사이트 다른 출처": {"Sec-Fetch-Site": "same-site"},
		"다른 Origin":    {"Origin": "https://evil.example.com"},
	} {
		req := httptest.NewRequest(http.MethodPost, "/admin/tags/delete", strings.NewReader("name=SF"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if rec := a.do(req, a.admin()); rec.Code != http.StatusForbidden {
			t.Errorf("%s: code = %d", name, rec.Code)
		}
	}
	expectCalls(t, a.st)

	// Firebase Hosting을 거치면 Host가 Cloud Run 주소로 바뀌지만, 사이트 주소에서 온 요청은 받는다.
	req := httptest.NewRequest(http.MethodPost, "https://abc-library-xyz.a.run.app/admin/tags/delete", strings.NewReader("name=SF"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://abc-lib.web.app")
	expectRedirect(t, a.do(req, a.admin()), "/admin/tags?done=tag-deleted")
	expectCalls(t, a.st, "DeleteTag SF")
}

func TestAdminTags(t *testing.T) {
	a := newAdminSite(t)
	admin := a.admin()
	rec := a.get("/admin/tags", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	body := rec.Body.String()
	topics, kinds, _ := strings.Cut(body, "책의 갈래</h2>")
	if !strings.Contains(topics, "#AI") || !strings.Contains(topics, "#SF") || strings.Contains(topics, "#소설") || !strings.Contains(kinds, "#소설") {
		t.Error("모임 주제와 갈래를 나눠 보여야 한다")
	}
	assertContains(t, body, `<td class="num">5</td>`, "태그 더하기")

	expectRedirect(t, a.post("/admin/tags", url.Values{"name": {" #기후 "}, "kind": {"topic"}}, admin), "/admin/tags?done=tag-added")
	expectRedirect(t, a.post("/admin/tags/rename", url.Values{"name": {"SF"}, "new_name": {"공상과학"}}, admin), "/admin/tags?done=tag-renamed")
	expectRedirect(t, a.post("/admin/tags/move", url.Values{"name": {"SF"}, "dir": {"up"}}, admin), "/admin/tags?done=tag-moved")
	expectRedirect(t, a.post("/admin/tags/move", url.Values{"name": {"SF"}, "dir": {"down"}}, admin), "/admin/tags?done=tag-moved")
	expectRedirect(t, a.post("/admin/tags/delete", url.Values{"name": {"SF"}}, admin), "/admin/tags?done=tag-deleted")
	expectCalls(t, a.st, "AddTag 기후 topic", "RenameTag SF 공상과학", "MoveTag SF true", "MoveTag SF false", "DeleteTag SF")

	assertContains(t, a.get("/admin/tags?done=tag-added", admin).Body.String(), "태그를 더했습니다.")

	// 잘못된 입력은 저장하지 않고 오류를 보인다.
	for _, form := range []url.Values{
		{"name": {""}, "kind": {"kind"}},
		{"name": {strings.Repeat("가", 21)}, "kind": {"kind"}},
		{"name": {"기후"}, "kind": {"genre"}},
	} {
		if rec := a.post("/admin/tags", form, admin); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "notice-error") {
			t.Errorf("%v: code = %d", form, rec.Code)
		}
	}
	if rec := a.post("/admin/tags/rename", url.Values{"name": {"SF"}, "new_name": {"a,b"}}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("잘못된 새 이름 code = %d", rec.Code)
	}
	expectCalls(t, a.st)

	a.st.err = store.ErrConflict
	rec = a.post("/admin/tags", url.Values{"name": {"SF"}, "kind": {"kind"}}, admin)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "같은 이름의 태그가 이미 있습니다") {
		t.Errorf("같은 이름 code = %d", rec.Code)
	}
	a.st.err = store.ErrNotFound
	if rec := a.post("/admin/tags/delete", url.Values{"name": {"없는 태그"}}, admin); rec.Code != http.StatusNotFound {
		t.Errorf("없는 태그 code = %d", rec.Code)
	}
	a.st.err = errors.New("DB 오류")
	if rec := a.post("/admin/tags/delete", url.Values{"name": {"SF"}}, admin); rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "DB 오류") {
		t.Errorf("DB 오류 code = %d", rec.Code)
	}
}

func TestAdminBook(t *testing.T) {
	a := newAdminSite(t)
	admin := a.admin()
	rec := a.get("/admin/books/1", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	body := rec.Body.String()
	assertContains(t, body, `value="당신 인생의 이야기"`, `value="테드 창"`, `value="9791196914806"`, `value="S000001981838"`,
		`value="SF" checked`, `value="소설" checked`, "이야기 3개", `href="/admin/mentions/5"`)
	if strings.Contains(body, `value="AI" checked`) {
		t.Error("붙지 않은 태그는 체크하지 않아야 한다")
	}
	if code := a.get("/admin/books/99", admin).Code; code != http.StatusNotFound {
		t.Errorf("없는 책 code = %d", code)
	}
	// 언급을 모두 지운 책도 관리자는 열 수 있다.
	assertContains(t, a.get("/books/3", admin).Body.String(), "이야기 0개", `href="/admin/books/3"`)

	form := url.Values{
		"title": {" 당신 인생의 이야기 "}, "authors": {"테드 창, 김상훈 ,"}, "publisher": {"엘리"},
		"isbn13": {"979-11-969148-0-6"}, "kyobo_pid": {" S000001981838 "}, "tag": {"SF", "AI"},
	}
	expectRedirect(t, a.post("/admin/books/1", form, admin), "/admin/books/1?done=book-saved")
	if b := a.st.updated; b.ID != 1 || b.Title != "당신 인생의 이야기" || strings.Join(b.Authors, "|") != "테드 창|김상훈" ||
		b.ISBN13 != "9791196914806" || b.KyoboPID != "S000001981838" || strings.Join(b.Tags, ",") != "SF,AI" {
		t.Errorf("저장한 책 = %+v", b)
	}
	expectCalls(t, a.st, "UpdateBook 1")

	for name, change := range map[string]func(url.Values){
		"제목 없음":      func(f url.Values) { f.Set("title", " ") },
		"틀린 ISBN":    func(f url.Values) { f.Set("isbn13", "9791196914807") },
		"ISBN-10":    func(f url.Values) { f.Set("isbn13", "8937460440") },
		"숫자 아닌 ISBN": func(f url.Values) { f.Set("isbn13", "abc") },
	} {
		bad := url.Values{}
		for k, v := range form {
			bad[k] = v
		}
		change(bad)
		rec := a.post("/admin/books/1", bad, admin)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "notice-error") {
			t.Errorf("%s: code = %d", name, rec.Code)
		}
	}
	expectCalls(t, a.st)

	// 같은 책이 있으면 그 책으로 합치라고 안내하고 입력한 값을 남긴다.
	a.st.err = &store.DuplicateError{BookID: 2}
	rec = a.post("/admin/books/1", form, admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("같은 책 code = %d", rec.Code)
	}
	assertContains(t, rec.Body.String(), `href="/books/2"`, `name="into" value="2"`, `value="테드 창, 김상훈"`)
	a.st.err = nil
	a.st.calls = nil

	expectRedirect(t, a.post("/admin/books/1/merge", url.Values{"into": {"2"}}, admin), "/admin/books/2?done=book-merged")
	expectRedirect(t, a.post("/admin/books/1/delete", nil, admin), "/admin/books/1?done=book-deleted")
	expectCalls(t, a.st, "MergeBook 1 2", "DeleteBook 1")
	for _, into := range []string{"1", "", "x", "-2"} {
		if rec := a.post("/admin/books/1/merge", url.Values{"into": {into}}, admin); rec.Code != http.StatusBadRequest {
			t.Errorf("into=%q: code = %d", into, rec.Code)
		}
	}
	a.st.err = store.ErrNotFound
	if rec := a.post("/admin/books/1/merge", url.Values{"into": {"99"}}, admin); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "99번 책이 없습니다") {
		t.Errorf("없는 책으로 합치기 code = %d", rec.Code)
	}
}

func TestAdminMention(t *testing.T) {
	a := newAdminSite(t)
	admin := a.admin()
	rec := a.get("/admin/mentions/5", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	assertContains(t, rec.Body.String(), "본문 첫 줄\n본문 둘째 줄", "https://news.example.com/a 관련 기사", "밴드 184번 글", `href="/admin/books/1"`)
	if code := a.get("/admin/mentions/6", admin).Code; code != http.StatusNotFound {
		t.Errorf("없는 언급 code = %d", code)
	}

	form := url.Values{"summary": {" 고친 요약\r\n둘째 줄 "}, "links": {"https://blog.example.com/r 서평 제목\r\n\r\nhttps://example.com/x\n"}}
	expectRedirect(t, a.post("/admin/mentions/5", form, admin), "/admin/books/1?done=mention-saved")
	expectCalls(t, a.st, "UpdateMention 5 고친 요약\n둘째 줄")
	if l := a.st.links; len(l) != 2 || l[0] != (book.Link{URL: "https://blog.example.com/r", Title: "서평 제목"}) || l[1].Title != "example.com" {
		t.Errorf("링크 = %+v", l)
	}

	for name, f := range map[string]url.Values{
		"요약 없음":    {"summary": {"  "}},
		"너무 긴 요약":  {"summary": {strings.Repeat("가", maxSummary+1)}},
		"잘못된 링크":   {"summary": {"요약"}, "links": {"javascript:alert(1) 제목"}},
		"주소 없는 링크": {"summary": {"요약"}, "links": {"제목만"}},
	} {
		if rec := a.post("/admin/mentions/5", f, admin); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "notice-error") {
			t.Errorf("%s: code = %d", name, rec.Code)
		}
	}
	expectCalls(t, a.st)

	expectRedirect(t, a.post("/admin/mentions/5/move", url.Values{"book": {"2"}}, admin), "/admin/books/2?done=mention-moved")
	expectRedirect(t, a.post("/admin/mentions/5/delete", nil, admin), "/admin/books/1?done=mention-deleted")
	expectCalls(t, a.st, "MoveMention 5 2", "DeleteMention 5")
	if rec := a.post("/admin/mentions/5/move", url.Values{"book": {"없음"}}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("잘못된 책 번호 code = %d", rec.Code)
	}
	a.st.err = store.ErrNotFound
	if rec := a.post("/admin/mentions/5/move", url.Values{"book": {"99"}}, admin); rec.Code != http.StatusNotFound {
		t.Errorf("없는 책으로 옮기기 code = %d", rec.Code)
	}
}

func TestAdminAdmins(t *testing.T) {
	a := newAdminSite(t)
	admin := a.admin()
	rec := a.get("/admin/admins", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	body := rec.Body.String()
	assertContains(t, body, "관리자 2명", "other@example.com", "(나)", `value="other@example.com"`)
	if strings.Contains(body, `name="email" value="admin@example.com"`) {
		t.Error("자기 자신을 빼는 버튼은 없어야 한다")
	}

	expectRedirect(t, a.post("/admin/admins", url.Values{"email": {" New@Example.com "}}, admin), "/admin/admins?done=admin-added")
	expectRedirect(t, a.post("/admin/admins/delete", url.Values{"email": {"other@example.com"}}, admin), "/admin/admins?done=admin-deleted")
	expectCalls(t, a.st, "AddAdmin new@example.com admin@example.com", "DeleteAdmin other@example.com")

	for _, email := range []string{"", "not-an-email", "이름 <x@example.com>", "a@b@c"} {
		if rec := a.post("/admin/admins", url.Values{"email": {email}}, admin); rec.Code != http.StatusBadRequest {
			t.Errorf("%q: code = %d", email, rec.Code)
		}
	}
	if rec := a.post("/admin/admins/delete", url.Values{"email": {"ADMIN@example.com"}}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("자기 자신 빼기 code = %d", rec.Code)
	}
	expectCalls(t, a.st)

	a.st.err = store.ErrConflict
	if rec := a.post("/admin/admins", url.Values{"email": {"other@example.com"}}, admin); rec.Code != http.StatusConflict {
		t.Errorf("이미 있는 관리자 code = %d", rec.Code)
	}
	a.st.err = store.ErrNotFound
	if rec := a.post("/admin/admins/delete", url.Values{"email": {"gone@example.com"}}, admin); rec.Code != http.StatusNotFound {
		t.Errorf("없는 관리자 code = %d", rec.Code)
	}
}

func TestParseLinks(t *testing.T) {
	links, err := parseLinks("  https://a.example.com/x   제목 있는 링크 \n\nhttp://b.example.com")
	if err != nil || len(links) != 2 || links[0].Title != "제목 있는 링크" || links[1] != (book.Link{URL: "http://b.example.com", Title: "b.example.com"}) {
		t.Errorf("links = %+v, %v", links, err)
	}
	if links, err := parseLinks(" \n "); err != nil || links != nil {
		t.Errorf("빈 입력 = %+v, %v", links, err)
	}
	for _, bad := range []string{"ftp://a.example.com", "https://", "/relative 제목", "https://ok.example.com\nbad"} {
		if _, err := parseLinks(bad); err == nil {
			t.Errorf("%q: 오류여야 한다", bad)
		}
	}
	if got := formatLinks([]book.Link{{URL: "https://a.example.com", Title: "제목"}}); got != "https://a.example.com 제목\n" {
		t.Errorf("formatLinks = %q", got)
	}
}
