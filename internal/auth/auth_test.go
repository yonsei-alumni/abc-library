package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const secret = "0123456789abcdef0123456789abcdef"

func TestSessionRoundTrip(t *testing.T) {
	s, err := NewSessions(secret, true)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.Set(rec, Session{Email: "admin@example.com"}, time.Hour)
	cookie := rec.Result().Cookies()[0]
	if cookie.Name != "__session" || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("쿠키 속성 = %+v", cookie)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	sess, ok := s.Read(req)
	if !ok || sess.Email != "admin@example.com" {
		t.Fatalf("세션 = %+v, %v", sess, ok)
	}

	read := func(value string, sessions *Sessions) bool {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: CookieName, Value: value})
		_, ok := sessions.Read(req)
		return ok
	}
	// 내용을 바꾸면 서명이 맞지 않는다.
	payload, sig, _ := strings.Cut(cookie.Value, ".")
	forged, _ := json.Marshal(Session{Email: "other@example.com", Expires: time.Now().Add(time.Hour).Unix()})
	for name, value := range map[string]string{
		"바꾼 내용":  base64.RawURLEncoding.EncodeToString(forged) + "." + sig,
		"서명 없음":  payload,
		"빈 값":    "",
		"잘못된 형식": "a.b.c",
	} {
		if read(value, s) {
			t.Errorf("%s: 받아들이면 안 된다", name)
		}
	}
	other, _ := NewSessions(strings.Repeat("x", MinSecretLen), true)
	if read(cookie.Value, other) {
		t.Error("다른 비밀 값으로 서명한 쿠키는 받아들이면 안 된다")
	}

	// 기한이 지나면 받아들이지 않는다.
	s.now = func() time.Time { return time.Now().Add(time.Hour + time.Second) }
	if read(cookie.Value, s) {
		t.Error("기한이 지난 세션은 받아들이면 안 된다")
	}

	rec = httptest.NewRecorder()
	s.Clear(rec)
	if c := rec.Result().Cookies()[0]; c.Name != CookieName || c.MaxAge >= 0 {
		t.Errorf("지우는 쿠키 = %+v", c)
	}
}

func TestNewSessionsNeedsLongSecret(t *testing.T) {
	if _, err := NewSessions(strings.Repeat("x", MinSecretLen-1), false); err == nil {
		t.Error("짧은 비밀 값은 오류여야 한다")
	}
	if a, b := NewState(), NewState(); a == b || len(a) < 20 {
		t.Error("state는 매번 다른 충분히 긴 값이어야 한다")
	}
}

func idToken(claims map[string]any) string {
	data, _ := json.Marshal(claims)
	return "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(data) + ".sig"
}

func validClaims() map[string]any {
	return map[string]any{
		"iss": "https://accounts.google.com", "aud": "client-1", "exp": time.Now().Add(time.Hour).Unix(),
		"sub": "109876543210987654321", "email": "Admin@Example.com", "email_verified": true,
	}
}

func TestVerifyIDToken(t *testing.T) {
	g := &Google{ClientID: "client-1"}
	want := Account{Subject: "109876543210987654321", Email: "admin@example.com"}
	if a, err := g.verify(idToken(validClaims()), time.Now()); err != nil || a != want {
		t.Fatalf("계정 = %+v, %v (메일 주소는 소문자로 바꿔야 한다)", a, err)
	}
	// 메일 주소를 달라고 하지 않았거나 Google이 확인하지 않은 주소면 계정 번호만 돌려준다.
	for name, change := range map[string]func(map[string]any){
		"메일 없음":     func(c map[string]any) { delete(c, "email"); delete(c, "email_verified") },
		"확인 안 된 메일": func(c map[string]any) { c["email_verified"] = false },
	} {
		c := validClaims()
		change(c)
		if a, err := g.verify(idToken(c), time.Now()); err != nil || a != (Account{Subject: want.Subject}) {
			t.Errorf("%s: 계정 = %+v, %v", name, a, err)
		}
	}
	for name, change := range map[string]func(map[string]any){
		"다른 발급자":   func(c map[string]any) { c["iss"] = "https://evil.example.com" },
		"다른 클라이언트": func(c map[string]any) { c["aud"] = "client-2" },
		"기한 지남":    func(c map[string]any) { c["exp"] = time.Now().Add(-time.Second).Unix() },
		"계정 번호 없음": func(c map[string]any) { delete(c, "sub") },
	} {
		c := validClaims()
		change(c)
		if _, err := g.verify(idToken(c), time.Now()); err == nil {
			t.Errorf("%s: 오류여야 한다", name)
		}
	}
	for _, bad := range []string{"", "a.b", "a.!!!.c", "a." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".c"} {
		if _, err := g.verify(bad, time.Now()); err == nil {
			t.Errorf("%q: 오류여야 한다", bad)
		}
	}
}

func TestExchange(t *testing.T) {
	var form url.Values
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": idToken(validClaims())})
	}))
	defer srv.Close()
	g := &Google{ClientID: "client-1", ClientSecret: "s3cret", RedirectURL: "https://abc-lib.web.app/auth/callback", TokenURL: srv.URL}

	a, err := g.Exchange(context.Background(), "code-1")
	if err != nil || a.Email != "admin@example.com" || a.Subject != "109876543210987654321" {
		t.Fatalf("계정 = %+v, %v", a, err)
	}
	if form.Get("code") != "code-1" || form.Get("client_secret") != "s3cret" || form.Get("grant_type") != "authorization_code" ||
		form.Get("redirect_uri") != "https://abc-lib.web.app/auth/callback" {
		t.Errorf("토큰 요청 = %v", form)
	}

	status = http.StatusBadRequest
	if _, err := g.Exchange(context.Background(), "bad"); err == nil {
		t.Error("토큰 요청이 실패하면 오류여야 한다")
	}
}

func TestAuthCodeURL(t *testing.T) {
	g := &Google{ClientID: "client-1", RedirectURL: "https://abc-lib.web.app/auth/callback"}
	for email, scope := range map[bool]string{true: "openid email", false: "openid"} {
		u, err := url.Parse(g.AuthCodeURL("st", email))
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		if u.Host != "accounts.google.com" || q.Get("state") != "st" || q.Get("client_id") != "client-1" ||
			q.Get("scope") != scope || q.Get("response_type") != "code" || q.Get("redirect_uri") != g.RedirectURL {
			t.Errorf("메일 주소 %v: 로그인 주소 = %s", email, u)
		}
	}
}

func TestSubjects(t *testing.T) {
	if _, err := NewSubjects(strings.Repeat("x", MinSecretLen-1)); err == nil {
		t.Error("짧은 비밀 값은 오류여야 한다")
	}
	s, err := NewSubjects(secret)
	if err != nil {
		t.Fatal(err)
	}
	h := s.Hash("109876543210987654321")
	if len(h) != 64 || strings.Contains(h, "109876543210987654321") {
		t.Errorf("Hash = %q, 원래 값이 드러나지 않는 64자 16진수여야 한다", h)
	}
	if s.Hash("109876543210987654321") != h {
		t.Error("같은 sub는 같은 값이어야 한다")
	}
	if s.Hash("109876543210987654322") == h {
		t.Error("다른 sub는 다른 값이어야 한다")
	}
	other, _ := NewSubjects(strings.Repeat("y", MinSecretLen))
	if other.Hash("109876543210987654321") == h {
		t.Error("키가 다르면 다른 값이어야 한다(키 없이는 같은 계정인지 맞춰 볼 수 없다)")
	}
}
