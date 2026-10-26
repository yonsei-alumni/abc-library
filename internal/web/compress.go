package web

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

// 앞단의 Firebase Hosting과 Google Frontend가 Cloud Run의 응답을 압축해 주지 않으므로 웹 서버가 직접 gzip으로 압축한다.
// 이미 Content-Encoding이 붙은 응답은 그대로 두므로 앞단이 압축하게 되더라도 두 번 압축하지 않는다.

// minCompressSize보다 작은 응답은 압축해도 얻는 것이 없거나 오히려 커진다.
const minCompressSize = 512

var gzipWriters = sync.Pool{
	New: func() any { return gzip.NewWriter(nil) },
}

// compressible은 압축해서 줄어드는 Content-Type인지 알려 준다. PNG, WebP처럼 이미 압축된 형식은 뺀다.
func compressible(contentType string) bool {
	ct, _, _ := strings.Cut(contentType, ";")
	ct = strings.ToLower(strings.TrimSpace(ct))
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	switch ct {
	case "application/javascript", "application/json", "application/xml",
		"application/manifest+json", "application/xhtml+xml", "image/svg+xml":
		return true
	}
	return false
}

// acceptsGzip은 요청의 Accept-Encoding이 gzip을 받는지 알려 준다. 'gzip;q=0'은 받지 않는다는 뜻이다.
func acceptsGzip(r *http.Request) bool {
	for _, v := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(v, ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		for _, p := range strings.Split(params, ";") {
			k, val, _ := strings.Cut(p, "=")
			if strings.EqualFold(strings.TrimSpace(k), "q") {
				q, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
				return err == nil && q > 0
			}
		}
		return true
	}
	return false
}

// gzipResponseWriter는 본문의 앞부분을 모았다가 압축할지 정한 뒤에 헤더와 본문을 내보낸다.
// Content-Type을 핸들러가 늦게 정하거나 내용으로 짐작하게 두는 경우가 있어서 본문을 볼 때까지 미룬다.
type gzipResponseWriter struct {
	http.ResponseWriter

	status  int
	buf     []byte
	decided bool
	gz      *gzip.Writer // 압축하지 않기로 했으면 nil이다.
}

func (w *gzipResponseWriter) WriteHeader(status int) {
	// 1xx는 최종 응답이 아니므로 바로 보낸다.
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status == 0 {
		w.status = status
	}
}

func (w *gzipResponseWriter) Write(p []byte) (int, error) {
	if w.decided {
		if w.gz != nil {
			return w.gz.Write(p)
		}
		return w.ResponseWriter.Write(p)
	}
	w.buf = append(w.buf, p...)
	if len(w.buf) >= minCompressSize {
		if err := w.decide(); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Flush는 모은 본문이 작아도 압축할지 정하고 지금까지의 본문을 내보낸다.
func (w *gzipResponseWriter) Flush() {
	if !w.decided {
		_ = w.decide()
	}
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap은 http.ResponseController가 원래 ResponseWriter의 기능을 쓰게 한다.
func (w *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// decide는 압축할지 정하고 헤더와 모은 본문을 내보낸다.
func (w *gzipResponseWriter) decide() error {
	w.decided = true
	if w.status == 0 {
		w.status = http.StatusOK
	}
	h := w.Header()
	ct := h.Get("Content-Type")
	if ct == "" && len(w.buf) > 0 {
		ct = http.DetectContentType(w.buf)
		h.Set("Content-Type", ct)
	}
	// 본문이 없는 응답과 일부만 보내는 응답(Range)은 압축하면 안 된다.
	skip := h.Get("Content-Encoding") != "" || h.Get("Content-Range") != "" ||
		w.status == http.StatusNoContent || w.status == http.StatusNotModified || w.status == http.StatusPartialContent ||
		len(w.buf) < minCompressSize || !compressible(ct)
	if skip {
		w.ResponseWriter.WriteHeader(w.status)
		_, err := w.ResponseWriter.Write(w.buf)
		w.buf = nil
		return err
	}

	h.Set("Content-Encoding", "gzip")
	// 압축하면 길이가 달라진다.
	h.Del("Content-Length")
	w.gz = gzipWriters.Get().(*gzip.Writer)
	w.gz.Reset(w.ResponseWriter)
	w.ResponseWriter.WriteHeader(w.status)
	_, err := w.gz.Write(w.buf)
	w.buf = nil
	return err
}

// finish는 핸들러가 끝난 뒤에 남은 본문을 내보내고 압축기를 돌려준다.
func (w *gzipResponseWriter) finish() {
	if !w.decided {
		_ = w.decide()
	}
	if w.gz != nil {
		_ = w.gz.Close()
		gzipWriters.Put(w.gz)
		w.gz = nil
	}
}

// compress는 gzip을 받는 클라이언트에게 HTML, CSS, JS 같은 텍스트 응답을 압축해서 보낸다.
func compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 같은 주소가 압축본과 원본으로 나가므로 캐시가 둘을 나눠 두게 한다.
		w.Header().Add("Vary", "Accept-Encoding")
		if !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.finish()
		next.ServeHTTP(gw, r)
	})
}

// gzipFiles는 파일 시스템의 파일 중 압축해서 줄어드는 것을 미리 압축해 둔다. 키는 파일 경로다.
// 정적 파일은 바뀌지 않으므로 요청마다 압축하지 않고 시작할 때 한 번 가장 높은 압축률로 압축한다.
func gzipFiles(fsys fs.FS) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !compressible(mime.TypeByExtension(path.Ext(name))) {
			return err
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if err != nil {
			return err
		}
		if _, err := gz.Write(data); err != nil {
			return err
		}
		if err := gz.Close(); err != nil {
			return err
		}
		if len(data) >= minCompressSize && buf.Len() < len(data) {
			files[name] = buf.Bytes()
		}
		return nil
	})
	return files, err
}
