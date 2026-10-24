package book

import (
	"strings"
	"testing"
	"time"
)

func TestWorkKeyIgnoresEditionAndSpacing(t *testing.T) {
	base := WorkKey("당신 인생의 이야기", []string{"테드 창"})
	same := []struct {
		title   string
		authors []string
	}{
		{"당신 인생의 이야기 (양장본 HardCover)", []string{"테드 창"}},
		{"당신인생의이야기", []string{"테드창"}},
		{"[리커버] 당신 인생의 이야기", []string{"테드 창", "김상훈"}},
		{"당신 인생의 이야기: 테드 창 소설집", []string{"테드 창"}},
	}
	for _, s := range same {
		if got := WorkKey(s.title, s.authors); got != base {
			t.Errorf("WorkKey(%q, %v) = %q, want %q", s.title, s.authors, got, base)
		}
	}
}

func TestWorkKeyDistinguishesAuthors(t *testing.T) {
	if WorkKey("이방인", []string{"알베르 카뮈"}) == WorkKey("이방인", []string{"다른 저자"}) {
		t.Error("저자가 다르면 다른 작품이어야 한다")
	}
	if WorkKey("이방인", nil) == WorkKey("이방인", []string{"알베르 카뮈"}) {
		t.Error("저자를 모르는 책과 저자를 아는 책은 키가 달라야 한다")
	}
}

func TestThreadsGroupsMentionsByPost(t *testing.T) {
	at := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	threads := Threads([]Mention{
		{PostNo: 3, Kind: InComment, At: at(1), Summary: "3번 글 댓글"},
		{PostNo: 5, Kind: InPost, At: at(2), Summary: "5번 글 본문"},
		{PostNo: 3, Kind: InComment, At: at(3), Summary: "3번 글 두 번째 댓글"},
		{PostNo: 5, Kind: InComment, At: at(4), Summary: "5번 글 댓글"},
	})
	if len(threads) != 2 || threads[0].PostNo != 3 || threads[1].PostNo != 5 {
		t.Fatalf("첫 언급 순서로 묶어야 한다: %+v", threads)
	}
	if threads[0].Post != nil || len(threads[0].Comments) != 2 {
		t.Errorf("댓글에서만 다룬 글은 본문 없이 댓글만 있어야 한다: %+v", threads[0])
	}
	if threads[1].Post == nil || threads[1].Post.Summary != "5번 글 본문" || len(threads[1].Comments) != 1 {
		t.Errorf("본문 아래에 댓글이 붙어야 한다: %+v", threads[1])
	}
	if Threads(nil) != nil {
		t.Error("언급이 없으면 묶음도 없어야 한다")
	}
}

func TestSortTagsPutsTopicsFirst(t *testing.T) {
	got := SortTags([]string{"역사", "목록에 없는 태그", "과학", "AI", "소설"})
	want := []string{"AI", "과학", "소설", "역사", "목록에 없는 태그"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("SortTags = %v, want %v", got, want)
	}
}

func TestTagsHaveNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, tag := range Tags {
		if seen[tag] {
			t.Errorf("태그 %q가 중복된다", tag)
		}
		seen[tag] = true
	}
}

func TestValidISBN13(t *testing.T) {
	tests := []struct {
		isbn string
		want bool
	}{
		{"9791196914806", true},
		{"9788956057842", true},
		{"9791196914807", false}, // 체크섬 불일치
		{"4808956057842", false}, // 978, 979로 시작하지 않음
		{"979119691480", false},  // 12자리
		{"97911969148a6", false}, // 숫자가 아님
		{"", false},
	}
	for _, tt := range tests {
		if got := ValidISBN13(tt.isbn); got != tt.want {
			t.Errorf("ValidISBN13(%q) = %v, want %v", tt.isbn, got, tt.want)
		}
	}
}
