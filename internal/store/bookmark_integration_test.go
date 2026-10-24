//go:build integration

package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/yonsei-alumni/abc-library/internal/book"
)

// saveBooks는 게시글 하나에 책을 한 권씩 저장하고 책 번호를 돌려준다.
func saveBooks(t *testing.T, s *Store, titles ...string) []int64 {
	t.Helper()
	ctx := context.Background()
	var ids []int64
	for i, title := range titles {
		no := 100 + i
		err := s.SavePost(ctx, book.Post{No: no, PostedAt: day(1), ContentHash: title}, []book.Finding{{
			Book: book.Book{Title: title, Authors: []string{"저자 " + title}}, Mentions: []book.Mention{mention(book.InPost, 1, title+" 요약")},
		}})
		if err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := s.db.NewRaw(`SELECT id FROM books WHERE title = ?`, title).Scan(ctx, &id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func bookIDs(marks []book.Bookmark) []int64 {
	var ids []int64
	for _, m := range marks {
		ids = append(ids, m.Book.ID)
	}
	return ids
}

func TestUsers(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	a, err := s.UserID(ctx, "hash-a")
	if err != nil {
		t.Fatal(err)
	}
	if again, err := s.UserID(ctx, "hash-a"); err != nil || again != a {
		t.Errorf("같은 값은 같은 사용자여야 한다: %d, %d, %v", a, again, err)
	}
	b, err := s.UserID(ctx, "hash-b")
	if err != nil || b == a {
		t.Fatalf("다른 값은 다른 사용자여야 한다: %d, %d, %v", a, b, err)
	}
	if ok, err := s.UserExists(ctx, a); !ok || err != nil {
		t.Errorf("UserExists = %v, %v", ok, err)
	}
	if err := s.DeleteUser(ctx, a); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.UserExists(ctx, a); ok {
		t.Error("지운 사용자가 남았다")
	}
	if err := s.DeleteUser(ctx, a); !errors.Is(err, ErrNotFound) {
		t.Errorf("없는 사용자 지우기 = %v", err)
	}
}

func TestCategoriesAndBookmarks(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	books := saveBooks(t, s, "첫째 책", "둘째 책", "셋째 책")
	me, _ := s.UserID(ctx, "hash-me")
	other, _ := s.UserID(ctx, "hash-other")

	sf, err := s.AddCategory(ctx, me, book.Category{Title: "SF 모음", Body: "# 좋아하는 SF", PublicTitle: "추천 SF"})
	if err != nil {
		t.Fatal(err)
	}
	later, err := s.AddCategory(ctx, me, book.Category{Title: "나중에"})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := s.AddCategory(ctx, other, book.Category{Title: "남의 카테고리"})
	if err != nil {
		t.Fatal(err)
	}

	c, err := s.Category(ctx, me, sf)
	if err != nil || c.Title != "SF 모음" || c.Body != "# 좋아하는 SF" || c.PublicTitle != "추천 SF" || c.Public || !book.ValidCode(c.Code) || c.UserID != me {
		t.Fatalf("카테고리 = %+v, %v", c, err)
	}
	if _, err := s.Category(ctx, me, theirs); !errors.Is(err, ErrNotFound) {
		t.Errorf("남의 카테고리 읽기 = %v", err)
	}

	// 다른 사용자의 카테고리 번호는 버린다.
	if err := s.SetBookmark(ctx, me, books[0], []int64{sf, later, theirs}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBookmark(ctx, me, books[1], []int64{sf}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBookmark(ctx, me, books[2], nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBookmark(ctx, me, 999999, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("없는 책 북마크 = %v", err)
	}
	ok, cats, err := s.Bookmark(ctx, me, books[0])
	if err != nil || !ok || !slices.Equal(cats, []int64{sf, later}) {
		t.Errorf("첫째 책 북마크 = %v %v %v", ok, cats, err)
	}
	if ok, _, _ := s.Bookmark(ctx, other, books[0]); ok {
		t.Error("다른 사용자에게는 북마크가 없어야 한다")
	}

	// 카테고리를 바꾸면 빠진 것만 지운다.
	if err := s.SetBookmark(ctx, me, books[0], []int64{later}); err != nil {
		t.Fatal(err)
	}
	if _, cats, _ := s.Bookmark(ctx, me, books[0]); !slices.Equal(cats, []int64{later}) {
		t.Errorf("바꾼 카테고리 = %v", cats)
	}

	marks, err := s.Bookmarks(ctx, me)
	if err != nil || len(marks) != 3 {
		t.Fatalf("북마크 = %+v, %v", marks, err)
	}
	cs, err := s.Categories(ctx, me)
	if err != nil || len(cs) != 2 || cs[0].ID != sf || cs[0].Books != 1 || cs[1].Books != 1 {
		t.Errorf("카테고리 목록 = %+v, %v", cs, err)
	}
	if got, _ := s.CategoryBooks(ctx, sf); len(got) != 1 || got[0].ID != books[1] {
		t.Errorf("SF 모음의 책 = %+v", got)
	}

	// 공개 주소의 코드로 찾는다. 공개 여부는 부르는 쪽이 본다.
	c.Public, c.Title = true, "SF 모음(고침)"
	if err := s.UpdateCategory(ctx, me, c); err != nil {
		t.Fatal(err)
	}
	pub, pubBooks, err := s.CategoryByCode(ctx, c.Code)
	if err != nil || !pub.Public || pub.Title != "SF 모음(고침)" || pub.Code != c.Code || len(pubBooks) != 1 {
		t.Errorf("코드로 찾은 카테고리 = %+v %+v %v", pub, pubBooks, err)
	}
	if pubs, err := s.PublicCategories(ctx); err != nil || len(pubs) != 1 || pubs[0].ID != sf || pubs[0].Books != 1 {
		t.Errorf("공개 카테고리 목록 = %+v, %v", pubs, err)
	}
	if _, _, err := s.CategoryByCode(ctx, "zzzzzzzz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("없는 코드 = %v", err)
	}
	c.ID = theirs
	if err := s.UpdateCategory(ctx, me, c); !errors.Is(err, ErrNotFound) {
		t.Errorf("남의 카테고리 고치기 = %v", err)
	}
	if err := s.DeleteCategory(ctx, me, theirs); !errors.Is(err, ErrNotFound) {
		t.Errorf("남의 카테고리 지우기 = %v", err)
	}

	// 관리자가 언급을 모두 지운 책은 북마크 목록과 카테고리에서 보이지 않는다.
	if err := s.DeleteBook(ctx, books[1]); err != nil {
		t.Fatal(err)
	}
	if marks, _ := s.Bookmarks(ctx, me); slices.Contains(bookIDs(marks), books[1]) {
		t.Error("언급을 모두 지운 책이 북마크 목록에 보인다")
	}
	if cs, _ := s.Categories(ctx, me); cs[0].Books != 0 {
		t.Errorf("언급을 모두 지운 책을 셌다: %+v", cs[0])
	}

	// 카테고리를 지워도 북마크는 남고, 북마크를 지우면 카테고리에서도 빠진다.
	if err := s.DeleteCategory(ctx, me, sf); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := s.Bookmark(ctx, me, books[1]); !ok {
		t.Error("카테고리를 지우면 북마크가 남아야 한다")
	}
	if err := s.DeleteBookmark(ctx, me, books[0]); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.CategoryBooks(ctx, later); len(got) != 0 {
		t.Errorf("지운 북마크가 카테고리에 남았다: %+v", got)
	}
	if err := s.DeleteBookmark(ctx, me, books[0]); !errors.Is(err, ErrNotFound) {
		t.Errorf("없는 북마크 지우기 = %v", err)
	}

	// 사용자를 지우면 북마크와 카테고리도 지워진다.
	if err := s.DeleteUser(ctx, me); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := s.db.NewRaw(`SELECT (SELECT count(*) FROM bookmarks WHERE user_id = ?) + (SELECT count(*) FROM bookmark_categories WHERE user_id = ?)`, me, me).Scan(ctx, &left); err != nil || left != 0 {
		t.Errorf("지운 사용자의 데이터 %d개가 남았다: %v", left, err)
	}
	if _, err := s.Category(ctx, other, theirs); err != nil {
		t.Errorf("다른 사용자의 카테고리는 남아야 한다: %v", err)
	}
}

func TestCategoryLimit(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	me, _ := s.UserID(ctx, "hash-me")
	for i := range book.MaxCategories {
		if _, err := s.AddCategory(ctx, me, book.Category{Title: fmt.Sprintf("카테고리 %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddCategory(ctx, me, book.Category{Title: "하나 더"}); !errors.Is(err, ErrTooMany) {
		t.Errorf("한도를 넘은 카테고리 = %v", err)
	}
	other, _ := s.UserID(ctx, "hash-other")
	if _, err := s.AddCategory(ctx, other, book.Category{Title: "다른 사람"}); err != nil {
		t.Errorf("한도는 사용자마다 따로다: %v", err)
	}
}

func TestBookmarksSurviveBatchAndMerge(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	books := saveBooks(t, s, "잘못 뽑은 책", "합칠 책", "남길 책")
	me, _ := s.UserID(ctx, "hash-me")
	cat, _ := s.AddCategory(ctx, me, book.Category{Title: "모음"})
	for _, id := range books {
		if err := s.SetBookmark(ctx, me, id, []int64{cat}); err != nil {
			t.Fatal(err)
		}
	}

	// 배치가 다시 추출해서 책이 어느 언급에도 남지 않아도 북마크한 책은 지우지 않는다.
	if err := s.SavePost(ctx, book.Post{No: 100, PostedAt: day(1), ContentHash: "바뀐 내용"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Book(ctx, books[0]); err != nil {
		t.Errorf("북마크한 책이 지워졌다: %v", err)
	}

	// 합치면 북마크와 카테고리가 합친 책으로 옮겨 간다. 두 책을 모두 북마크했어도 하나가 된다.
	if err := s.MergeBook(ctx, books[1], books[2]); err != nil {
		t.Fatal(err)
	}
	marks, err := s.Bookmarks(ctx, me)
	if err != nil || !slices.Equal(bookIDs(marks), []int64{books[2]}) || !slices.Equal(marks[0].Categories, []int64{cat}) {
		t.Errorf("합친 뒤 북마크 = %+v, %v", marks, err)
	}
	if got, _ := s.CategoryBooks(ctx, cat); len(got) != 1 || got[0].ID != books[2] {
		t.Errorf("합친 뒤 카테고리의 책 = %+v", got)
	}
}

func TestLowercaseCodesMigration(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	me, _ := s.UserID(ctx, "hash-me")
	for i, code := range []string{"Ab3dE5gH", "Zz9", "abc"} {
		if _, err := s.db.NewRaw(`INSERT INTO bookmark_categories (user_id, title, code) VALUES (?, ?, ?)`, me, fmt.Sprint(i), code).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	sql, err := migrationFiles.ReadFile("migrations/20261025000001_lowercase_codes.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	cs, err := s.Categories(ctx, me)
	if err != nil || len(cs) != 3 {
		t.Fatalf("카테고리 = %+v, %v", cs, err)
	}
	seen := map[string]bool{}
	for _, c := range cs {
		if len(c.Code) != book.CodeLen || !book.ValidCode(c.Code) || seen[c.Code] {
			t.Errorf("다시 뽑은 코드 %q는 겹치지 않는 영어 소문자 %d자여야 한다", c.Code, book.CodeLen)
		}
		seen[c.Code] = true
	}
}
