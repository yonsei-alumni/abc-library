// Package store는 Bun으로 Neon(PostgreSQL)에 책과 언급 요약을 저장하고 조회한다.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql용 pgx 드라이버
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/migrate"

	"github.com/yonsei-alumni/abc-library/internal/book"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// ErrNotFound는 찾는 행이 없다는 뜻이다.
var ErrNotFound = errors.New("찾는 데이터가 없습니다")

// Store는 DB 연결이다.
type Store struct {
	db *bun.DB
}

// Open은 연결 문자열로 DB에 연결한다.
func Open(dsn string) (*Store, error) {
	sqldb, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	sqldb.SetMaxOpenConns(4)
	sqldb.SetConnMaxIdleTime(5 * time.Minute)
	return &Store{db: bun.NewDB(sqldb, pgdialect.New())}, nil
}

// Close는 연결을 닫는다.
func (s *Store) Close() error {
	return s.db.Close()
}

// Migrate는 아직 적용하지 않은 마이그레이션을 적용하고, 적용한 마이그레이션 묶음의 이름을 돌려준다.
func (s *Store) Migrate(ctx context.Context) (string, error) {
	migrations := migrate.NewMigrations()
	if err := migrations.Discover(migrationFiles); err != nil {
		return "", err
	}
	m := migrate.NewMigrator(s.db, migrations)
	if err := m.Init(ctx); err != nil {
		return "", err
	}
	if err := m.Lock(ctx); err != nil {
		return "", err
	}
	defer func() { _ = m.Unlock(ctx) }()
	group, err := m.Migrate(ctx)
	if err != nil {
		return "", err
	}
	if group.IsZero() {
		return "", nil
	}
	return group.String(), nil
}

type postRow struct {
	bun.BaseModel `bun:"table:posts"`

	ID          int64     `bun:",pk,autoincrement"`
	BandPostNo  int       `bun:",notnull"`
	PostedAt    time.Time `bun:",notnull"`
	ContentHash string    `bun:",notnull"`
	ProcessedAt time.Time `bun:",notnull"`
}

type bookRow struct {
	bun.BaseModel `bun:"table:books"`

	ID        int64     `bun:",pk,autoincrement"`
	ISBN13    string    `bun:"isbn13,nullzero"`
	WorkKey   string    `bun:",notnull"`
	Title     string    `bun:",notnull"`
	Authors   []string  `bun:",array"`
	Publisher string    `bun:",notnull"`
	KyoboPID  string    `bun:"kyobo_pid,nullzero"`
	UpdatedAt time.Time `bun:",nullzero,notnull,default:current_timestamp"`
}

// bookView는 조회 결과의 책이다. 태그는 book_tags에서 모아 온다.
type bookView struct {
	ID        int64    `bun:"id"`
	ISBN13    string   `bun:"isbn13"`
	Title     string   `bun:"title"`
	Authors   []string `bun:"authors,array"`
	Publisher string   `bun:"publisher"`
	KyoboPID  string   `bun:"kyobo_pid"`
	Tags      []string `bun:"tags,array"`
}

func (v bookView) toBook() book.Book {
	return book.Book{
		ID: v.ID, ISBN13: v.ISBN13, Title: v.Title, Authors: v.Authors,
		Publisher: v.Publisher, KyoboPID: v.KyoboPID, Tags: book.SortTags(v.Tags),
	}
}

// bookColumns는 bookView로 읽을 책 컬럼이다. 테이블 별칭은 b다.
const bookColumns = `b.id, coalesce(b.isbn13, '') AS isbn13, b.title, b.authors, b.publisher,
	coalesce(b.kyobo_pid, '') AS kyobo_pid,
	ARRAY(SELECT t.tag FROM book_tags t WHERE t.book_id = b.id) AS tags`

type mentionRow struct {
	bun.BaseModel `bun:"table:mentions"`

	ID          int64            `bun:",pk,autoincrement"`
	BookID      int64            `bun:",notnull"`
	PostID      int64            `bun:",notnull"`
	Kind        book.MentionKind `bun:",notnull"`
	MentionedAt time.Time        `bun:",notnull"`
	Summary     string           `bun:",notnull"`
	Links       []book.Link      `bun:",type:jsonb,notnull"`
}

// LastPostNo는 저장된 게시글 번호 중 가장 큰 값이다. 저장된 글이 없으면 0이다.
func (s *Store) LastPostNo(ctx context.Context) (int, error) {
	var no int
	err := s.db.NewSelect().Model((*postRow)(nil)).ColumnExpr("coalesce(max(band_post_no), 0)").Scan(ctx, &no)
	return no, err
}

// PostHash는 저장된 게시글의 내용 해시다. 저장된 적 없는 글이면 ok가 거짓이다.
func (s *Store) PostHash(ctx context.Context, no int) (hash string, ok bool, err error) {
	err = s.db.NewSelect().Model((*postRow)(nil)).Column("content_hash").Where("band_post_no = ?", no).Scan(ctx, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return hash, err == nil, err
}

// PostNosSince는 since 이후에 올라온 게시글 번호다. 수정된 글과 새 댓글을 다시 확인할 때 쓴다.
func (s *Store) PostNosSince(ctx context.Context, since time.Time) ([]int, error) {
	var nos []int
	err := s.db.NewSelect().Model((*postRow)(nil)).Column("band_post_no").
		Where("posted_at >= ?", since).Order("band_post_no").Scan(ctx, &nos)
	return nos, err
}

// SavePost는 게시글과 그 글(본문과 댓글)에서 찾은 책을 저장한다.
// 같은 글을 다시 저장하면 그 글의 언급을 새 결과로 바꾼다.
func (s *Store) SavePost(ctx context.Context, p book.Post, findings []book.Finding) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		row := &postRow{BandPostNo: p.No, PostedAt: p.PostedAt, ContentHash: p.ContentHash, ProcessedAt: time.Now()}
		_, err := tx.NewInsert().Model(row).
			On("CONFLICT (band_post_no) DO UPDATE").
			Set("posted_at = EXCLUDED.posted_at").
			Set("content_hash = EXCLUDED.content_hash").
			Set("processed_at = EXCLUDED.processed_at").
			Returning("id").Exec(ctx)
		if err != nil {
			return fmt.Errorf("게시글 %d 저장: %w", p.No, err)
		}
		if _, err := tx.NewDelete().Model((*Pending)(nil)).Where("band_post_no = ?", p.No).Exec(ctx); err != nil {
			return err
		}

		var oldBookIDs []int64
		_, err = tx.NewDelete().Model((*mentionRow)(nil)).Where("post_id = ?", row.ID).Returning("book_id").Exec(ctx, &oldBookIDs)
		if err != nil {
			return err
		}

		for _, f := range findings {
			if len(f.Mentions) == 0 {
				continue
			}
			bookID, err := upsertBook(ctx, tx, f.Book)
			if err != nil {
				return fmt.Errorf("게시글 %d의 책 %q 저장: %w", p.No, f.Book.Title, err)
			}
			for _, m := range f.Mentions {
				links := m.Links
				if links == nil {
					links = []book.Link{}
				}
				_, err = tx.NewInsert().Model(&mentionRow{
					BookID: bookID, PostID: row.ID, Kind: m.Kind, MentionedAt: m.At, Summary: m.Summary, Links: links,
				}).Exec(ctx)
				if err != nil {
					return err
				}
			}
		}

		// 수정된 글에서 빠져서 어느 언급에도 남지 않은 책은 지운다.
		if len(oldBookIDs) > 0 {
			_, err = tx.NewDelete().Model((*bookRow)(nil)).
				Where("id IN (?)", bun.List(oldBookIDs)).
				Where("NOT EXISTS (SELECT 1 FROM mentions m WHERE m.book_id = book_row.id)").
				Exec(ctx)
		}
		return err
	})
}

// upsertBook은 ISBN이나 작품 키가 같은 책을 찾아서 빠진 정보와 태그를 더하고, 없으면 새로 만든다.
func upsertBook(ctx context.Context, tx bun.Tx, b book.Book) (int64, error) {
	key := book.WorkKey(b.Title, b.Authors)
	var existing bookRow
	q := tx.NewSelect().Model(&existing)
	if b.ISBN13 != "" {
		q = q.Where("isbn13 = ? OR work_key = ?", b.ISBN13, key).OrderExpr("isbn13 = ? DESC NULLS LAST", b.ISBN13)
	} else {
		q = q.Where("work_key = ?", key)
	}
	err := q.Limit(1).Scan(ctx)

	var id int64
	switch {
	case errors.Is(err, sql.ErrNoRows):
		authors := b.Authors
		if authors == nil {
			authors = []string{}
		}
		row := &bookRow{ISBN13: b.ISBN13, WorkKey: key, Title: b.Title, Authors: authors, Publisher: b.Publisher, KyoboPID: b.KyoboPID}
		if _, err := tx.NewInsert().Model(row).Returning("id").Exec(ctx); err != nil {
			return 0, err
		}
		id = row.ID
	case err != nil:
		return 0, err
	default:
		id = existing.ID
		_, err = tx.NewUpdate().Model((*bookRow)(nil)).
			Set("isbn13 = coalesce(isbn13, nullif(?, ''))", b.ISBN13).
			Set("kyobo_pid = coalesce(kyobo_pid, nullif(?, ''))", b.KyoboPID).
			Set("publisher = CASE WHEN publisher = '' THEN ? ELSE publisher END", b.Publisher).
			Set("updated_at = now()").
			Where("id = ?", id).
			Exec(ctx)
		if err != nil {
			return 0, err
		}
	}

	for _, tag := range b.Tags {
		if _, err := tx.NewRaw("INSERT INTO book_tags (book_id, tag) VALUES (?, ?) ON CONFLICT DO NOTHING", id, tag).Exec(ctx); err != nil {
			return 0, err
		}
	}
	return id, nil
}

type entryRow struct {
	bookView
	BandPostNo int       `bun:"band_post_no"`
	PostedAt   time.Time `bun:"posted_at"`
	IsNew      bool      `bun:"is_new"`
}

// Timeline은 최근 소식을 시각의 역순으로 돌려준다. 같은 글에서 같은 책을 여러 번 다뤘으면 소식 하나로 묶는다.
func (s *Store) Timeline(ctx context.Context, limit, offset int) ([]book.Entry, error) {
	var rows []entryRow
	err := s.db.NewRaw(`
		WITH news AS (
			SELECT m.book_id, p.band_post_no, min(m.mentioned_at) AS posted_at
			FROM mentions m JOIN posts p ON p.id = m.post_id
			GROUP BY m.book_id, p.band_post_no
		)
		SELECT `+bookColumns+`, n.band_post_no, n.posted_at,
		       n.posted_at = min(n.posted_at) OVER (PARTITION BY n.book_id) AS is_new
		FROM news n JOIN books b ON b.id = n.book_id
		ORDER BY n.posted_at DESC, b.id
		LIMIT ? OFFSET ?`, limit, offset).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	entries := make([]book.Entry, len(rows))
	for i, r := range rows {
		entries[i] = book.Entry{Book: r.toBook(), PostNo: r.BandPostNo, PostedAt: r.PostedAt, IsNew: r.IsNew}
	}
	return entries, nil
}

// Filter는 책 목록의 조건이다. 빈 값은 조건을 걸지 않는다.
type Filter struct {
	Tag    string
	Author string
	// Query는 제목, 저자, 태그에서 찾을 검색어다. 대소문자와 공백을 무시한다.
	Query string
}

type shelvedRow struct {
	bookView
	LastPostedAt time.Time `bun:"last_posted_at"`
	PostCount    int       `bun:"post_count"`
}

// Books는 조건에 맞는 책을 마지막으로 다뤄진 순서로 돌려준다.
func (s *Store) Books(ctx context.Context, f Filter, limit, offset int) ([]book.Shelved, error) {
	pattern := likePattern(f.Query)
	var rows []shelvedRow
	err := s.db.NewRaw(`
		SELECT `+bookColumns+`, max(m.mentioned_at) AS last_posted_at, count(DISTINCT m.post_id) AS post_count
		FROM books b
		JOIN mentions m ON m.book_id = b.id
		WHERE (? = '' OR EXISTS (SELECT 1 FROM book_tags t WHERE t.book_id = b.id AND t.tag = ?))
		  AND (? = '' OR ? = ANY (b.authors))
		  AND (? = '%%' OR squash(b.title) LIKE ?
		       OR EXISTS (SELECT 1 FROM unnest(b.authors) a WHERE squash(a) LIKE ?)
		       OR EXISTS (SELECT 1 FROM book_tags t WHERE t.book_id = b.id AND squash(t.tag) LIKE ?))
		GROUP BY b.id
		ORDER BY last_posted_at DESC, b.id
		LIMIT ? OFFSET ?`,
		f.Tag, f.Tag, f.Author, f.Author, pattern, pattern, pattern, pattern, limit, offset).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	shelf := make([]book.Shelved, len(rows))
	for i, r := range rows {
		shelf[i] = book.Shelved{Book: r.toBook(), LastPostedAt: r.LastPostedAt, PostCount: r.PostCount}
	}
	return shelf, nil
}

// likePattern은 검색어를 squash와 같은 방식(소문자, 공백 제거)으로 바꾸고 LIKE 특수 문자를 이스케이프한다.
func likePattern(q string) string {
	var b strings.Builder
	b.WriteByte('%')
	for _, r := range strings.ToLower(q) {
		switch {
		case unicode.IsSpace(r):
			continue
		case r == '%' || r == '_' || r == '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('%')
	return b.String()
}

type mentionView struct {
	BandPostNo  int              `bun:"band_post_no"`
	Kind        book.MentionKind `bun:"kind"`
	MentionedAt time.Time        `bun:"mentioned_at"`
	Summary     string           `bun:"summary"`
	Links       []book.Link      `bun:"links,type:jsonb"`
}

// Book은 책 한 권과, 그 책에 대한 언급을 오래된 순서로 돌려준다.
func (s *Store) Book(ctx context.Context, id int64) (book.Book, []book.Mention, error) {
	var views []bookView
	if err := s.db.NewRaw(`SELECT `+bookColumns+` FROM books b WHERE b.id = ?`, id).Scan(ctx, &views); err != nil {
		return book.Book{}, nil, err
	}
	if len(views) == 0 {
		return book.Book{}, nil, ErrNotFound
	}
	var rows []mentionView
	err := s.db.NewRaw(`
		SELECT p.band_post_no, m.kind, m.mentioned_at, m.summary, m.links
		FROM mentions m
		JOIN posts p ON p.id = m.post_id
		WHERE m.book_id = ?
		ORDER BY m.mentioned_at, m.id`, id).Scan(ctx, &rows)
	if err != nil {
		return book.Book{}, nil, err
	}
	mentions := make([]book.Mention, len(rows))
	for i, r := range rows {
		mentions[i] = book.Mention{PostNo: r.BandPostNo, Kind: r.Kind, At: r.MentionedAt, Summary: r.Summary, Links: r.Links}
	}
	return views[0].toBook(), mentions, nil
}

// TagCounts는 태그별 책 수다.
func (s *Store) TagCounts(ctx context.Context) ([]book.Count, error) {
	return s.counts(ctx, `
		SELECT t.tag AS name, count(*) AS books
		FROM book_tags t
		WHERE EXISTS (SELECT 1 FROM mentions m WHERE m.book_id = t.book_id)
		GROUP BY t.tag
		ORDER BY books DESC, name`)
}

// AuthorCounts는 저자별 책 수다.
func (s *Store) AuthorCounts(ctx context.Context) ([]book.Count, error) {
	return s.counts(ctx, `
		SELECT a AS name, count(*) AS books
		FROM books b, unnest(b.authors) AS a
		WHERE EXISTS (SELECT 1 FROM mentions m WHERE m.book_id = b.id)
		GROUP BY a
		ORDER BY books DESC, name`)
}

func (s *Store) counts(ctx context.Context, query string) ([]book.Count, error) {
	var rows []struct {
		Name  string `bun:"name"`
		Books int    `bun:"books"`
	}
	if err := s.db.NewRaw(query).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	counts := make([]book.Count, len(rows))
	for i, r := range rows {
		counts[i] = book.Count{Name: r.Name, Books: r.Books}
	}
	return counts, nil
}
