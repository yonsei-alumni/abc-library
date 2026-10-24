package band

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

func TestParseTime(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, seoul)
	tests := []struct {
		in   string
		want time.Time
	}{
		{"2026년 10월 23일 오후 8:09", time.Date(2026, 10, 23, 20, 9, 0, 0, seoul)},
		{"2026년 10월 21일 오전 1:24", time.Date(2026, 10, 21, 1, 24, 0, 0, seoul)},
		{"2026년 3월 5일 오후 12:30", time.Date(2026, 3, 5, 12, 30, 0, 0, seoul)},
		{"2026년 3월 5일 오전 12:30", time.Date(2026, 3, 5, 0, 30, 0, 0, seoul)},
		{"10월 23일 오후 8:09", time.Date(2026, 10, 23, 20, 9, 0, 0, seoul)},
		// 연도가 없고 올해로 보면 미래가 되는 날짜는 작년 글이다.
		{"12월 31일 오후 11:00", time.Date(2025, 12, 31, 23, 0, 0, 0, seoul)},
	}
	for _, tt := range tests {
		got, err := ParseTime(tt.in, now)
		if err != nil {
			t.Errorf("ParseTime(%q): %v", tt.in, err)
			continue
		}
		if !got.Equal(tt.want) {
			t.Errorf("ParseTime(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
	for _, bad := range []string{"", "16시간 전", "2026-10-23 20:09"} {
		if _, err := ParseTime(bad, now); err == nil {
			t.Errorf("ParseTime(%q)는 오류여야 한다", bad)
		}
	}
}

func TestPostURL(t *testing.T) {
	if got := PostURL("102765569", 187); got != "https://band.us/band/102765569/post/187" {
		t.Error(got)
	}
}

// TestExtractScript는 밴드 게시글 화면을 흉내 낸 HTML에서 추출 스크립트를 실행한다.
// Chromium이 설치되어 있지 않으면 건너뛴다(make band-login이 설치한다).
func TestExtractScript(t *testing.T) {
	pw, err := playwright.Run(&playwright.RunOptions{Verbose: false})
	if err != nil {
		t.Skip("Playwright 드라이버가 없다:", err)
	}
	defer func() { _ = pw.Stop() }()
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)})
	if err != nil {
		t.Skip("Chromium이 없다:", err)
	}
	defer func() { _ = browser.Close() }()
	page, err := browser.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile("testdata/post.html")
	if err != nil {
		t.Fatal(err)
	}
	if err := page.SetContent(string(html)); err != nil {
		t.Fatal(err)
	}
	v, err := page.Evaluate(extractScript)
	if err != nil {
		t.Fatal(err)
	}
	var s scraped
	if err := json.Unmarshal([]byte(v.(string)), &s); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(s.Text, "테드 창의 소설집") || strings.Contains(s.Text, "가상회원") {
		t.Errorf("본문만 읽고 작성자는 읽지 않아야 한다: %q", s.Text)
	}
	if s.Time != "2026년 10월 21일 오전 1:24" {
		t.Errorf("시각 = %q", s.Time)
	}
	// 본문의 교보문고 주소와 링크 미리보기는 하나로 합치고 미리보기 제목을 쓴다. 밴드 내부 링크는 뺀다.
	if len(s.Links) != 2 || s.Links[0].URL != "https://news.example.com/a" || s.Links[0].Title != "" ||
		s.Links[1].URL != "https://product.kyobobook.co.kr/detail/S000001981838" || s.Links[1].Title != "당신 인생의 이야기 - 교보문고" {
		t.Errorf("외부 링크와 링크 미리보기 제목을 읽어야 한다: %+v", s.Links)
	}
	if len(s.Images) != 1 || !strings.HasSuffix(s.Images[0], "/photo1.jpg") {
		t.Errorf("프로필 사진과 미리보기 썸네일을 뺀 첨부 사진만 읽어야 한다: %v", s.Images)
	}

	// 글자가 없는 댓글(스티커 등)은 빼고, 댓글 작성자는 읽지 않는다.
	if len(s.Comments) != 1 {
		t.Fatalf("댓글 = %+v", s.Comments)
	}
	c := s.Comments[0]
	if !strings.Contains(c.Text, "'숨'도 추천해요") || strings.Contains(c.Text, "댓글회원") || strings.Contains(raw(v), "가상학과") {
		t.Errorf("댓글 본문만 읽어야 한다: %+v", c)
	}
	if c.Time != "2026년 10월 21일 오전 10:57" || len(c.Links) != 1 || c.Links[0].URL != "https://www.youtube.com/watch?v=abc" {
		t.Errorf("댓글 시각과 링크 = %+v", c)
	}
	for _, l := range s.Links {
		if strings.Contains(l.URL, "youtube") {
			t.Error("댓글의 링크는 본문 링크에 섞이지 않아야 한다")
		}
	}
}

func raw(v any) string {
	s, _ := v.(string)
	return s
}

func TestOnPostPage(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://band.us/band/102765569/post/187", true},
		{"https://www.band.us/band/102765569/post/187", true},
		{"https://www.band.us/band/102765569/post/187/", true},
		// 삭제된 글은 목록으로 옮겨 간다.
		{"https://www.band.us/band/102765569/post", false},
		{"https://www.band.us/band/102765569/post/1870", false},
		{"https://www.band.us/band/999/post/187", false},
		{"https://auth.band.us/login_page", false},
		{"https://evil.example.com/band/102765569/post/187", false},
	}
	for _, tt := range tests {
		if got := OnPostPage(tt.url, "102765569", 187); got != tt.want {
			t.Errorf("OnPostPage(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}
