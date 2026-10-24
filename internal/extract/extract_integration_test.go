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

// 여러 권으로 된 책은 한 권만 넣고, 그 책과 직접 관련 없는 링크는 빼는지 본다.
func TestExtractSeriesAndUnrelatedLinksWithClaude(t *testing.T) {
	client, err := NewClient()
	if err != nil {
		t.Skip(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	posted := time.Now().Add(-24 * time.Hour)
	in := Input{
		PostedAt: posted,
		Text: `#독서_인증
넬레 노이하우스의 신작 '몬스터' 1, 2권을 주말에 다 읽었어요. 호프하임 경찰서 시리즈답게 결말이 궁금해서 손을 놓을 수 없었습니다.
자세한 감상은 블로그에 적었어요. 이번 모임 장소 지도와 요즘 화제인 AI 기사도 같이 올립니다.`,
		Links: []book.Link{
			{URL: "https://blog.example.com/monster-review", Title: "넬레 노이하우스 '몬스터' 읽고 - 독서 일기"},
			{URL: "https://naver.me/map-example", Title: "네이버지도"},
			{URL: "https://news.example.com/gpu", Title: "젠슨 황 \"새 GPU로 AI 시대 연다\""},
		},
		Comments: []Comment{
			{At: posted.Add(time.Hour), Text: "닐 셔스터먼의 수확자 시리즈도 추천해요. 죽음이 사라진 미래 이야기예요."},
		},
	}
	got, err := New(&client.Beta.Messages, kyobo.NewClient()).Extract(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	var monsters, reapers []book.Finding
	for _, f := range got {
		t.Logf("책: %+v", f.Book)
		for _, m := range f.Mentions {
			t.Logf("  [%s] 링크: %+v", m.Kind, m.Links)
		}
		if strings.Contains(f.Book.Title, "세트") || strings.Contains(f.Book.Title, "전") && strings.Contains(f.Book.Title, "권") {
			t.Errorf("세트 상품을 고르면 안 된다: %+v", f.Book)
		}
		switch {
		case strings.Contains(f.Book.Title, "몬스터"):
			monsters = append(monsters, f)
		case strings.Contains(f.Book.Title, "수확자"):
			reapers = append(reapers, f)
		}
	}
	if len(monsters) != 1 {
		t.Fatalf("언급 하나가 1, 2권을 함께 다루면 한 권만 넣어야 한다: %d권", len(monsters))
	}
	if title := monsters[0].Book.Title; strings.Contains(title, "2") {
		t.Errorf("여러 권을 함께 다루면 앞선 권을 골라야 한다: %s", title)
	}
	if links := monsters[0].Mentions[0].Links; len(links) != 1 || !strings.Contains(links[0].URL, "monster-review") {
		t.Errorf("그 책의 감상 링크만 남고 지도와 다른 주제 기사는 빠져야 한다: %+v", links)
	}
	if len(reapers) != 1 {
		t.Errorf("댓글에서 소개한 시리즈를 한 권으로 넣어야 한다: %d권", len(reapers))
	}
}
