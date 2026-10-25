package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yonsei-alumni/abc-library/internal/band"
	"github.com/yonsei-alumni/abc-library/internal/book"
	"github.com/yonsei-alumni/abc-library/internal/extract"
	"github.com/yonsei-alumni/abc-library/internal/store"
)

type fakeSource struct {
	latest int
	posts  map[int]band.Post
	errs   map[int]error
	opened []int
	// cards는 '최신 댓글순' 목록의 카드다. read는 그중 읽은 카드 수다.
	cards    []band.Activity
	cardsErr error
	read     int
}

func (f *fakeSource) LatestPostNo() (int, error) { return f.latest, nil }

func (f *fakeSource) RecentlyCommented(done func(band.Activity) bool, limit int) ([]band.Activity, error) {
	if f.cardsErr != nil {
		return nil, f.cardsErr
	}
	var out []band.Activity
	for _, a := range f.cards {
		if len(out) == limit {
			break
		}
		out = append(out, a)
		if done(a) {
			break
		}
	}
	f.read = len(out)
	return out, nil
}

func (f *fakeSource) Post(no int) (band.Post, error) {
	f.opened = append(f.opened, no)
	if err := f.errs[no]; err != nil {
		return band.Post{}, err
	}
	p, ok := f.posts[no]
	if !ok {
		return band.Post{}, band.ErrNotFound
	}
	return p, nil
}

type fakeExtractor struct {
	calls int
	errs  map[string]error
}

func (f *fakeExtractor) Extract(_ context.Context, in extract.Input) ([]book.Finding, error) {
	f.calls++
	if err := f.errs[in.Text]; err != nil {
		return nil, err
	}
	return []book.Finding{{Book: book.Book{Title: in.Text}, Mentions: []book.Mention{{Kind: book.InPost, Summary: "요약"}}}}, nil
}

type fakeStore struct {
	last    int
	hashes  map[int]string
	counts  map[int]int
	recent  []int
	saved   map[int][]book.Finding
	pending map[int]string
	runs    []store.Run
}

func (f *fakeStore) LastPostNo(context.Context) (int, error) { return f.last, nil }

func (f *fakeStore) PostHash(_ context.Context, no int) (string, bool, error) {
	h, ok := f.hashes[no]
	return h, ok, nil
}

func (f *fakeStore) PostNosSince(context.Context, time.Time) ([]int, error) { return f.recent, nil }

func (f *fakeStore) CommentCounts(context.Context) (map[int]int, error) {
	counts := map[int]int{}
	for no := range f.hashes {
		counts[no] = -1
		if c, ok := f.counts[no]; ok {
			counts[no] = c
		}
	}
	return counts, nil
}

func (f *fakeStore) SetCommentCount(_ context.Context, no, count int) error {
	if f.counts == nil {
		f.counts = map[int]int{}
	}
	f.counts[no] = count
	return nil
}

func (f *fakeStore) SavePost(ctx context.Context, p book.Post, findings []book.Finding) error {
	f.saved[p.No] = findings
	f.hashes[p.No] = p.ContentHash
	delete(f.pending, p.No)
	return f.SetCommentCount(ctx, p.No, p.CommentCount)
}

func (f *fakeStore) StartRun(context.Context) (int64, error) {
	return int64(len(f.runs) + 1), nil
}

func (f *fakeStore) FinishRun(_ context.Context, r store.Run) error {
	f.runs = append(f.runs, r)
	return nil
}

func (f *fakeStore) MarkPending(_ context.Context, no int, reason, _ string) error {
	if f.pending == nil {
		f.pending = map[int]string{}
	}
	f.pending[no] = reason
	return nil
}

func (f *fakeStore) PendingPostNos(context.Context, int) ([]int, error) {
	var nos []int
	for no := range f.pending {
		nos = append(nos, no)
	}
	slices.Sort(nos)
	return nos, nil
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func post(no int, text string) band.Post {
	return band.Post{No: no, PostedAt: time.Date(2026, 10, no, 0, 0, 0, 0, time.UTC), Text: text}
}

func TestRunProcessesNewAndChangedPosts(t *testing.T) {
	unchanged := post(2, "그대로인 글")
	src := &fakeSource{latest: 6, posts: map[int]band.Post{
		2: unchanged, 3: post(3, "수정된 글"), 4: post(4, "새 글"), 6: post(6, "거절될 글"),
	}}
	st := &fakeStore{
		last:   3,
		recent: []int{2, 3},
		hashes: map[int]string{2: ContentHash(unchanged), 3: "옛 해시"},
		saved:  map[int][]book.Finding{},
	}
	ex := &fakeExtractor{errs: map[string]error{"거절될 글": extract.ErrRefused}}

	r, err := Run(context.Background(), src, ex, st, Options{RecheckDays: 3}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	// 2는 바뀌지 않았고, 5는 삭제된 글이다.
	want := Report{LastPostNo: 3, LatestPostNo: 6, Checked: 5, Extracted: 3, Unchanged: 1, Missing: 1, Books: 2}
	if r != want {
		t.Errorf("report = %+v, want %+v", r, want)
	}
	if ex.calls != 3 {
		t.Errorf("바뀐 글만 추출해야 한다: %d번 호출", ex.calls)
	}
	if _, ok := st.saved[6]; !ok || len(st.saved[6]) != 0 {
		t.Errorf("거절된 글은 책 없이 저장해야 한다: %+v", st.saved)
	}
}

func TestRunReadsOldPostsWithNewComments(t *testing.T) {
	sticker := post(1, "스티커 댓글만 달린 글")
	sticker.CommentCount = 1
	commented := post(3, "새 댓글이 달린 글")
	commented.CommentCount = 2
	src := &fakeSource{
		latest: 9,
		posts:  map[int]band.Post{1: sticker, 2: post(2, "b"), 3: commented, 4: post(4, "d"), 9: post(9, "새 글")},
		// 최근 댓글 시각의 역순이다. 9는 아직 저장하지 않은 새 글이고, 4는 저장한 댓글 수와 같다.
		cards: []band.Activity{{No: 3, Comments: 2}, {No: 9, Comments: 1}, {No: 1, Comments: 1}, {No: 4, Comments: 3}, {No: 2, Comments: 5}},
	}
	st := &fakeStore{
		last: 8,
		// 3은 댓글 수를 저장하기 전에 저장한 글이다.
		hashes: map[int]string{1: ContentHash(post(1, "스티커 댓글만 달린 글")), 2: "h2", 3: "옛 해시", 4: "h4"},
		counts: map[int]int{1: 0, 2: 4, 4: 3},
		saved:  map[int][]book.Finding{},
	}
	ex := &fakeExtractor{}

	// 최근 글을 다시 확인하지 않아도 오래된 글에 달린 새 댓글을 찾는다.
	r, err := Run(context.Background(), src, ex, st, Options{RecheckDays: 0}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	// 4에서 멈추므로 그 아래의 2는 읽지 않는다. 새 글 9는 댓글 목록과 상관없이 새 글로 읽는다.
	if !slices.Equal(src.opened, []int{3, 1, 9}) || src.read != 4 {
		t.Errorf("열어 본 글 = %v, 읽은 카드 %d장", src.opened, src.read)
	}
	if r.Extracted != 2 || r.Unchanged != 1 || ex.calls != 2 {
		t.Errorf("report = %+v, 추출 %d번", r, ex.calls)
	}
	// 내용이 그대로인 글도 댓글 수는 맞춰서 다음 실행에서 다시 읽지 않는다.
	if st.counts[1] != 1 || st.counts[3] != 2 || st.counts[2] != 4 {
		t.Errorf("댓글 수 = %v", st.counts)
	}

	// 다음 실행에서는 맨 위 카드가 저장한 댓글 수와 같으므로 바로 멈춘다.
	src.opened = nil
	st.last = 9
	if _, err := Run(context.Background(), src, ex, st, Options{}, quiet); err != nil {
		t.Fatal(err)
	}
	if len(src.opened) != 0 || src.read != 1 {
		t.Errorf("열어 본 글 = %v, 읽은 카드 %d장", src.opened, src.read)
	}
}

func TestRunStopsReadingCardsAtTheLimit(t *testing.T) {
	src := &fakeSource{latest: MaxCommentCards + 10, posts: map[int]band.Post{}}
	st := &fakeStore{last: MaxCommentCards + 10, hashes: map[int]string{}, saved: map[int][]book.Finding{}}
	// 댓글 수를 모르는 글만 있으면 상한까지만 읽는다.
	same := post(1, "그대로인 글")
	for no := 1; no <= MaxCommentCards+10; no++ {
		src.posts[no] = same
		st.hashes[no] = ContentHash(same)
		src.cards = append(src.cards, band.Activity{No: no, Comments: 1})
	}
	r, err := Run(context.Background(), src, &fakeExtractor{}, st, Options{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if src.read != MaxCommentCards || r.Checked != MaxCommentCards || r.Unchanged != MaxCommentCards {
		t.Errorf("읽은 카드 %d장, report = %+v", src.read, r)
	}
}

func TestRunProcessesOtherPostsWhenCommentCheckFails(t *testing.T) {
	src := &fakeSource{latest: 2, posts: map[int]band.Post{2: post(2, "새 글")}, cardsErr: errors.New("정렬 버튼이 없다")}
	st := &fakeStore{last: 1, hashes: map[int]string{1: "h1"}, saved: map[int][]book.Finding{}}

	r, err := Run(context.Background(), src, &fakeExtractor{}, st, Options{}, quiet)
	if err == nil || !strings.Contains(err.Error(), "정렬 버튼이 없다") {
		t.Errorf("새 댓글을 찾지 못하면 실행은 실패로 남아야 한다: %v", err)
	}
	if r.Extracted != 1 || st.runs[0].Error == "" {
		t.Errorf("새 글은 처리해야 한다: %+v, 실행 기록 %+v", r, st.runs[0])
	}

	src.cardsErr = band.ErrSessionExpired
	src.opened = nil
	if _, err := Run(context.Background(), src, &fakeExtractor{}, st, Options{}, quiet); !errors.Is(err, band.ErrSessionExpired) || len(src.opened) != 0 {
		t.Errorf("세션이 만료되면 바로 멈춰야 한다: %v, 열어 본 글 %v", err, src.opened)
	}
}

func TestRunRedoFromReextractsSavedPosts(t *testing.T) {
	p1, p2, p3 := post(1, "a"), post(2, "b"), post(3, "c")
	src := &fakeSource{latest: 4, posts: map[int]band.Post{1: p1, 2: p2, 3: p3, 4: post(4, "d")}}
	st := &fakeStore{
		last:   3,
		recent: []int{3},
		hashes: map[int]string{1: ContentHash(p1), 2: ContentHash(p2), 3: ContentHash(p3)},
		saved:  map[int][]book.Finding{},
	}
	ex := &fakeExtractor{}

	r, err := Run(context.Background(), src, ex, st, Options{RedoFrom: 2}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	// 1은 RedoFrom보다 앞이라 건드리지 않고, 2와 3은 내용이 같아도 다시 추출한다. 3은 최근 글과 겹쳐도 한 번만 연다.
	if r.Extracted != 3 || r.Unchanged != 0 || ex.calls != 3 {
		t.Errorf("report = %+v, 추출 %d번", r, ex.calls)
	}
	if got := src.opened; len(got) != 3 || got[0] != 2 || got[1] != 3 || got[2] != 4 {
		t.Errorf("열어 본 글 = %v, want [2 3 4]", got)
	}
}

func TestRunRedoPostsReextractsOnlyThosePosts(t *testing.T) {
	p1, p2, p3 := post(1, "a"), post(2, "b"), post(3, "c")
	src := &fakeSource{latest: 3, posts: map[int]band.Post{1: p1, 2: p2, 3: p3}}
	st := &fakeStore{
		last:   3,
		hashes: map[int]string{1: ContentHash(p1), 2: ContentHash(p2), 3: ContentHash(p3)},
		saved:  map[int][]book.Finding{},
	}
	ex := &fakeExtractor{}

	// 0과 9는 저장된 글 범위 밖이라 무시하고, 3은 두 번 줘도 한 번만 연다.
	r, err := Run(context.Background(), src, ex, st, Options{RedoPosts: []int{3, 1, 3, 0, 9}}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if r.Extracted != 2 || ex.calls != 2 {
		t.Errorf("report = %+v, 추출 %d번", r, ex.calls)
	}
	if got := src.opened; len(got) != 2 || got[0] != 3 || got[1] != 1 {
		t.Errorf("열어 본 글 = %v, want [3 1]", got)
	}
}

func TestRunRecordsRunsAndRetriesFailedPosts(t *testing.T) {
	src := &fakeSource{latest: 4, posts: map[int]band.Post{1: post(1, "a"), 2: post(2, "실패할 글"), 4: post(4, "d")}}
	st := &fakeStore{hashes: map[int]string{}, saved: map[int][]book.Finding{}}
	ex := &fakeExtractor{errs: map[string]error{"실패할 글": errors.New("API 오류")}}

	if _, err := Run(context.Background(), src, ex, st, Options{}, quiet); err == nil {
		t.Fatal("실패한 글이 있으면 오류여야 한다")
	}
	// 2는 추출에 실패했고 3은 화면이 뜨지 않았다. 둘 다 다시 시도할 글로 남는다.
	if st.pending[2] != store.PendingFailed || st.pending[3] != store.PendingMissing {
		t.Errorf("다시 시도할 글 = %v", st.pending)
	}
	first := st.runs[0]
	if first.LastPostNo != 0 || first.LatestPostNo != 4 || first.Extracted != 2 || first.Failed != 1 || first.Missing != 1 || first.Error == "" {
		t.Errorf("첫 실행 기록 = %+v", first)
	}

	// 다음 실행에서는 저장된 마지막 글(4)보다 앞이어도 실패한 글을 다시 처리한다.
	ex.errs = nil
	src.opened = nil
	st.last = 4
	if _, err := Run(context.Background(), src, ex, st, Options{}, quiet); err != nil {
		t.Fatal(err)
	}
	if len(src.opened) != 2 || src.opened[0] != 2 || src.opened[1] != 3 {
		t.Errorf("다시 시도한 글 = %v", src.opened)
	}
	if _, ok := st.pending[2]; ok || st.pending[3] != store.PendingMissing {
		t.Errorf("처리한 글은 지우고 아직 없는 글은 남겨야 한다: %v", st.pending)
	}
	if second := st.runs[1]; second.Extracted != 1 || second.Error != "" {
		t.Errorf("두 번째 실행 기록 = %+v", second)
	}
}

func TestRunStopsWhenSessionExpires(t *testing.T) {
	src := &fakeSource{latest: 3, posts: map[int]band.Post{1: post(1, "a"), 3: post(3, "c")},
		errs: map[int]error{2: band.ErrSessionExpired}}
	st := &fakeStore{hashes: map[int]string{}, saved: map[int][]book.Finding{}}
	_, err := Run(context.Background(), src, &fakeExtractor{}, st, Options{}, quiet)
	if !errors.Is(err, band.ErrSessionExpired) {
		t.Fatalf("err = %v", err)
	}
	if len(src.opened) != 2 {
		t.Errorf("세션이 만료되면 바로 멈춰야 한다: %v", src.opened)
	}
}

func TestRunRespectsLimitAndReportsFailures(t *testing.T) {
	src := &fakeSource{latest: 5, posts: map[int]band.Post{
		1: post(1, "실패할 글"), 2: post(2, "b"), 3: post(3, "c"), 4: post(4, "d"), 5: post(5, "e"),
	}}
	st := &fakeStore{hashes: map[int]string{}, saved: map[int][]book.Finding{}}
	ex := &fakeExtractor{errs: map[string]error{"실패할 글": errors.New("API 오류")}}

	r, err := Run(context.Background(), src, ex, st, Options{Limit: 3}, quiet)
	if err == nil {
		t.Error("실패한 글이 있으면 오류여야 한다")
	}
	if r.Extracted != 2 || r.Failed != 1 || len(src.opened) != 3 {
		t.Errorf("추출 상한 3에서 멈춰야 한다: %+v, 열어 본 글 %v", r, src.opened)
	}
	if _, ok := st.saved[1]; ok {
		t.Error("실패한 글은 저장하지 않아야 다음에 다시 처리한다")
	}
}

func TestContentHashChangesWithContent(t *testing.T) {
	a := post(1, "본문")
	b := a
	b.Links = []book.Link{{URL: "https://example.com", Title: "링크"}}
	c := a
	c.Images = []band.Image{{MediaType: "image/jpeg", Data: []byte{1}}}
	d := a
	d.Comments = []band.Comment{{Text: "새 댓글"}}
	if ContentHash(a) == ContentHash(b) || ContentHash(a) == ContentHash(c) || ContentHash(a) == ContentHash(d) {
		t.Error("링크, 사진, 댓글이 바뀌면 해시가 달라야 한다")
	}
	if ContentHash(a) != ContentHash(post(1, "본문")) {
		t.Error("같은 내용이면 해시가 같아야 한다")
	}
}

// 해시가 바뀌면 저장한 글이 모두 바뀐 글로 보여서 다시 추출된다. 계산 방식을 바꾸지 않게 고정값과 비교한다.
func TestContentHashIsStable(t *testing.T) {
	at := time.Date(2026, 10, 21, 1, 24, 0, 0, time.UTC)
	p := band.Post{
		Text:   "본문",
		Links:  []book.Link{{URL: "https://example.com/a", Title: "기사"}},
		Images: []band.Image{{MediaType: "image/png", Data: []byte("사진")}},
		Comments: []band.Comment{
			{At: at, Text: "댓글", Links: []book.Link{{URL: "https://example.com/b", Title: ""}}},
			{At: at.Add(time.Hour), Text: "다른 댓글"},
		},
	}
	if got, want := ContentHash(p), "a1fff221f9b9f79d1b882e7de91fc4d21135192bc0881f1f2a3afea030fef566"; got != want {
		t.Errorf("ContentHash = %s, want %s", got, want)
	}
}
