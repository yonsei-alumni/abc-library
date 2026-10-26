package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// largeText는 압축할 만큼 큰 본문이다.
var largeText = strings.Repeat("<p>가나다 abc</p>\n", 100)

func compressed(t *testing.T, h http.Handler, method, acceptEncoding string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, "/", nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rec := httptest.NewRecorder()
	compress(h).ServeHTTP(rec, req)
	return rec.Result()
}

// gunzip은 응답 본문을 Content-Encoding에 맞게 읽는다.
func gunzip(t *testing.T, res *http.Response) string {
	t.Helper()
	var r io.Reader = res.Body
	if res.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(res.Body)
		if err != nil {
			t.Fatalf("gzip을 풀지 못했다: %v", err)
		}
		r = zr
	}
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("본문을 읽지 못했다: %v", err)
	}
	return string(body)
}

func writeText(contentType string, status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		if status != 0 {
			w.WriteHeader(status)
		}
		// 템플릿처럼 여러 번 나눠 쓴다.
		for chunk := range strings.SplitAfterSeq(body, "\n") {
			_, _ = io.WriteString(w, chunk)
		}
	}
}

func TestCompressDecision(t *testing.T) {
	html := "text/html; charset=utf-8"
	tests := []struct {
		name           string
		handler        http.Handler
		method         string
		acceptEncoding string
		wantGzip       bool
		wantStatus     int
		wantBody       string
	}{
		{"큰 HTML", writeText(html, 0, largeText), http.MethodGet, "gzip, deflate, br", true, http.StatusOK, largeText},
		{"상태 코드는 그대로", writeText(html, http.StatusNotFound, largeText), http.MethodGet, "gzip", true, http.StatusNotFound, largeText},
		{"Content-Type을 정하지 않은 HTML", writeText("", 0, "<!doctype html>"+largeText), http.MethodGet, "gzip", true, http.StatusOK, "<!doctype html>" + largeText},
		{"CSS", writeText("text/css; charset=utf-8", 0, largeText), http.MethodGet, "gzip", true, http.StatusOK, largeText},
		{"JS", writeText("text/javascript; charset=utf-8", 0, largeText), http.MethodGet, "gzip", true, http.StatusOK, largeText},
		{"q 값이 있는 gzip", writeText(html, 0, largeText), http.MethodGet, "br;q=1.0, gzip;q=0.5", true, http.StatusOK, largeText},
		{"gzip을 받지 않는 클라이언트", writeText(html, 0, largeText), http.MethodGet, "", false, http.StatusOK, largeText},
		{"gzip;q=0", writeText(html, 0, largeText), http.MethodGet, "gzip;q=0", false, http.StatusOK, largeText},
		{"gzip; q=0.0", writeText(html, 0, largeText), http.MethodGet, "deflate, gzip; q=0.0", false, http.StatusOK, largeText},
		{"이름만 비슷한 인코딩", writeText(html, 0, largeText), http.MethodGet, "x-gzipped", false, http.StatusOK, largeText},
		{"작은 응답", writeText(html, 0, "ok"), http.MethodGet, "gzip", false, http.StatusOK, "ok"},
		{"경계보다 한 바이트 작은 응답", writeText(html, 0, strings.Repeat("a", minCompressSize-1)), http.MethodGet, "gzip", false, http.StatusOK, strings.Repeat("a", minCompressSize-1)},
		{"경계 크기의 응답", writeText(html, 0, strings.Repeat("a", minCompressSize)), http.MethodGet, "gzip", true, http.StatusOK, strings.Repeat("a", minCompressSize)},
		{"이미지", writeText("image/png", 0, largeText), http.MethodGet, "gzip", false, http.StatusOK, largeText},
		{"이미 압축한 응답", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Encoding", "br")
			writeText(html, 0, largeText)(w, r)
		}), http.MethodGet, "gzip", false, http.StatusOK, largeText},
		{"일부만 보내는 응답", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Range", "bytes 0-1599/3000")
			writeText(html, http.StatusPartialContent, largeText)(w, r)
		}), http.MethodGet, "gzip", false, http.StatusPartialContent, largeText},
		{"204", writeText(html, http.StatusNoContent, ""), http.MethodGet, "gzip", false, http.StatusNoContent, ""},
		{"304", writeText(html, http.StatusNotModified, ""), http.MethodGet, "gzip", false, http.StatusNotModified, ""},
		{"본문을 쓰지 않은 HEAD", writeText(html, 0, ""), http.MethodHead, "gzip", false, http.StatusOK, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := compressed(t, tt.handler, tt.method, tt.acceptEncoding)
			if got := res.Header.Get("Content-Encoding") == "gzip"; got != tt.wantGzip {
				t.Errorf("gzip 압축 = %v, 기대 %v", got, tt.wantGzip)
			}
			if res.StatusCode != tt.wantStatus {
				t.Errorf("상태 코드 = %d, 기대 %d", res.StatusCode, tt.wantStatus)
			}
			if !strings.Contains(res.Header.Get("Vary"), "Accept-Encoding") {
				t.Errorf("Vary = %q, Accept-Encoding이 있어야 한다", res.Header.Get("Vary"))
			}
			if body := gunzip(t, res); body != tt.wantBody {
				t.Errorf("본문이 원본과 다르다: %d바이트, 기대 %d바이트", len(body), len(tt.wantBody))
			}
		})
	}
}

func TestCompressDropsContentLength(t *testing.T) {
	res := compressed(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1600")
		writeText("text/plain; charset=utf-8", 0, largeText)(w, r)
	}), http.MethodGet, "gzip")
	if res.Header.Get("Content-Encoding") != "gzip" || res.Header.Get("Content-Length") != "" {
		t.Errorf("압축하면 원본 길이를 지워야 한다: %v", res.Header)
	}
}

func TestCompressFlushSendsSmallBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "처음")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("Flush 실패: %v", err)
		}
		if !rec.Flushed || rec.Body.String() != "처음" {
			t.Errorf("Flush하면 모은 본문을 내보내야 한다: %q", rec.Body.String())
		}
		_, _ = io.WriteString(w, " 다음")
	})).ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != "처음 다음" {
		t.Errorf("작은 본문은 압축하지 않고 이어서 보내야 한다: %q", rec.Body.String())
	}
}

func TestPagesAreCompressed(t *testing.T) {
	st := &fakeStore{entries: pageSize + 3}
	tests := []struct {
		name    string
		path    string
		headers []string
		want    string
	}{
		{"화면 전체", "/", nil, "<html"},
		{"HTMX 더 보기 조각", "/?page=2", []string{"HX-Request", "true", "HX-Target", "timeline-more"}, "/books/1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.Header.Set("Accept-Encoding", "gzip")
			for i := 0; i+1 < len(tt.headers); i += 2 {
				req.Header.Set(tt.headers[i], tt.headers[i+1])
			}
			rec := httptest.NewRecorder()
			newServer(t, st).Handler().ServeHTTP(rec, req)
			res := rec.Result()
			if res.Header.Get("Content-Encoding") != "gzip" {
				t.Fatalf("압축하지 않았다: %v", res.Header)
			}
			if res.Header.Get("X-Robots-Tag") == "" {
				t.Error("압축해도 다른 헤더는 그대로여야 한다")
			}
			assertContains(t, gunzip(t, res), tt.want)
		})
	}

	// robots.txt처럼 작은 응답은 압축하지 않는다.
	req := httptest.NewRequest(http.MethodGet, "/robots.txt", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	newServer(t, st).Handler().ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "" || !strings.HasPrefix(rec.Body.String(), "User-agent") {
		t.Errorf("작은 응답은 압축하지 않아야 한다: %v", rec.Header())
	}
}

func TestStaticFilesAreServedPrecompressed(t *testing.T) {
	css, err := staticFiles.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	png, err := staticFiles.ReadFile("static/icon-192.png")
	if err != nil {
		t.Fatal(err)
	}
	h := newServer(t, &fakeStore{}).Handler()
	serve := func(path string, headers ...string) *http.Response {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Result()
	}

	res := serve(staticURL("style.css"), "Accept-Encoding", "gzip")
	if res.Header.Get("Content-Encoding") != "gzip" || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/css") {
		t.Fatalf("CSS는 미리 압축한 것을 보내야 한다: %v", res.Header)
	}
	if res.Header.Get("Cache-Control") == "" || !strings.Contains(res.Header.Get("Vary"), "Accept-Encoding") {
		t.Errorf("캐시 헤더 = %v", res.Header)
	}
	gz, _ := io.ReadAll(res.Body)
	if len(gz) == 0 || len(gz) >= len(css) {
		t.Errorf("압축한 길이 = %d, 원본 %d", len(gz), len(css))
	}
	res.Body = io.NopCloser(bytes.NewReader(gz))
	if gunzip(t, res) != string(css) {
		t.Error("압축을 풀면 원본 CSS와 같아야 한다")
	}

	res = serve("/static/style.css")
	if res.Header.Get("Content-Encoding") != "" || gunzip(t, res) != string(css) {
		t.Errorf("gzip을 받지 않으면 원본을 보내야 한다: %v", res.Header)
	}

	res = serve("/static/icon-192.png", "Accept-Encoding", "gzip")
	if res.Header.Get("Content-Encoding") != "" || gunzip(t, res) != string(png) {
		t.Errorf("PNG는 압축하지 않아야 한다: %v", res.Header)
	}

	res = serve("/static/no-such.css", "Accept-Encoding", "gzip")
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("없는 파일 = %d", res.StatusCode)
	}

	// Range 요청은 압축본의 일부를 보내고 다시 압축하지 않는다.
	res = serve("/static/style.css", "Accept-Encoding", "gzip", "Range", "bytes=0-9")
	if body, _ := io.ReadAll(res.Body); res.StatusCode != http.StatusPartialContent || len(body) != 10 || res.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("Range 응답 = %d, %d바이트, %v", res.StatusCode, len(body), res.Header)
	}
}

func TestGzipFilesSkipsWhatDoesNotShrink(t *testing.T) {
	files, err := gzipFiles(staticFiles)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"static/style.css", "static/htmx.min.js", "static/app.js"} {
		if _, ok := files[name]; !ok {
			t.Errorf("%s를 미리 압축해야 한다", name)
		}
	}
	// 이미 압축된 이미지와 압축할 만큼 크지 않은 파일은 두지 않는다.
	for _, name := range []string{"static/icon-192.png", "static/help/bookmark.webp", "static/icon.svg"} {
		if _, ok := files[name]; ok {
			t.Errorf("%s는 미리 압축하지 않아야 한다", name)
		}
	}
}
