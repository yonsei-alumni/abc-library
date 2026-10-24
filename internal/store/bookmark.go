package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"

	"github.com/yonsei-alumni/abc-library/internal/book"
)

// ErrTooMany는 사용자 한 명이 만들 수 있는 수를 넘었다는 뜻이다.
var ErrTooMany = errors.New("더 만들 수 없습니다")

// visibleBook은 지우지 않은 언급이 있는 책(별칭 b)이다. 관리자가 언급을 모두 지운 책은 북마크 목록에서도 뺀다.
const visibleBook = `EXISTS (SELECT 1 FROM mentions m WHERE m.book_id = b.id AND NOT m.hidden)`

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
	res, err := s.db.NewRaw(`DELETE FROM users WHERE id = ?`, id).Exec(ctx)
	if err != nil {
		return err
	}
	return expectRow(res, ErrNotFound)
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

func (s *Store) categories(ctx context.Context, where string, args ...any) ([]book.Category, error) {
	var views []categoryView
	err := s.db.NewRaw(`SELECT `+categoryColumns+` FROM bookmark_categories c WHERE `+where+` ORDER BY c.created_at, c.id`, args...).Scan(ctx, &views)
	if err != nil {
		return nil, err
	}
	cs := make([]book.Category, len(views))
	for i, v := range views {
		cs[i] = v.toCategory()
	}
	return cs, nil
}

// Categories는 사용자의 카테고리를 만든 순서로 돌려준다.
func (s *Store) Categories(ctx context.Context, userID int64) ([]book.Category, error) {
	return s.categories(ctx, `c.user_id = ?`, userID)
}

// Category는 사용자의 카테고리 하나다. 다른 사용자의 카테고리는 ErrNotFound다.
func (s *Store) Category(ctx context.Context, userID, id int64) (book.Category, error) {
	cs, err := s.categories(ctx, `c.user_id = ? AND c.id = ?`, userID, id)
	if err != nil {
		return book.Category{}, err
	}
	if len(cs) == 0 {
		return book.Category{}, ErrNotFound
	}
	return cs[0], nil
}

// CategoryByCode는 공개 주소의 코드로 카테고리와 그 책을 넣은 순서의 역순으로 돌려준다.
// 공개하지 않은 카테고리도 돌려주므로 보여 줄지는 부르는 쪽이 정한다.
func (s *Store) CategoryByCode(ctx context.Context, code string) (book.Category, []book.Book, error) {
	cs, err := s.categories(ctx, `c.code = ?`, code)
	if err != nil {
		return book.Category{}, nil, err
	}
	if len(cs) == 0 {
		return book.Category{}, nil, ErrNotFound
	}
	books, err := s.CategoryBooks(ctx, cs[0].ID)
	return cs[0], books, err
}

// CategoryBooks는 카테고리에 넣은 책을 넣은 순서의 역순으로 돌려준다.
func (s *Store) CategoryBooks(ctx context.Context, categoryID int64) ([]book.Book, error) {
	var views []bookView
	err := s.db.NewRaw(`
		SELECT `+bookColumns+`
		FROM bookmark_category_books cb JOIN books b ON b.id = cb.book_id
		WHERE cb.category_id = ? AND `+visibleBook+`
		ORDER BY cb.created_at DESC, b.id`, categoryID).Scan(ctx, &views)
	if err != nil {
		return nil, err
	}
	books := make([]book.Book, len(views))
	for i, v := range views {
		books[i] = v.toBook()
	}
	return books, nil
}

// AddCategory는 카테고리를 만들고 번호를 돌려준다. 공개 주소의 코드는 새로 뽑는다.
// 사용자가 이미 book.MaxCategories개를 만들었으면 ErrTooMany다.
func (s *Store) AddCategory(ctx context.Context, userID int64, c book.Category) (int64, error) {
	n, err := s.db.NewSelect().Model((*categoryRow)(nil)).Where("user_id = ?", userID).Count(ctx)
	if err != nil {
		return 0, err
	}
	if n >= book.MaxCategories {
		return 0, ErrTooMany
	}
	// 코드가 다른 카테고리와 겹칠 확률은 아주 낮지만, 겹치면 다시 뽑는다.
	for range 3 {
		row := &categoryRow{UserID: userID, Title: c.Title, Body: c.Body, PublicTitle: c.PublicTitle, Public: c.Public, Code: book.NewCode()}
		_, err = s.db.NewInsert().Model(row).Returning("id").Exec(ctx)
		if !isUniqueViolation(err) {
			return row.ID, err
		}
	}
	return 0, err
}

// UpdateCategory는 사용자의 카테고리를 고친다. 공개 주소의 코드는 그대로 둔다.
func (s *Store) UpdateCategory(ctx context.Context, userID int64, c book.Category) error {
	res, err := s.db.NewUpdate().Model((*categoryRow)(nil)).
		Set("title = ?", c.Title).
		Set("body = ?", c.Body).
		Set("public_title = ?", c.PublicTitle).
		Set("public = ?", c.Public).
		Set("updated_at = now()").
		Where("id = ? AND user_id = ?", c.ID, userID).
		Exec(ctx)
	if err != nil {
		return err
	}
	return expectRow(res, ErrNotFound)
}

// DeleteCategory는 사용자의 카테고리를 지운다. 그 안의 북마크는 남는다.
func (s *Store) DeleteCategory(ctx context.Context, userID, id int64) error {
	res, err := s.db.NewRaw(`DELETE FROM bookmark_categories WHERE id = ? AND user_id = ?`, id, userID).Exec(ctx)
	if err != nil {
		return err
	}
	return expectRow(res, ErrNotFound)
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
		ids := categoryIDs
		if ids == nil {
			ids = []int64{}
		}
		// 이미 넣은 카테고리는 넣은 시각을 남기려고 지우지 않는다.
		_, err = tx.NewRaw(`DELETE FROM bookmark_category_books WHERE user_id = ? AND book_id = ? AND NOT (category_id = ANY (?))`,
			userID, bookID, pgdialect.Array(ids)).Exec(ctx)
		if err != nil || len(categoryIDs) == 0 {
			return err
		}
		_, err = tx.NewRaw(`
			INSERT INTO bookmark_category_books (category_id, user_id, book_id)
			SELECT id, user_id, ? FROM bookmark_categories WHERE user_id = ? AND id IN (?)
			ON CONFLICT DO NOTHING`, bookID, userID, bun.List(categoryIDs)).Exec(ctx)
		return err
	})
}

// DeleteBookmark는 북마크를 지운다. 그 책은 카테고리에서도 빠진다.
func (s *Store) DeleteBookmark(ctx context.Context, userID, bookID int64) error {
	res, err := s.db.NewRaw(`DELETE FROM bookmarks WHERE user_id = ? AND book_id = ?`, userID, bookID).Exec(ctx)
	if err != nil {
		return err
	}
	return expectRow(res, ErrNotFound)
}

type bookmarkView struct {
	bookView
	At         time.Time `bun:"at"`
	Categories []int64   `bun:"categories,array"`
}

// Bookmarks는 사용자의 북마크를 북마크한 순서의 역순으로 돌려준다.
func (s *Store) Bookmarks(ctx context.Context, userID int64) ([]book.Bookmark, error) {
	var views []bookmarkView
	err := s.db.NewRaw(`
		SELECT `+bookColumns+`, k.created_at AS at,
		       ARRAY(SELECT cb.category_id FROM bookmark_category_books cb
		             WHERE cb.user_id = k.user_id AND cb.book_id = k.book_id ORDER BY cb.category_id) AS categories
		FROM bookmarks k JOIN books b ON b.id = k.book_id
		WHERE k.user_id = ? AND `+visibleBook+`
		ORDER BY k.created_at DESC, b.id`, userID).Scan(ctx, &views)
	if err != nil {
		return nil, err
	}
	marks := make([]book.Bookmark, len(views))
	for i, v := range views {
		marks[i] = book.Bookmark{Book: v.toBook(), At: v.At, Categories: v.Categories}
	}
	return marks, nil
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
