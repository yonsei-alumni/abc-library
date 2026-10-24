//go:build integration

package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yonsei-alumni/abc-library/internal/book"
)

func tagNames(t *testing.T, s *Store) string {
	t.Helper()
	tags, err := s.Tags(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(tags.Names(), ",")
}

func bookTags(t *testing.T, s *Store, id int64) string {
	t.Helper()
	b, _, err := s.Book(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(b.Tags, ",")
}

func onlyBook(t *testing.T, s *Store) book.Shelved {
	t.Helper()
	shelf, err := s.Books(context.Background(), Filter{}, 10, 0)
	if err != nil || len(shelf) != 1 {
		t.Fatalf("책은 한 권이어야 한다: %+v, %v", shelf, err)
	}
	return shelf[0]
}

func summaries(ms []book.Mention) string {
	var ss []string
	for _, m := range ms {
		ss = append(ss, m.Summary)
	}
	return strings.Join(ss, ",")
}

func TestTagAdmin(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	const seeded = "AI,SF,과학,소설,추리·미스터리,판타지,시,에세이,인문,철학,역사,사회·정치,경제·경영,자기계발,기술·IT,예술,여행,종교,어린이·청소년,만화"
	if got := tagNames(t, s); got != seeded {
		t.Fatalf("처음 태그 = %s", got)
	}

	// 모임 주제는 맨 앞에, 갈래는 맨 뒤에 더한다.
	if err := s.AddTag(ctx, book.Tag{Name: "기후", Kind: book.TopicTag}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTag(ctx, book.Tag{Name: "요리", Kind: book.KindTag}); err != nil {
		t.Fatal(err)
	}
	if got := tagNames(t, s); got != "기후,"+seeded+",요리" {
		t.Errorf("더한 뒤 = %s", got)
	}
	if err := s.AddTag(ctx, book.Tag{Name: "SF", Kind: book.KindTag}); !errors.Is(err, ErrConflict) {
		t.Errorf("같은 이름 = %v", err)
	}

	// 한 칸씩 옮기고, 끝에서는 그대로 둔다. 다른 갈래로 넘어가지 않는다.
	for _, mv := range []struct {
		name string
		up   bool
	}{{"과학", true}, {"기후", true}, {"과학", false}, {"과학", false}, {"소설", true}} {
		if err := s.MoveTag(ctx, mv.name, mv.up); err != nil {
			t.Fatal(err)
		}
	}
	if got := tagNames(t, s); !strings.HasPrefix(got, "기후,AI,SF,과학,소설,추리") {
		t.Errorf("옮긴 뒤 = %s", got)
	}
	if err := s.MoveTag(ctx, "없는 태그", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("없는 태그 옮기기 = %v", err)
	}

	err := s.SavePost(ctx, book.Post{No: 1, PostedAt: day(1), ContentHash: "h"}, []book.Finding{{
		Book:     book.Book{Title: "삼체", Authors: []string{"류츠신"}, Tags: []string{"SF", "소설", "목록에 없는 태그"}},
		Mentions: []book.Mention{mention(book.InPost, 1, "요약")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	id := onlyBook(t, s).Book.ID
	if got := bookTags(t, s, id); got != "SF,소설" {
		t.Errorf("목록에 없는 태그는 붙지 않아야 한다: %s", got)
	}

	// 이름을 바꾸면 책의 태그도 바뀌고, 지우면 책에서도 빠진다.
	if err := s.RenameTag(ctx, "SF", "공상과학"); err != nil {
		t.Fatal(err)
	}
	if got := bookTags(t, s, id); got != "공상과학,소설" {
		t.Errorf("이름을 바꾼 뒤 = %s", got)
	}
	if err := s.RenameTag(ctx, "공상과학", "소설"); !errors.Is(err, ErrConflict) {
		t.Errorf("있는 이름으로 바꾸기 = %v", err)
	}
	if err := s.RenameTag(ctx, "없는 태그", "새 이름"); !errors.Is(err, ErrNotFound) {
		t.Errorf("없는 태그 바꾸기 = %v", err)
	}
	if err := s.DeleteTag(ctx, "소설"); err != nil {
		t.Fatal(err)
	}
	if got := bookTags(t, s, id); got != "공상과학" {
		t.Errorf("지운 뒤 = %s", got)
	}
	if err := s.DeleteTag(ctx, "소설"); !errors.Is(err, ErrNotFound) {
		t.Errorf("이미 지운 태그 = %v", err)
	}
}

func TestAdminEditsSurviveReextraction(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	post := book.Post{No: 1, PostedAt: day(1), ContentHash: "h"}
	claude := []book.Finding{{
		Book: book.Book{ISBN13: "9791196914806", Title: "당신 인생의 이야기", Authors: []string{"테드 창"}, Tags: []string{"SF", "소설"}},
		Mentions: []book.Mention{
			mention(book.InPost, 1, "본문 요약"),
			mention(book.InComment, 2, "댓글 요약"),
			mention(book.InComment, 3, "다른 댓글 요약"),
		},
	}}
	if err := s.SavePost(ctx, post, claude); err != nil {
		t.Fatal(err)
	}
	id := onlyBook(t, s).Book.ID
	_, ms, _ := s.Book(ctx, id)

	edited := book.Book{ID: id, ISBN13: "9788956057842", Title: "Stories of Your Life", Authors: []string{"Ted Chiang"}, Publisher: "Vintage", Tags: []string{"AI"}}
	if err := s.UpdateBook(ctx, edited); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateMention(ctx, ms[0].ID, "고친 요약", []book.Link{{URL: "https://example.com/r", Title: "서평"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMention(ctx, ms[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMention(ctx, ms[1].ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("지운 언급을 또 지우기 = %v", err)
	}
	if _, err := s.Mention(ctx, ms[1].ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("지운 언급 = %v", err)
	}

	// 배치가 같은 글을 다시 추출해도(여러 번) 관리자가 고친 책 정보, 태그, 언급은 그대로다.
	for range 2 {
		if err := s.SavePost(ctx, post, claude); err != nil {
			t.Fatal(err)
		}
	}
	sh := onlyBook(t, s)
	b, ms, err := s.Book(ctx, sh.Book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b.ID != id || b.Title != edited.Title || b.ISBN13 != edited.ISBN13 || b.Publisher != "Vintage" || strings.Join(b.Tags, ",") != "AI" {
		t.Errorf("고친 책이 바뀌었다: %+v", b)
	}
	if got := summaries(ms); got != "고친 요약,다른 댓글 요약" {
		t.Errorf("언급 = %s", got)
	}
	if len(ms[0].Links) != 1 || ms[0].Links[0].Title != "서평" {
		t.Errorf("고친 링크 = %+v", ms[0].Links)
	}

	// 새 댓글은 그대로 더해진다. 예전 ISBN이나 새 ISBN으로 찾아도 같은 책이다.
	claude[0].Mentions = append(claude[0].Mentions, mention(book.InComment, 4, "새 댓글"))
	if err := s.SavePost(ctx, post, claude); err != nil {
		t.Fatal(err)
	}
	other := book.Book{ISBN13: "9788956057842", Title: "전혀 다른 표기", Tags: []string{"소설"}}
	if err := s.SavePost(ctx, book.Post{No: 2, PostedAt: day(5), ContentHash: "h"}, []book.Finding{{Book: other, Mentions: []book.Mention{mention(book.InPost, 5, "둘째 글")}}}); err != nil {
		t.Fatal(err)
	}
	sh = onlyBook(t, s)
	_, ms, _ = s.Book(ctx, sh.Book.ID)
	if sh.Book.ID != id || summaries(ms) != "고친 요약,다른 댓글 요약,새 댓글,둘째 글" || strings.Join(sh.Book.Tags, ",") != "AI" {
		t.Errorf("책 = %+v, 언급 = %s", sh.Book, summaries(ms))
	}
}

func TestUpdateBookRejectsDuplicates(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	err := s.SavePost(ctx, book.Post{No: 1, PostedAt: day(1), ContentHash: "h"}, []book.Finding{
		{Book: book.Book{ISBN13: "9791196914806", Title: "당신 인생의 이야기", Authors: []string{"테드 창"}}, Mentions: []book.Mention{mention(book.InPost, 1, "a")}},
		{Book: book.Book{Title: "숨", Authors: []string{"테드 창"}}, Mentions: []book.Mention{mention(book.InComment, 2, "b")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	shelf, _ := s.Books(ctx, Filter{}, 10, 0)
	ids := map[string]int64{}
	for _, sh := range shelf {
		ids[sh.Book.Title] = sh.Book.ID
	}
	story, breath := ids["당신 인생의 이야기"], ids["숨"]

	var dup *DuplicateError
	if err := s.UpdateBook(ctx, book.Book{ID: breath, Title: "숨", Authors: []string{"테드 창"}, ISBN13: "9791196914806"}); !errors.As(err, &dup) || dup.BookID != story {
		t.Errorf("ISBN이 겹치면 DuplicateError여야 한다: %v", err)
	}
	if err := s.UpdateBook(ctx, book.Book{ID: breath, Title: "당신 인생의 이야기 (양장)", Authors: []string{"테드 창"}}); !errors.As(err, &dup) || dup.BookID != story {
		t.Errorf("작품 키가 겹치면 DuplicateError여야 한다: %v", err)
	}
	if err := s.UpdateBook(ctx, book.Book{ID: 999999, Title: "없는 책"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("없는 책 = %v", err)
	}
	// 자기 자신의 ISBN과 제목은 겹치는 것이 아니다.
	if err := s.UpdateBook(ctx, book.Book{ID: story, ISBN13: "9791196914806", Title: "당신 인생의 이야기", Authors: []string{"테드 창"}}); err != nil {
		t.Errorf("그대로 저장 = %v", err)
	}
}

func TestMoveMentionMergeAndDeleteBook(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	post := book.Post{No: 1, PostedAt: day(1), ContentHash: "h"}
	// Claude가 '숨'에 대한 댓글을 '당신 인생의 이야기'에 붙였다.
	claude := []book.Finding{
		{Book: book.Book{Title: "당신 인생의 이야기", Authors: []string{"테드 창"}}, Mentions: []book.Mention{mention(book.InPost, 1, "본문"), mention(book.InComment, 2, "숨 이야기")}},
		{Book: book.Book{Title: "숨", Authors: []string{"테드 창"}}, Mentions: []book.Mention{mention(book.InComment, 3, "숨 댓글")}},
	}
	if err := s.SavePost(ctx, post, claude); err != nil {
		t.Fatal(err)
	}
	shelf, _ := s.Books(ctx, Filter{}, 10, 0)
	ids := map[string]int64{}
	for _, sh := range shelf {
		ids[sh.Book.Title] = sh.Book.ID
	}
	story, breath := ids["당신 인생의 이야기"], ids["숨"]
	_, ms, _ := s.Book(ctx, story)

	if err := s.MoveMention(ctx, ms[1].ID, breath); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveMention(ctx, ms[0].ID, 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("없는 책으로 옮기기 = %v", err)
	}
	check := func(when string) {
		t.Helper()
		_, a, _ := s.Book(ctx, story)
		_, b, _ := s.Book(ctx, breath)
		if summaries(a) != "본문" || summaries(b) != "숨 이야기,숨 댓글" {
			t.Errorf("%s: 당신 인생의 이야기 = %s, 숨 = %s", when, summaries(a), summaries(b))
		}
	}
	check("옮긴 뒤")
	if err := s.SavePost(ctx, post, claude); err != nil {
		t.Fatal(err)
	}
	check("다시 추출한 뒤")

	// 같은 책을 다른 제목으로 뽑은 책을 합치면, 다시 추출해도 합친 책으로 모인다.
	dupFinding := book.Finding{Book: book.Book{ISBN13: "9788932917245", Title: "숨 (리커버)", Authors: []string{"Ted Chiang"}, Publisher: "엘리", Tags: []string{"SF"}},
		Mentions: []book.Mention{mention(book.InPost, 6, "둘째 글 본문")}}
	post2 := book.Post{No: 2, PostedAt: day(6), ContentHash: "h"}
	if err := s.SavePost(ctx, post2, []book.Finding{dupFinding}); err != nil {
		t.Fatal(err)
	}
	shelf, _ = s.Books(ctx, Filter{}, 10, 0)
	var dupID int64
	for _, sh := range shelf {
		if sh.Book.Title == "숨 (리커버)" {
			dupID = sh.Book.ID
		}
	}
	if dupID == 0 || dupID == breath {
		t.Fatalf("다른 책으로 저장되어야 한다: %+v", shelf)
	}
	if err := s.MergeBook(ctx, dupID, breath); err != nil {
		t.Fatal(err)
	}
	if err := s.MergeBook(ctx, breath, breath); err == nil {
		t.Error("같은 책끼리 합치면 오류여야 한다")
	}
	if err := s.MergeBook(ctx, dupID, breath); !errors.Is(err, ErrNotFound) {
		t.Errorf("이미 합친 책 = %v", err)
	}
	if err := s.SavePost(ctx, post2, []book.Finding{dupFinding}); err != nil {
		t.Fatal(err)
	}
	b, bm, _ := s.Book(ctx, breath)
	if b.ISBN13 != "9788932917245" || b.Publisher != "엘리" || b.Title != "숨" || summaries(bm) != "숨 이야기,숨 댓글,둘째 글 본문" || strings.Join(b.Tags, ",") != "SF" {
		t.Errorf("합친 책 = %+v, %s", b, summaries(bm))
	}
	if shelf, _ := s.Books(ctx, Filter{}, 10, 0); len(shelf) != 2 {
		t.Errorf("합친 뒤 책 수 = %d", len(shelf))
	}

	// 지운 책은 목록에서 사라지고 다시 추출해도 돌아오지 않는다. 다른 글에서 새로 언급되면 다시 나타난다.
	if err := s.DeleteBook(ctx, story); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePost(ctx, post, claude); err != nil {
		t.Fatal(err)
	}
	if shelf, _ := s.Books(ctx, Filter{}, 10, 0); len(shelf) != 1 || shelf[0].Book.ID != breath {
		t.Errorf("지운 책이 목록에 있다: %+v", shelf)
	}
	if b, ms, err := s.Book(ctx, story); err != nil || b.ID != story || len(ms) != 0 {
		t.Errorf("지운 책은 관리자가 볼 수 있게 남아야 한다: %+v %+v %v", b, ms, err)
	}
	if err := s.SavePost(ctx, book.Post{No: 3, PostedAt: day(8), ContentHash: "h"}, []book.Finding{{Book: claude[0].Book, Mentions: []book.Mention{mention(book.InPost, 8, "새 글")}}}); err != nil {
		t.Fatal(err)
	}
	if _, ms, _ := s.Book(ctx, story); summaries(ms) != "새 글" {
		t.Errorf("새로 언급되면 다시 나타나야 한다: %s", summaries(ms))
	}
	if err := s.DeleteBook(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("없는 책 지우기 = %v", err)
	}
}

func TestAdmins(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if ok, err := s.IsAdmin(ctx, "BeneLog@Gmail.com"); err != nil || !ok {
		t.Fatalf("처음 관리자는 benelog@gmail.com이다: %v %v", ok, err)
	}
	// 마지막 관리자는 지울 수 없다.
	if err := s.DeleteAdmin(ctx, "benelog@gmail.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("마지막 관리자 지우기 = %v", err)
	}
	if err := s.AddAdmin(ctx, "New@Example.com", "benelog@gmail.com"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAdmin(ctx, "new@example.com", "benelog@gmail.com"); !errors.Is(err, ErrConflict) {
		t.Errorf("같은 관리자 = %v", err)
	}
	admins, err := s.Admins(ctx)
	if err != nil || len(admins) != 2 || admins[1].Email != "new@example.com" || admins[1].AddedBy != "benelog@gmail.com" {
		t.Fatalf("관리자 = %+v, %v", admins, err)
	}
	if err := s.DeleteAdmin(ctx, "NEW@example.com"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.IsAdmin(ctx, "new@example.com"); ok {
		t.Error("지운 관리자는 관리자가 아니어야 한다")
	}
	if err := s.DeleteAdmin(ctx, "gone@example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("없는 관리자 = %v", err)
	}
}
