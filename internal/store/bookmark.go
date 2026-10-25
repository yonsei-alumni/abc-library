package store

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"

	"github.com/yonsei-alumni/abc-library/internal/book"
)

// ErrTooMany는 사용자 한 명이 만들 수 있는 수를 넘었다는 뜻이다.
var ErrTooMany = errors.New("더 만들 수 없습니다")

// UserID는 Google 계정 고유 번호의 HMAC 값으로 사용자를 찾고, 없으면 만들어서 번호를 돌려준다.
func (s *Store) UserID(ctx context.Context, subjectHash string) (int64, error) {
	var id int64
	err := s.db.NewRaw(`
		INSERT INTO users (subject_hash) VALUES (?)
		ON CONFLICT (subject_hash) DO UPDATE SET subject_hash = EXCLUDED.subject_hash
		RETURNING id`, subjectHash).Scan(ctx, &id)
	return id, err
}

// UserExists는 그 번호의 사용자가 있는지 알려 준다.
func (s *Store) UserExists(ctx context.Context, id int64) (bool, error) {
	return s.db.NewSelect().Table("users").Where("id = ?", id).Exists(ctx)
}

// DeleteUser는 사용자와 그 사용자의 북마크, 카테고리를 모두 지운다.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	return notFoundIfNone(s.db.NewRaw(`DELETE FROM users WHERE id = ?`, id).Exec(ctx))
}

type categoryRow struct {
	bun.BaseModel `bun:"table:bookmark_categories"`

	ID          int64     `bun:",pk,autoincrement"`
	UserID      int64     `bun:",notnull"`
	Title       string    `bun:",notnull"`
	Body        string    `bun:",notnull"`
	PublicTitle string    `bun:",notnull"`
	Public      bool      `bun:",notnull"`
	Code        string    `bun:",notnull"`
	UpdatedAt   time.Time `bun:",nullzero,notnull,default:current_timestamp"`
}

type categoryView struct {
	ID          int64  `bun:"id"`
	UserID      int64  `bun:"user_id"`
	Title       string `bun:"title"`
	Body        string `bun:"body"`
	PublicTitle string `bun:"public_title"`
	Public      bool   `bun:"public"`
	Code        string `bun:"code"`
	Books       int    `bun:"books"`
}

func (v categoryView) toCategory() book.Category {
	return book.Category{
		ID: v.ID, UserID: v.UserID, Title: v.Title, Body: v.Body, PublicTitle: v.PublicTitle,
		Public: v.Public, Code: v.Code, Books: v.Books,
	}
}

// categoryColumns는 categoryView로 읽을 카테고리 컬럼이다. 카테고리의 별칭은 c다.
const categoryColumns = `c.id, c.user_id, c.title, c.body, c.public_title, c.public, c.code,
	(SELECT count(*) FROM bookmark_category_books cb JOIN books b ON b.id = cb.book_id
	 WHERE cb.category_id = c.id AND ` + visibleBook + `) AS books`

// categoryOrder는 카테고리를 사용자가 정한 순서로 늘어놓는다. 순서가 같으면 만든 순서를 따른다.
const categoryOrder = `c.position, c.created_at, c.id`

func (s *Store) categories(ctx context.Context, where, order string, args ...any) ([]book.Category, error) {
	var views []categoryView
	err := s.db.NewRaw(`SELECT `+categoryColumns+` FROM bookmark_categories c WHERE `+where+` ORDER BY `+order, args...).Scan(ctx, &views)
	return convert(views, categoryView.toCategory), err
}

// Categories는 사용자의 카테고리를 사용자가 정한 순서로 돌려준다.
func (s *Store) Categories(ctx context.Context, userID int64) ([]book.Category, error) {
	return s.categories(ctx, `c.user_id = ?`, categoryOrder, userID)
}

// PublicCategories는 모든 사용자의 공개한 카테고리를 최근에 고친 순서로 돌려준다.
func (s *Store) PublicCategories(ctx context.Context) ([]book.Category, error) {
	return s.categories(ctx, `c.public`, `c.updated_at DESC, c.id DESC`)
}

// Category는 사용자의 카테고리 하나다. 다른 사용자의 카테고리는 ErrNotFound다.
func (s *Store) Category(ctx context.Context, userID, id int64) (book.Category, error) {
	cs, err := s.categories(ctx, `c.user_id = ? AND c.id = ?`, categoryOrder, userID, id)
	if err != nil {
		return book.Category{}, err
	}
	if len(cs) == 0 {
		return book.Category{}, ErrNotFound
	}
	return cs[0], nil
}

// CategoryByCode는 공개 주소의 코드로 카테고리와 그 책을 사용자가 그 카테고리에서 정한 순서로 돌려준다.
// 공개하지 않은 카테고리도 돌려주므로 보여 줄지는 부르는 쪽이 정한다.
func (s *Store) CategoryByCode(ctx context.Context, code string) (book.Category, []book.Book, error) {
	cs, err := s.categories(ctx, `c.code = ?`, categoryOrder, code)
	if err != nil {
		return book.Category{}, nil, err
	}
	if len(cs) == 0 {
		return book.Category{}, nil, ErrNotFound
	}
	books, err := s.CategoryBooks(ctx, cs[0].ID)
	return cs[0], books, err
}

// categoryBookOrder는 카테고리(별칭 cb)에 넣은 책(별칭 b)을 사용자가 그 카테고리에서 정한 순서로 둔다.
// 순서가 같으면 최근에 넣은 책이 먼저 온다. 카테고리 화면과 공개 화면이 이 순서를 따른다.
const categoryBookOrder = `cb.position, cb.created_at DESC, b.id`

// CategoryBooks는 카테고리에 넣은 책을 사용자가 그 카테고리에서 정한 순서로 돌려준다.
func (s *Store) CategoryBooks(ctx context.Context, categoryID int64) ([]book.Book, error) {
	var views []bookView
	err := s.db.NewRaw(`
		SELECT `+bookColumns+`
		FROM bookmark_category_books cb JOIN books b ON b.id = cb.book_id
		WHERE cb.category_id = ? AND `+visibleBook+`
		ORDER BY `+categoryBookOrder, categoryID).Scan(ctx, &views)
	return convert(views, bookView.toBook), err
}

// AddCategory는 카테고리를 맨 뒤에 만들고 번호를 돌려준다. 공개 주소의 코드는 새로 뽑는다.
// 사용자가 이미 book.MaxCategories개를 만들었으면 ErrTooMany다.
func (s *Store) AddCategory(ctx context.Context, userID int64, c book.Category) (int64, error) {
	n, err := s.db.NewSelect().Model((*categoryRow)(nil)).Where("user_id = ?", userID).Count(ctx)
	if err != nil {
		return 0, err
	}
	if n >= book.MaxCategories {
		return 0, ErrTooMany
	}
	// 코드가 다른 카테고리와 겹치면 다시 뽑는다. 열 번 겹치면 짧은 코드가 거의 찼다는 뜻이므로 한 글자씩 늘린다.
	for i := range 30 {
		code := book.NewCode(book.CodeLen + i/10)
		row := &categoryRow{UserID: userID, Title: c.Title, Body: c.Body, PublicTitle: c.PublicTitle, Public: c.Public, Code: code}
		_, err = s.db.NewInsert().Model(row).
			Value("position", "(SELECT coalesce(max(position), 0) + 1 FROM bookmark_categories WHERE user_id = ?)", userID).
			Returning("id").Exec(ctx)
		if !isUniqueViolation(err) {
			return row.ID, err
		}
	}
	return 0, err
}

// UpdateCategory는 사용자의 카테고리를 고친다. 공개 주소의 코드는 그대로 둔다.
func (s *Store) UpdateCategory(ctx context.Context, userID int64, c book.Category) error {
	return notFoundIfNone(s.db.NewUpdate().Model((*categoryRow)(nil)).
		Set("title = ?", c.Title).
		Set("body = ?", c.Body).
		Set("public_title = ?", c.PublicTitle).
		Set("public = ?", c.Public).
		Set("updated_at = now()").
		Where("id = ? AND user_id = ?", c.ID, userID).
		Exec(ctx))
}

// MoveCategory는 사용자의 카테고리를 한 칸 앞(up)이나 뒤로 옮긴다. 이미 끝에 있으면 그대로 둔다.
// 사용자의 카테고리 순서를 1부터 다시 매긴다. 다른 사용자의 카테고리는 ErrNotFound다.
func (s *Store) MoveCategory(ctx context.Context, userID, id int64, up bool) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var ids []int64
		err := tx.NewRaw(`SELECT c.id FROM bookmark_categories c WHERE c.user_id = ? ORDER BY `+categoryOrder+` FOR UPDATE`, userID).Scan(ctx, &ids)
		if err != nil {
			return err
		}
		if !swapNeighbor(ids, id, up) {
			if slices.Contains(ids, id) {
				return nil
			}
			return ErrNotFound
		}
		_, err = tx.NewRaw(`
			UPDATE bookmark_categories c SET position = o.pos
			FROM unnest(?::bigint[]) WITH ORDINALITY AS o (id, pos)
			WHERE c.id = o.id AND c.user_id = ?`, pgdialect.Array(ids), userID).Exec(ctx)
		return err
	})
}

// swapNeighbor는 ids에서 id를 한 칸 앞(up)이나 뒤의 것과 맞바꾼다. id가 없거나 이미 끝에 있으면 거짓이다.
func swapNeighbor(ids []int64, id int64, up bool) bool {
	i := slices.Index(ids, id)
	if i < 0 {
		return false
	}
	j := i + 1
	if up {
		j = i - 1
	}
	if j < 0 || j >= len(ids) {
		return false
	}
	ids[i], ids[j] = ids[j], ids[i]
	return true
}

// DeleteCategory는 사용자의 카테고리를 지운다. 그 안의 북마크는 남는다.
func (s *Store) DeleteCategory(ctx context.Context, userID, id int64) error {
	return notFoundIfNone(s.db.NewRaw(`DELETE FROM bookmark_categories WHERE id = ? AND user_id = ?`, id, userID).Exec(ctx))
}

// Bookmark는 사용자가 그 책을 북마크했는지와, 그 책을 넣은 카테고리의 번호를 돌려준다.
func (s *Store) Bookmark(ctx context.Context, userID, bookID int64) (bool, []int64, error) {
	exists, err := s.db.NewSelect().Table("bookmarks").Where("user_id = ? AND book_id = ?", userID, bookID).Exists(ctx)
	if err != nil || !exists {
		return false, nil, err
	}
	var ids []int64
	err = s.db.NewRaw(`SELECT category_id FROM bookmark_category_books WHERE user_id = ? AND book_id = ? ORDER BY category_id`,
		userID, bookID).Scan(ctx, &ids)
	return true, ids, err
}

// SetBookmark는 책을 북마크하고, 그 책을 넣을 카테고리를 categoryIDs로 바꾼다.
// 다른 사용자의 카테고리 번호는 버린다. 책이 없으면 ErrNotFound다.
func (s *Store) SetBookmark(ctx context.Context, userID, bookID int64, categoryIDs []int64) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewRaw(`INSERT INTO bookmarks (user_id, book_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, userID, bookID).Exec(ctx)
		if isForeignKeyViolation(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		// 이미 넣은 카테고리는 넣은 시각을 남기려고 지우지 않는다.
		_, err = tx.NewRaw(`DELETE FROM bookmark_category_books WHERE user_id = ? AND book_id = ? AND NOT (category_id = ANY (?))`,
			userID, bookID, pgdialect.Array(nonNil(categoryIDs))).Exec(ctx)
		if err != nil || len(categoryIDs) == 0 {
			return err
		}
		// 카테고리에 새로 넣은 책은 그 카테고리의 맨 앞에 둔다. 이미 넣은 책은 순서를 그대로 둔다.
		_, err = tx.NewRaw(`
			INSERT INTO bookmark_category_books (category_id, user_id, book_id, position)
			SELECT c.id, c.user_id, ?, (SELECT coalesce(min(cb.position), 1) - 1 FROM bookmark_category_books cb WHERE cb.category_id = c.id)
			FROM bookmark_categories c WHERE c.user_id = ? AND c.id IN (?)
			ON CONFLICT DO NOTHING`, bookID, userID, bun.List(categoryIDs)).Exec(ctx)
		return err
	})
}

// ReorderCategoryBooks는 사용자의 카테고리 안에서 책 순서를 bookIDs의 순서로 바꾼다. 화면에 보이는 책의 순서를 1부터 다시 매긴다.
// 카테고리에 없는 책과 겹친 번호는 버리고, bookIDs에 없는 책은 원래 순서대로 뒤에 둔다.
// 다른 화면에서 그사이 책을 넣거나 뺐어도 남은 순서를 지킨다. 다른 사용자의 카테고리는 ErrNotFound다.
func (s *Store) ReorderCategoryBooks(ctx context.Context, userID, categoryID int64, bookIDs []int64) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var mine int
		err := tx.NewRaw(`SELECT count(*) FROM bookmark_categories WHERE id = ? AND user_id = ?`, categoryID, userID).Scan(ctx, &mine)
		if err != nil {
			return err
		}
		if mine == 0 {
			return ErrNotFound
		}
		var current []int64
		err = tx.NewRaw(`
			SELECT b.id
			FROM bookmark_category_books cb JOIN books b ON b.id = cb.book_id
			WHERE cb.category_id = ? AND `+visibleBook+`
			ORDER BY `+categoryBookOrder+`
			FOR UPDATE OF cb`, categoryID).Scan(ctx, &current)
		if err != nil {
			return err
		}
		order := make([]int64, 0, len(current))
		for _, id := range bookIDs {
			if slices.Contains(current, id) && !slices.Contains(order, id) {
				order = append(order, id)
			}
		}
		for _, id := range current {
			if !slices.Contains(order, id) {
				order = append(order, id)
			}
		}
		_, err = tx.NewRaw(`
			UPDATE bookmark_category_books cb SET position = o.pos
			FROM unnest(?::bigint[]) WITH ORDINALITY AS o (book_id, pos)
			WHERE cb.category_id = ? AND cb.book_id = o.book_id`, pgdialect.Array(order), categoryID).Exec(ctx)
		return err
	})
}

// DeleteBookmark는 북마크를 지운다. 그 책은 카테고리에서도 빠진다.
func (s *Store) DeleteBookmark(ctx context.Context, userID, bookID int64) error {
	return notFoundIfNone(s.db.NewRaw(`DELETE FROM bookmarks WHERE user_id = ? AND book_id = ?`, userID, bookID).Exec(ctx))
}

type bookmarkView struct {
	bookView
	At         time.Time `bun:"at"`
	Categories []int64   `bun:"categories,array"`
}

func (v bookmarkView) toBookmark() book.Bookmark {
	return book.Bookmark{Book: v.toBook(), At: v.At, Categories: v.Categories}
}

// Bookmarks는 사용자의 북마크를 최근에 북마크한 순서로 돌려준다. 순서는 카테고리 안에서만 바꾼다.
func (s *Store) Bookmarks(ctx context.Context, userID int64) ([]book.Bookmark, error) {
	var views []bookmarkView
	err := s.db.NewRaw(`
		SELECT `+bookColumns+`, k.created_at AS at,
		       ARRAY(SELECT cb.category_id FROM bookmark_category_books cb
		             WHERE cb.user_id = k.user_id AND cb.book_id = k.book_id ORDER BY cb.category_id) AS categories
		FROM bookmarks k JOIN books b ON b.id = k.book_id
		WHERE k.user_id = ? AND `+visibleBook+`
		ORDER BY k.created_at DESC, b.id`, userID).Scan(ctx, &views)
	return convert(views, bookmarkView.toBookmark), err
}
