// Package book은 책과 책을 다룬 게시글을 표현하는 도메인 코드다.
package book

import (
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

// TopicTags는 매달 모임에서 정한 주제다. 새 주제가 정해지면 맨 앞에 더한다.
var TopicTags = []string{"AI", "SF", "과학"}

// KindTags는 책의 갈래를 나타내는 태그다.
var KindTags = []string{
	"소설", "추리·미스터리", "판타지", "시", "에세이",
	"인문", "철학", "역사", "사회·정치", "경제·경영", "자기계발",
	"기술·IT", "예술", "여행", "종교", "어린이·청소년", "만화",
}

// Tags는 책에 붙일 수 있는 모든 태그다. 추출할 때 이 목록에서만 고르게 한다.
var Tags = slices.Concat(TopicTags, KindTags)

// SortTags는 태그를 Tags의 순서(모임 주제가 먼저)로 정렬한다. 목록에 없는 태그는 뒤에 둔다.
func SortTags(tags []string) []string {
	sorted := slices.Clone(tags)
	slices.SortStableFunc(sorted, func(a, b string) int {
		return rank(a) - rank(b)
	})
	return sorted
}

func rank(tag string) int {
	if i := slices.Index(Tags, tag); i >= 0 {
		return i
	}
	return len(Tags)
}

// Book은 책 한 권이다. 같은 작품의 판본은 하나의 Book으로 묶는다. 태그는 여러 개 붙는다.
type Book struct {
	ID        int64
	ISBN13    string
	Title     string
	Authors   []string
	Publisher string
	KyoboPID  string
	Tags      []string
}

// Link는 요약에 덧붙이는 책 관련 링크다.
type Link struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

// MentionKind는 책을 언급한 곳이다.
type MentionKind string

const (
	// InPost는 게시글 본문에서 다룬 것이다.
	InPost MentionKind = "post"
	// InComment는 게시글의 댓글에서 다룬 것이다.
	InComment MentionKind = "comment"
)

// Mention은 게시글 본문이나 댓글 하나가 책에 대해 한 이야기를 개인 정보를 빼고 요약한 것이다.
// 책 상세 화면에 댓글처럼 쌓인다.
type Mention struct {
	PostNo  int
	Kind    MentionKind
	At      time.Time
	Summary string
	Links   []Link
}

// Finding은 게시글 하나(본문과 댓글)에서 찾은 책과 그 책에 대한 언급이다.
type Finding struct {
	Book     Book
	Mentions []Mention
}

// Post는 저장할 게시글의 식별 정보다. 본문과 댓글은 저장하지 않는다.
type Post struct {
	No          int
	PostedAt    time.Time
	ContentHash string
}

// Thread는 책 상세 화면에서 게시글 하나에 속한 언급의 묶음이다.
// 본문 언급이 있으면 Post에 두고, 댓글 언급은 그 아래에 답글처럼 붙인다.
type Thread struct {
	PostNo   int
	Post     *Mention
	Comments []Mention
}

// Threads는 시각 순으로 정렬된 언급을 게시글별로 묶는다. 묶음의 순서는 각 게시글의 첫 언급 순서다.
func Threads(mentions []Mention) []Thread {
	var threads []Thread
	index := map[int]int{}
	for _, m := range mentions {
		i, ok := index[m.PostNo]
		if !ok {
			i = len(threads)
			index[m.PostNo] = i
			threads = append(threads, Thread{PostNo: m.PostNo})
		}
		if m.Kind == InPost && threads[i].Post == nil {
			post := m
			threads[i].Post = &post
		} else {
			threads[i].Comments = append(threads[i].Comments, m)
		}
	}
	return threads
}

// Shelved는 책 목록에 보여 줄 책과 마지막으로 다뤄진 시각이다.
type Shelved struct {
	Book         Book
	LastPostedAt time.Time
	PostCount    int
}

// Entry는 타임라인의 소식 하나다. IsNew가 참이면 이 게시글로 책이 처음 등록됐다는 뜻이다.
type Entry struct {
	Book     Book
	PostNo   int
	PostedAt time.Time
	IsNew    bool
}

// Count는 태그나 저자별 책 수다.
type Count struct {
	Name  string
	Books int
}

var bracketed = regexp.MustCompile(`\([^)]*\)|\[[^\]]*\]`)

// WorkKey는 판본과 표기 차이를 무시하고 같은 작품을 가리키는 키를 만든다.
// 괄호 안의 판본 표기와 콜론 뒤의 부제를 빼고, 글자와 숫자만 남긴 제목에 대표 저자를 붙인다.
func WorkKey(title string, authors []string) string {
	key := normalize(title, true)
	if len(authors) > 0 {
		key += "|" + normalize(authors[0], false)
	}
	return key
}

func normalize(s string, dropSubtitle bool) string {
	s = bracketed.ReplaceAllString(s, "")
	if dropSubtitle {
		if i := strings.IndexAny(s, ":："); i > 0 {
			s = s[:i]
		}
	}
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ValidISBN13은 s가 체크섬까지 맞는 ISBN-13인지 검사한다.
func ValidISBN13(s string) bool {
	if len(s) != 13 || (!strings.HasPrefix(s, "978") && !strings.HasPrefix(s, "979")) {
		return false
	}
	sum := 0
	for i, r := range s {
		if r < '0' || r > '9' {
			return false
		}
		d := int(r - '0')
		if i%2 == 1 {
			d *= 3
		}
		sum += d
	}
	return sum%10 == 0
}
