package extract

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/yonsei-alumni/abc-library/internal/book"
	"github.com/yonsei-alumni/abc-library/internal/kyobo"
)

type fakeAPI struct {
	responses []string
	calls     []anthropic.BetaMessageNewParams
}

func (f *fakeAPI) New(_ context.Context, p anthropic.BetaMessageNewParams, _ ...option.RequestOption) (*anthropic.BetaMessage, error) {
	f.calls = append(f.calls, p)
	if len(f.calls) > len(f.responses) {
		return nil, errors.New("응답이 더 없다")
	}
	var m anthropic.BetaMessage
	if err := json.Unmarshal([]byte(f.responses[len(f.calls)-1]), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

type fakeSearch struct{ queries []string }

func (f *fakeSearch) Search(_ context.Context, q string) ([]kyobo.Edition, error) {
	f.queries = append(f.queries, q)
	return []kyobo.Edition{{
		PID: "S000001981838", ISBN13: "9791196914806", Title: "당신 인생의 이야기",
		Authors: []string{"테드 창"}, Publisher: "엘리", Category: "국내도서",
	}}, nil
}

func message(stop string, content ...string) string {
	return `{"id":"msg","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"` + stop +
		`","content":[` + strings.Join(content, ",") + `],"usage":{"input_tokens":1,"output_tokens":1}}`
}

func textBlock(v any) string {
	b, _ := json.Marshal(v)
	s, _ := json.Marshal(string(b))
	return `{"type":"text","text":` + string(s) + `}`
}

type m = map[string]any

func found(title, isbn string, tags []string, postSummary string, postLinks []int, comments ...m) m {
	if comments == nil {
		comments = []m{}
	}
	if postLinks == nil {
		postLinks = []int{}
	}
	return m{"title": title, "authors": []string{"테드 창"}, "isbn13": isbn, "tags": tags,
		"post": m{"summary": postSummary, "links": postLinks}, "comments": comments, "unrelated_links": []int{}}
}

var (
	postedAt    = time.Date(2026, 10, 21, 1, 24, 0, 0, time.UTC)
	commentedAt = time.Date(2026, 10, 21, 10, 57, 0, 0, time.UTC)
	input       = Input{
		PostedAt: postedAt,
		Text:     "#책_소개 테드 창 소설집을 읽었다.",
		Links: []book.Link{
			{URL: "https://product.kyobobook.co.kr/detail/S000001981838", Title: "당신 인생의 이야기 - 교보문고"},
			{URL: "https://news.example.com/ted-chiang", Title: ""},
			{URL: "https://blog.example.com/review", Title: "리뷰"},
		},
		Images: []Image{{MediaType: "image/jpeg", Data: []byte("jpeg")}},
		Comments: []Comment{
			{At: commentedAt, Text: "같은 작가의 '숨'도 좋아요", Links: []book.Link{
				{URL: "https://www.youtube.com/watch?v=abc", Title: ""},
				{URL: "https://www.aladin.co.kr/shop/wproduct.aspx?ItemId=1", Title: "알라딘"},
			}},
			{At: commentedAt.Add(time.Hour), Text: "다음 모임 언제예요?"},
		},
	}
)

func TestExtractBuildsMentionsAndAttachesLinks(t *testing.T) {
	final := m{"books": []m{
		// 본문이 다룬 책은 한 권뿐이라, Claude가 고른 링크와 상관없이 본문 링크를 모두 붙인다(서점 링크는 뺀다).
		found("당신 인생의 이야기 (양장)", "9791196914806", []string{"SF", "SF", "없는 태그", "소설"}, "첫 줄\n둘째 줄", []int{2}),
		// 댓글에서만 소개된 책. 검색 결과에 없던 ISBN은 버린다. 잘못된 댓글 번호와 중복 번호는 뺀다.
		found("숨", "9788932917245", []string{"SF"}, "", nil,
			m{"comment": 1, "summary": "같은 작가의 다른 소설집으로 추천"}, m{"comment": 1, "summary": "중복"}, m{"comment": 9, "summary": "없는 댓글"}),
		// 같은 작품이 또 나오면 뺀다.
		found("당신 인생의 이야기", "", []string{}, "중복", nil),
		// 언급이 없는 책은 뺀다.
		found("언급 없음", "", []string{}, " ", nil),
	}}
	api := &fakeAPI{responses: []string{
		message("tool_use", `{"type":"tool_use","id":"tu_1","name":"search_kyobo","input":{"query":"당신 인생의 이야기 테드 창"}}`),
		message("end_turn", textBlock(final)),
	}}
	search := &fakeSearch{}

	got, err := New(api, search).Extract(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(search.queries) != 1 || len(got) != 2 {
		t.Fatalf("검색 %v, 찾은 책 %+v", search.queries, got)
	}

	story := got[0]
	if b := story.Book; b.ISBN13 != "9791196914806" || b.Title != "당신 인생의 이야기" || b.Publisher != "엘리" || b.KyoboPID != "S000001981838" || len(b.Authors) != 1 {
		t.Errorf("교보문고 판본 정보를 따라야 한다: %+v", b)
	}
	if strings.Join(story.Book.Tags, ",") != "SF,소설" {
		t.Errorf("태그 = %v", story.Book.Tags)
	}
	if len(story.Mentions) != 1 || story.Mentions[0].Kind != book.InPost || !story.Mentions[0].At.Equal(postedAt) {
		t.Fatalf("본문 언급 = %+v", story.Mentions)
	}
	if links := story.Mentions[0].Links; len(links) != 2 || links[0].Title != "news.example.com" || links[1].Title != "리뷰" {
		t.Errorf("서점 링크만 빼고 본문 링크를 모두 붙여야 한다: %+v", links)
	}

	breath := got[1]
	if breath.Book.ISBN13 != "" || breath.Book.Title != "숨" {
		t.Errorf("확인되지 않은 ISBN은 버려야 한다: %+v", breath.Book)
	}
	if len(breath.Mentions) != 1 || breath.Mentions[0].Kind != book.InComment || !breath.Mentions[0].At.Equal(commentedAt) {
		t.Fatalf("댓글 언급 = %+v", breath.Mentions)
	}
	if links := breath.Mentions[0].Links; len(links) != 1 || links[0].URL != "https://www.youtube.com/watch?v=abc" {
		t.Errorf("댓글의 링크를 서점 링크만 빼고 붙여야 한다: %+v", links)
	}

	if n := len(api.calls[1].Messages); n != 3 {
		t.Errorf("두 번째 요청에는 사용자, 도구 호출, 도구 결과가 있어야 한다: %d", n)
	}
}

func TestExtractUsesChosenLinksWhenPostCoversSeveralBooks(t *testing.T) {
	final := m{"books": []m{
		found("당신 인생의 이야기", "", []string{"SF"}, "요약", []int{1, 3, 7}),
		found("숨", "", []string{"SF"}, "요약", []int{2}),
	}}
	api := &fakeAPI{responses: []string{message("end_turn", textBlock(final))}}
	got, err := New(api, &fakeSearch{}).Extract(context.Background(), input)
	if err != nil || len(got) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	// 1번(교보문고)은 서점 링크라서, 7번은 없는 번호라서 빠진다.
	if l := got[0].Mentions[0].Links; len(l) != 1 || l[0].URL != "https://blog.example.com/review" {
		t.Errorf("첫 책 링크 = %+v", l)
	}
	if l := got[1].Mentions[0].Links; len(l) != 1 || l[0].URL != "https://news.example.com/ted-chiang" {
		t.Errorf("둘째 책 링크 = %+v", l)
	}
}

func TestExtractDropsUnrelatedLinks(t *testing.T) {
	in := input
	in.Comments = append(slices.Clone(input.Comments), Comment{At: commentedAt, Text: "숨 리뷰와 모임 장소예요", Links: []book.Link{
		{URL: "https://blog.example.com/breath", Title: "숨 리뷰"},
		{URL: "https://naver.me/map", Title: "네이버지도"},
	}})
	// 링크 번호: 본문 1~3, 1번 댓글 4~5, 3번 댓글 6~7
	story := found("당신 인생의 이야기", "", []string{"SF"}, "요약", nil)
	story["unrelated_links"] = []int{2, 0, 99}
	breath := found("숨", "", []string{"SF"}, "", nil, m{"comment": 1, "summary": "추천"}, m{"comment": 3, "summary": "리뷰 공유"})
	breath["unrelated_links"] = []int{4, 7}
	api := &fakeAPI{responses: []string{message("end_turn", textBlock(m{"books": []m{story, breath}}))}}

	got, err := New(api, &fakeSearch{}).Extract(context.Background(), in)
	if err != nil || len(got) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	// 본문이 한 권만 다뤄서 본문 링크를 모두 붙이지만, 관련 없다고 판정한 2번은 뺀다. 없는 번호는 무시한다.
	if l := got[0].Mentions[0].Links; len(l) != 1 || l[0].URL != "https://blog.example.com/review" {
		t.Errorf("본문 링크 = %+v", l)
	}
	// 1번 댓글의 4번(유튜브)은 관련 없고 5번은 서점 링크라서 남는 링크가 없다.
	if l := got[1].Mentions[0].Links; len(l) != 0 {
		t.Errorf("1번 댓글 링크 = %+v", l)
	}
	// 3번 댓글은 7번(지도)만 빠진다.
	if l := got[1].Mentions[1].Links; len(l) != 1 || l[0].URL != "https://blog.example.com/breath" {
		t.Errorf("3번 댓글 링크 = %+v", l)
	}
	req, _ := json.Marshal(api.calls[0])
	for _, want := range []string{"링크 [6] 숨 리뷰", "링크 [7] 네이버지도"} {
		if !strings.Contains(string(req), want) {
			t.Errorf("요청에 %s가 없다", want)
		}
	}
}

func TestExtractRequest(t *testing.T) {
	api := &fakeAPI{responses: []string{message("end_turn", textBlock(m{"books": []any{}}))}}
	got, err := New(api, &fakeSearch{}).Extract(context.Background(), input)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
	body, err := json.Marshal(api.calls[0])
	if err != nil {
		t.Fatal(err)
	}
	req := string(body)
	for _, want := range []string{
		`"model":"claude-opus-5-5"`, `"effort":"medium"`, `"type":"json_schema"`, `"fallbacks":"default"`,
		`"name":"search_kyobo"`, `"strict":true`, `"media_type":"image/jpeg"`, `게시일: 2026-10-21`,
		`[3] 리뷰 https://blog.example.com/review`, `[1] (2026-10-21) 같은 작가의 '숨'도 좋아요`,
		`링크 [4]  https://www.youtube.com/watch?v=abc`, `링크 [5] 알라딘 https://www.aladin.co.kr`, `[2] (2026-10-21) 다음 모임 언제예요?`, `"enum":["AI","SF","과학"`,
	} {
		if !strings.Contains(req, want) {
			t.Errorf("요청에 %s가 없다", want)
		}
	}
}

func TestExtractRefusal(t *testing.T) {
	api := &fakeAPI{responses: []string{message("refusal")}}
	if _, err := New(api, &fakeSearch{}).Extract(context.Background(), input); !errors.Is(err, ErrRefused) {
		t.Errorf("err = %v, want ErrRefused", err)
	}
}

func TestExtractStopsAfterTooManyToolCalls(t *testing.T) {
	var responses []string
	for range maxTurns + 1 {
		responses = append(responses, message("tool_use", `{"type":"tool_use","id":"tu","name":"search_kyobo","input":{"query":"q"}}`))
	}
	if _, err := New(&fakeAPI{responses: responses}, &fakeSearch{}).Extract(context.Background(), input); err == nil {
		t.Error("도구 호출이 끝없이 이어지면 오류여야 한다")
	}
}

func TestIsStoreLink(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://product.kyobobook.co.kr/detail/S000001981838": true,
		"https://www.yes24.com/Product/Goods/1":                true,
		"https://aladin.co.kr/shop/wproduct.aspx?ItemId=1":     true,
		"https://www.amazon.com/dp/1":                          true,
		"https://www.amazon.co.jp/dp/1":                        true,
		"https://ridibooks.com/books/1":                        true,
		"https://smartstore.naver.com/shop/1":                  true,
		"https://www.hani.co.kr/arti/1.html":                   false,
		"https://www.youtube.com/watch?v=1":                    false,
		"https://blog.naver.com/x/1":                           false,
		"https://notkyobobook.co.kr/":                          false,
	} {
		u, _ := url.Parse(raw)
		if got := IsStoreLink(u); got != want {
			t.Errorf("IsStoreLink(%s) = %v, want %v", raw, got, want)
		}
	}
}
