package band

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"

	"github.com/yonsei-alumni/abc-library/internal/book"
)

func TestParseTime(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, book.Seoul)
	tests := []struct {
		in   string
		want time.Time
	}{
		{"2026년 10월 23일 오후 8:09", time.Date(2026, 10, 23, 20, 9, 0, 0, book.Seoul)},
		{"2026년 10월 21일 오전 1:24", time.Date(2026, 10, 21, 1, 24, 0, 0, book.Seoul)},
		{"2026년 3월 5일 오후 12:30", time.Date(2026, 3, 5, 12, 30, 0, 0, book.Seoul)},
		{"2026년 3월 5일 오전 12:30", time.Date(2026, 3, 5, 0, 30, 0, 0, book.Seoul)},
		{"10월 23일 오후 8:09", time.Date(2026, 10, 23, 20, 9, 0, 0, book.Seoul)},
		// 연도가 없고 올해로 보면 미래가 되는 날짜는 작년 글이다.
		{"12월 31일 오후 11:00", time.Date(2025, 12, 31, 23, 0, 0, 0, book.Seoul)},
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

// evaluate는 testdata의 HTML을 브라우저에 띄우고 스크립트를 실행한다.
// Chromium이 설치되어 있지 않으면 건너뛴다(make band-login이 설치한다).
func evaluate(t *testing.T, file, script string, args ...any) any {
	t.Helper()
	pw, err := playwright.Run(&playwright.RunOptions{Verbose: false})
	if err != nil {
		t.Skip("Playwright 드라이버가 없다:", err)
	}
	t.Cleanup(func() { _ = pw.Stop() })
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)})
	if err != nil {
		t.Skip("Chromium이 없다:", err)
	}
	t.Cleanup(func() { _ = browser.Close() })
	page, err := browser.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := page.SetContent(string(html)); err != nil {
		t.Fatal(err)
	}
	v, err := page.Evaluate(script, args...)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestCardsScript는 게시글 목록 화면을 흉내 낸 HTML에서 카드의 글 번호와 댓글 수를 읽는다.
func TestCardsScript(t *testing.T) {
	v := evaluate(t, "testdata/list.html", cardsScript, "1")
	var cards []Activity
	if err := json.Unmarshal([]byte(raw(v)), &cards); err != nil {
		t.Fatal(err)
	}
	// 카드 밖의 링크, 본문 속 다른 글 링크, 다른 밴드의 글은 빼고, 표정 수를 댓글 수로 읽지 않는다.
	want := []Activity{{No: 30, Comments: 2}, {No: 12, Comments: 1024}, {No: 5, Comments: 0}}
	if !slices.Equal(cards, want) {
		t.Errorf("카드 = %+v, want %+v", cards, want)
	}
	if strings.Contains(raw(v), "회원") || strings.Contains(raw(v), "미리보기") {
		t.Errorf("작성자와 댓글은 읽지 않아야 한다: %s", raw(v))
	}
}

// TestExtractScript는 밴드 게시글 화면을 흉내 낸 HTML에서 추출 스크립트를 실행한다.
func TestExtractScript(t *testing.T) {
	v := evaluate(t, "testdata/post.html", extractScript)
	var s scraped
	if err := json.Unmarshal([]byte(v.(string)), &s); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(s.Text, "테드 창의 소설집") || strings.Contains(s.Text, "가상회원") {
		t.Errorf("본문만 읽고 작성자는 읽지 않아야 한다: %q", s.Text)
	}
	if !strings.Contains(s.Text, "《소설가 구보씨의 일일》") {
		t.Errorf("사진 아래 이어지는 본문 덩어리도 읽어야 한다: %q", s.Text)
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
	// 댓글 수는 화면에 나온 수를 읽는다. 글자가 없어서 뺀 댓글도 세고, 표정 수와 헷갈리지 않는다.
	if s.CommentCount != 2 {
		t.Errorf("댓글 수 = %d", s.CommentCount)
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
