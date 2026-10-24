package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestManifest(t *testing.T) {
	s := newServer(t, &fakeStore{})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/manifest+json" {
		t.Fatalf("code = %d, Content-Type = %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var m struct {
		Name, StartURL, Scope, Display string
		Icons                          []struct{ Src, Sizes, Purpose string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Name != "ABC 도서관" || m.Display != "standalone" {
		t.Errorf("manifest = %+v", m)
	}
	// 설치 조건: 192px와 512px 아이콘, 마스크용 아이콘
	sizes := map[string]bool{}
	for _, icon := range m.Icons {
		sizes[icon.Sizes+"/"+icon.Purpose] = true
		if code, _ := get(t, &fakeStore{}, icon.Src); code != http.StatusOK {
			t.Errorf("%s: code = %d", icon.Src, code)
		}
	}
	for _, want := range []string{"192x192/", "512x512/", "512x512/maskable"} {
		if !sizes[want] {
			t.Errorf("아이콘 %s가 없다", want)
		}
	}
}

func TestServiceWorker(t *testing.T) {
	s := newServer(t, &fakeStore{})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sw.js", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("code = %d, Content-Type = %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Error("새 서비스 워커를 바로 받도록 캐시하지 않아야 한다")
	}
	if strings.Contains(body, "__VERSION__") || strings.Contains(body, "__PRECACHE__") {
		t.Error("자리표시자가 남아 있다")
	}
	if !regexp.MustCompile(`const VERSION = '[0-9a-f]{8}';`).MatchString(body) {
		t.Error("VERSION이 정적 파일 해시로 채워져야 한다")
	}
	// 미리 받는 정적 파일은 화면이 쓰는 주소와 같아야 오프라인 화면에 스타일이 적용된다.
	assertContains(t, body, `"/offline"`, `"`+staticURL("style.css")+`"`, `"`+staticURL("htmx.min.js")+`"`)
	for _, name := range precached {
		if _, err := staticFiles.ReadFile("static/" + name); err != nil {
			t.Errorf("미리 받을 파일 %s가 없다", name)
		}
	}
}

func TestOfflinePage(t *testing.T) {
	code, body := get(t, &fakeStore{}, "/offline")
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	assertContains(t, body, "인터넷에 연결되지 않았습니다", staticURL("style.css"))
}

func TestLayoutRegistersPWA(t *testing.T) {
	_, body := get(t, &fakeStore{}, "/")
	assertContains(t, body, `<link rel="manifest" href="/manifest.webmanifest">`, `register("/sw.js")`,
		`rel="apple-touch-icon" href="`+staticURL("apple-touch-icon.png")+`"`, `name="theme-color"`)
}
