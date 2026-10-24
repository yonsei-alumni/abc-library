//go:build integration

package extract

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yonsei-alumni/abc-library/internal/book"
	"github.com/yonsei-alumni/abc-library/internal/kyobo"
)

// 실제 Claude API와 교보문고 검색을 호출한다. 게시글과 댓글은 지어낸 것이고,
// 지어낸 개인 정보가 요약에 남지 않는지, 댓글에서 소개한 책을 찾는지 본다.
// 실행: make test-integration
func TestExtractWithClaude(t *testing.T) {
	client, err := NewClient()
	if err != nil {
		t.Skip(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	posted := time.Now().Add(-24 * time.Hour)
	in := Input{
		PostedAt: posted,
		Text: `#책_소개
홍길동(경영 97, 판교 IT회사 다님) 선배가 지난주 신촌 모임에서 추천해 준 테드 창의 '당신 인생의 이야기'를 다 읽었어요.
표제작은 외계 언어를 배우면서 시간을 다르게 인식하게 되는 언어학자의 이야기인데, 딸 지민이를 키우는 제 처지와 겹쳐서 많이 울었네요.
'바빌론의 탑'도 신화를 과학적으로 풀어 내는 상상력이 대단했습니다. SF를 어려워하는 분들께도 권합니다.`,
		Links: []book.Link{
			{URL: "https://product.kyobobook.co.kr/detail/S000001981838", Title: "당신 인생의 이야기 - 교보문고"},
			{URL: "https://www.hani.co.kr/arti/culture/book/1.html", Title: "테드 창 인터뷰 - 한겨레"},
		},
		Comments: []Comment{
			{At: posted.Add(time.Hour), Text: "저는 김철수(법학 98)인데요, 같은 작가의 '숨'이 더 좋았어요. 인공지능 이야기도 있어서 이번 달 AI 주제랑도 맞아요.",
				Links: []book.Link{{URL: "https://www.youtube.com/watch?v=ted-chiang", Title: ""}}},
			{At: posted.Add(2 * time.Hour), Text: "다음 모임은 언제인가요?"},
		},
	}
	got, err := New(&client.Beta.Messages, kyobo.NewClient()).Extract(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	var story, breath *book.Finding
	for i, f := range got {
		t.Logf("책: %+v", f.Book)
		for _, m := range f.Mentions {
			t.Logf("  [%s] %s\n%s\n  링크: %+v", m.Kind, m.At.Format(time.DateTime), m.Summary, m.Links)
			for _, private := range []string{"홍길동", "길동", "김철수", "97", "98", "판교", "IT회사", "지민", "신촌", "법학", "경영"} {
				if strings.Contains(m.Summary, private) {
					t.Errorf("요약에 개인 정보 %q가 남았다", private)
				}
			}
			for _, l := range m.Links {
				if strings.Contains(l.URL, "kyobobook") {
					t.Errorf("서점 구매 링크가 남았다: %s", l.URL)
				}
			}
		}
		switch {
		case strings.Contains(f.Book.Title, "당신 인생의 이야기"):
			story = &got[i]
		case strings.HasPrefix(f.Book.Title, "숨"):
			breath = &got[i]
		}
	}
	if story == nil || !book.ValidISBN13(story.Book.ISBN13) || len(story.Mentions) == 0 || story.Mentions[0].Kind != book.InPost {
		t.Fatalf("본문의 책을 교보문고 판본으로 찾아야 한다: %+v", story)
	}
	if len(story.Mentions[0].Links) != 1 || !strings.Contains(story.Mentions[0].Links[0].URL, "hani.co.kr") {
		t.Errorf("본문 링크는 서점 링크만 빼고 모두 붙어야 한다: %+v", story.Mentions[0].Links)
	}
	if breath == nil {
		t.Fatal("댓글에서 소개한 책을 찾아야 한다")
	}
	var commented bool
	for _, m := range breath.Mentions {
		if m.Kind == book.InComment && len(m.Links) == 1 && strings.Contains(m.Links[0].URL, "youtube") {
			commented = true
		}
	}
	if !commented {
		t.Errorf("댓글 언급과 그 댓글의 링크가 있어야 한다: %+v", breath.Mentions)
	}
}
