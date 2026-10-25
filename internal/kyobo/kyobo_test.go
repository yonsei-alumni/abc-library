package kyobo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

func TestParseSearch(t *testing.T) {
	page, err := os.ReadFile("testdata/search.html")
	if err != nil {
		t.Fatal(err)
	}
	eds := ParseSearch(string(page))

	// 세 번째 상품은 ISBN이 아닌 바코드를 가진 eBook이라 빠진다.
	if len(eds) != 2 {
		t.Fatalf("판본 수 = %d, want 2: %+v", len(eds), eds)
	}
	want := Edition{
		PID:       "S000001981838",
		ISBN13:    "9791196914806",
		Title:     "당신 인생의 이야기",
		Authors:   []string{"테드 창"},
		Publisher: "엘리",
		PubDate:   "2020년 01월 28일",
		Category:  "국내도서",
	}
	if !reflect.DeepEqual(eds[0], want) {
		t.Errorf("첫 판본\n got %+v\nwant %+v", eds[0], want)
	}
	if eds[1].ISBN13 != "9788956057842" || eds[1].PID != "S000000814614" {
		t.Errorf("두 번째 판본 = %+v", eds[1])
	}
}

func TestParseSearchEmpty(t *testing.T) {
	if eds := ParseSearch("<html><body>검색 결과가 없습니다</body></html>"); len(eds) != 0 {
		t.Errorf("결과가 없어야 한다: %+v", eds)
	}
}

func TestIsAuthorRole(t *testing.T) {
	for role, want := range map[string]bool{
		"저자(글)": true, "지은이": true, "글": true, "엮음": true, "편저": true,
		"번역": false, "그림": false, "감수": false,
	} {
		if got := isAuthorRole(role); got != want {
			t.Errorf("isAuthorRole(%q) = %v, want %v", role, got, want)
		}
	}
}

func TestSearchSendsQueryAndFailsOnBadStatus(t *testing.T) {
	var gotKeyword, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKeyword = r.URL.Query().Get("keyword")
		gotUA = r.UserAgent()
		if gotKeyword == "오류" {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()

	c := NewClient()
	c.BaseURL = srv.URL
	c.MinInterval = 0
	if _, err := c.Search(context.Background(), "당신 인생의 이야기 테드 창"); err != nil {
		t.Fatal(err)
	}
	if gotKeyword != "당신 인생의 이야기 테드 창" || gotUA != userAgent {
		t.Errorf("keyword=%q ua=%q", gotKeyword, gotUA)
	}
	if _, err := c.Search(context.Background(), "오류"); err == nil {
		t.Error("응답 코드가 200이 아니면 오류여야 한다")
	}
}

func TestProductPID(t *testing.T) {
	for raw, want := range map[string]string{
		"https://product.kyobobook.co.kr/detail/S000213812009":                   "S000213812009",
		"http://product.kyobobook.co.kr/detail/S000213812009/":                   "S000213812009",
		"https://PRODUCT.kyobobook.co.kr/detail/S000213812009?utm_source=band#x": "S000213812009",
		" https://product.kyobobook.co.kr/detail/S000213812009 ":                 "S000213812009",
		// 상품 페이지가 아닌 주소와 잘못된 상품 번호는 뺀다.
		"https://product.kyobobook.co.kr/detail/":                           "",
		"https://product.kyobobook.co.kr/detail/s000213812009":              "",
		"https://product.kyobobook.co.kr/detail/S000213812009/review":       "",
		"https://product.kyobobook.co.kr/book/S000213812009":                "",
		"https://ebook-product.kyobobook.co.kr/dig/epd/ebook/E000002956543": "",
		"https://search.kyobobook.co.kr/search?keyword=S000213812009":       "",
		"https://product.kyobobook.co.kr.example.com/detail/S000213812009":  "",
		"javascript://product.kyobobook.co.kr/detail/S000213812009":         "",
		"https://www.yes24.com/Product/Goods/1":                             "",
		"":                                                                  "",
	} {
		pid, ok := ProductPID(raw)
		if pid != want || ok != (want != "") {
			t.Errorf("ProductPID(%q) = %q, %v, want %q", raw, pid, ok, want)
		}
	}
}

func TestURLs(t *testing.T) {
	if got := ProductURL("S000001981838"); got != "https://product.kyobobook.co.kr/detail/S000001981838" {
		t.Error(got)
	}
	if got := CoverURL("9791196914806", 200); got != "https://contents.kyobobook.co.kr/sih/fit-in/200x0/pdt/9791196914806.jpg" {
		t.Error(got)
	}
	if got := SearchURL("무수면 사회"); got != "https://search.kyobobook.co.kr/search?keyword=%EB%AC%B4%EC%88%98%EB%A9%B4+%EC%82%AC%ED%9A%8C" {
		t.Error(got)
	}
}
