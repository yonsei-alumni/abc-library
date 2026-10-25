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

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql용 pgx 드라이버
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/migrate"

	"github.com/yonsei-alumni/abc-library/internal/book"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

var (
	// ErrNotFound는 찾는 행이 없다는 뜻이다.
	ErrNotFound = errors.New("찾는 데이터가 없습니다")
	// ErrConflict는 같은 이름이나 주소가 이미 있다는 뜻이다.
	ErrConflict = errors.New("이미 있는 데이터와 겹칩니다")
)

// PostgreSQL 오류 코드. https://www.postgresql.org/docs/current/errcodes-appendix.html
const (
	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
)

func isUniqueViolation(err error) bool     { return hasPgCode(err, uniqueViolation) }
func isForeignKeyViolation(err error) bool { return hasPgCode(err, foreignKeyViolation) }

func hasPgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

// notFoundIfNone은 고치거나 지운 행이 없으면 ErrNotFound를 돌려준다.
func notFoundIfNone(res sql.Result, err error) error {
	return expectRow(res, err, ErrNotFound)
}

// conflictIfNone은 더한 행이 없으면 ErrConflict를 돌려준다. ON CONFLICT DO NOTHING으로 더하는 쿼리에 쓴다.
func conflictIfNone(res sql.Result, err error) error {
	return expectRow(res, err, ErrConflict)
}

func expectRow(res sql.Result, err, ifNone error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ifNone
	}
	return nil
}

// convert는 조회한 행을 도메인 값으로 바꾼다.
func convert[R, T any](rows []R, to func(R) T) []T {
	out := make([]T, len(rows))
	for i, r := range rows {
		out[i] = to(r)
	}
	return out
}

// nonNil은 nil 슬라이스를 빈 슬라이스로 바꾼다. NOT NULL인 배열과 JSON 컬럼에 NULL 대신 빈 값을 넣는다.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

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
		Publisher: v.Publisher, KyoboPID: v.KyoboPID, Tags: v.Tags,
	}
}

// bookColumns는 bookView로 읽을 책 컬럼이다. 테이블 별칭은 b다.
// 태그는 태그 목록의 순서(모임 주제가 먼저)로 늘어놓는다.
const bookColumns = `b.id, coalesce(b.isbn13, '') AS isbn13, b.title, b.authors, b.publisher,
	coalesce(b.kyobo_pid, '') AS kyobo_pid,
	ARRAY(SELECT t.tag FROM book_tags t JOIN tags g ON g.name = t.tag WHERE t.book_id = b.id ` + tagOrder + `) AS tags`

// tagOrder는 tags 테이블(별칭 g)의 태그를 정해진 순서로 늘어놓는다.
const tagOrder = `ORDER BY g.kind = 'topic' DESC, g.position, g.name`

// visibleBook은 지우지 않은 언급이 있는 책(별칭 b)이다. 관리자가 언급을 모두 지운 책은 목록에서 뺀다.
const visibleBook = `EXISTS (SELECT 1 FROM mentions m WHERE m.book_id = b.id AND NOT m.hidden)`

type mentionRow struct {
	bun.BaseModel `bun:"table:mentions"`

	ID          int64            `bun:",pk,autoincrement"`
	BookID      int64            `bun:",notnull"`
	PostID      int64            `bun:",notnull"`
	Kind        book.MentionKind `bun:",notnull"`
	MentionedAt time.Time        `bun:",notnull"`
	Summary     string           `bun:",notnull"`
	Links       []book.Link      `bun:",type:jsonb,notnull"`
	Edited      bool             `bun:",notnull"`
	Hidden      bool             `bun:",notnull"`
}

// sourceKey는 언급의 출처(본문이나 댓글 하나)와 책을 가리키는 키다. 같은 출처는 언급 시각이 같다.
func sourceKey(bookID int64, kind book.MentionKind, at time.Time) string {
	return fmt.Sprintf("%d|%s|%d", bookID, kind, at.UnixMicro())
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
// 같은 글을 다시 저장하면 그 글의 언급을 새 결과로 바꾼다. 관리자가 고치거나 지운 언급은 그대로 두고,
// 같은 본문이나 댓글에서 같은 책에 대한 새 언급은 더하지 않는다.
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
		_, err = tx.NewDelete().Model((*mentionRow)(nil)).Where("post_id = ?", row.ID).Where("NOT edited").
			Returning("book_id").Exec(ctx, &oldBookIDs)
		if err != nil {
			return err
		}

		// 추출 결과의 책 두 개가 ISBN이나 정규화한 제목으로 DB의 같은 책에 묶일 수 있다.
		// 그때 같은 본문 언급이나 같은 댓글 언급이 한 책에 두 번 붙지 않게 한다.
		// 관리자가 고치거나 지운 언급도 이미 붙은 것으로 본다.
		var kept []mentionRow
		if err := tx.NewSelect().Model(&kept).Column("book_id", "kind", "mentioned_at").Where("post_id = ?", row.ID).Scan(ctx); err != nil {
			return err
		}
		saved := map[string]bool{}
		for _, k := range kept {
			saved[sourceKey(k.BookID, k.Kind, k.MentionedAt)] = true
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
				key := sourceKey(bookID, m.Kind, m.At)
				if saved[key] {
					continue
				}
				saved[key] = true
				_, err = tx.NewInsert().Model(&mentionRow{
					BookID: bookID, PostID: row.ID, Kind: m.Kind, MentionedAt: m.At, Summary: m.Summary, Links: nonNil(m.Links),
				}).Exec(ctx)
				if err != nil {
					return err
				}
			}
		}

		// 수정된 글에서 빠져서 어느 언급에도 남지 않은 책은 지운다. 누가 북마크한 책은 북마크를 잃지 않게 남긴다.
		if len(oldBookIDs) > 0 {
			_, err = tx.NewDelete().Model((*bookRow)(nil)).
				Where("id IN (?)", bun.List(oldBookIDs)).
				Where("NOT EXISTS (SELECT 1 FROM mentions m WHERE m.book_id = book_row.id)").
				Where("NOT EXISTS (SELECT 1 FROM bookmarks k WHERE k.book_id = book_row.id)").
				Exec(ctx)
		}
		return err
	})
}

// upsertBook은 같은 책 키(ISBN이나 작품 키)를 가진 책을 찾아서 빠진 정보와 태그를 더하고, 없으면 새로 만든다.
// 관리자가 고친 책은 정보와 태그를 바꾸지 않는다. 어느 경우든 이 책의 키를 더해서 다음에도 같은 책을 찾게 한다.
func upsertBook(ctx context.Context, tx bun.Tx, b book.Book) (int64, error) {
	keys := bookKeys(b)
	var found []struct {
		ID     int64 `bun:"id"`
		Edited bool  `bun:"edited"`
	}
	// ISBN이 맞는 책을 작품 키만 맞는 책보다 먼저 고른다.
	err := tx.NewRaw(`
		SELECT b.id, b.edited_at IS NOT NULL AS edited
		FROM books b JOIN book_keys k ON k.book_id = b.id
		WHERE k.key IN (?)
		ORDER BY k.key LIKE 'isbn:%' DESC, b.id
		LIMIT 1`, bun.List(keys)).Scan(ctx, &found)
	if err != nil {
		return 0, err
	}

	var id int64
	switch {
	case len(found) == 0:
		row := &bookRow{ISBN13: b.ISBN13, Title: b.Title, Authors: nonNil(b.Authors), Publisher: b.Publisher, KyoboPID: b.KyoboPID}
		if _, err := tx.NewInsert().Model(row).Returning("id").Exec(ctx); err != nil {
			return 0, err
		}
		id = row.ID
	case found[0].Edited:
		return found[0].ID, addKeys(ctx, tx, found[0].ID, keys)
	default:
		id = found[0].ID
		if err := fillMissingInfo(ctx, tx, id, b); err != nil {
			return 0, err
		}
	}

	// 추출하는 동안 관리자가 지운 태그는 건너뛴다.
	for _, tag := range b.Tags {
		if _, err := tx.NewRaw("INSERT INTO book_tags (book_id, tag) SELECT ?, name FROM tags WHERE name = ? ON CONFLICT DO NOTHING", id, tag).Exec(ctx); err != nil {
			return 0, err
		}
	}
	return id, addKeys(ctx, tx, id, keys)
}

// fillMissingInfo는 책에 비어 있는 ISBN, 교보문고 상품 번호, 출판사만 from의 값으로 채운다. 이미 있는 값은 바꾸지 않는다.
func fillMissingInfo(ctx context.Context, db bun.IDB, id int64, from book.Book) error {
	_, err := db.NewUpdate().Model((*bookRow)(nil)).
		Set("isbn13 = coalesce(isbn13, nullif(?, ''))", from.ISBN13).
		Set("kyobo_pid = coalesce(kyobo_pid, nullif(?, ''))", from.KyoboPID).
		Set("publisher = CASE WHEN publisher = '' THEN ? ELSE publisher END", from.Publisher).
		Set("updated_at = now()").
		Where("id = ?", id).
		Exec(ctx)
	return err
}

// requireBook은 책이 없으면 ErrNotFound를 돌려준다.
func requireBook(ctx context.Context, db bun.IDB, id int64) error {
	exists, err := db.NewSelect().Model((*bookRow)(nil)).Where("id = ?", id).Exists(ctx)
	if err == nil && !exists {
		return ErrNotFound
	}
	return err
}

// bookKeys는 배치가 같은 책을 찾는 키다. 작품 키와, ISBN이 있으면 'isbn:'을 붙인 ISBN이다.
func bookKeys(b book.Book) []string {
	keys := []string{book.WorkKey(b.Title, b.Authors)}
	if b.ISBN13 != "" {
		keys = append(keys, isbnKey(b.ISBN13))
	}
	return keys
}

func isbnKey(isbn string) string {
	return "isbn:" + isbn
}

// addKeys는 책에 키를 더한다. 다른 책이 이미 가진 키는 그대로 둔다.
func addKeys(ctx context.Context, db bun.IDB, bookID int64, keys []string) error {
	for _, key := range keys {
		if _, err := db.NewRaw("INSERT INTO book_keys (key, book_id) VALUES (?, ?) ON CONFLICT DO NOTHING", key, bookID).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

type entryRow struct {
	bookView
	BandPostNo int       `bun:"band_post_no"`
	PostedAt   time.Time `bun:"posted_at"`
	IsNew      bool      `bun:"is_new"`
	Summary    string    `bun:"summary"`
	Comments   int       `bun:"comments"`
	Mentions   int       `bun:"book_mentions"`
}

func (r entryRow) toEntry() book.Entry {
	return book.Entry{Book: r.toBook(), PostNo: r.BandPostNo, PostedAt: r.PostedAt, IsNew: r.IsNew, Summary: r.Summary,
		Comments: r.Comments, Mentions: r.Mentions}
}

// Timeline은 최근 소식을 시각의 역순으로 돌려준다. 같은 글에서 같은 책을 여러 번 다뤘으면 소식 하나로 묶는다.
func (s *Store) Timeline(ctx context.Context, limit, offset int) ([]book.Entry, error) {
	var rows []entryRow
	err := s.db.NewRaw(`
		WITH news AS (
			SELECT m.book_id, p.band_post_no, min(m.mentioned_at) AS posted_at,
			       (array_agg(m.summary ORDER BY m.kind = 'post' DESC, m.mentioned_at, m.id))[1] AS summary,
			       count(*) FILTER (WHERE m.kind = 'comment') AS comments, count(*) AS mentions
			FROM mentions m JOIN posts p ON p.id = m.post_id
			WHERE NOT m.hidden
			GROUP BY m.book_id, p.band_post_no
		)
		SELECT `+bookColumns+`, n.band_post_no, n.posted_at, n.summary, n.comments,
		       (sum(n.mentions) OVER (PARTITION BY n.book_id))::int AS book_mentions,
		       n.posted_at = min(n.posted_at) OVER (PARTITION BY n.book_id) AS is_new
		FROM news n JOIN books b ON b.id = n.book_id
		ORDER BY n.posted_at DESC, b.id
		LIMIT ? OFFSET ?`, limit, offset).Scan(ctx, &rows)
	return convert(rows, entryRow.toEntry), err
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

func (r shelvedRow) toShelved() book.Shelved {
	return book.Shelved{Book: r.toBook(), LastPostedAt: r.LastPostedAt, PostCount: r.PostCount}
}

// Books는 조건에 맞는 책을 마지막으로 다뤄진 순서로 돌려준다.
func (s *Store) Books(ctx context.Context, f Filter, limit, offset int) ([]book.Shelved, error) {
	pattern := likePattern(f.Query)
	var rows []shelvedRow
	err := s.db.NewRaw(`
		SELECT `+bookColumns+`, max(m.mentioned_at) AS last_posted_at, count(DISTINCT m.post_id) AS post_count
		FROM books b
		JOIN mentions m ON m.book_id = b.id AND NOT m.hidden
		WHERE (? = '' OR EXISTS (SELECT 1 FROM book_tags t WHERE t.book_id = b.id AND t.tag = ?))
		  AND (? = '' OR ? = ANY (b.authors))
		  AND (? = '%%' OR squash(b.title) LIKE ?
		       OR EXISTS (SELECT 1 FROM unnest(b.authors) a WHERE squash(a) LIKE ?)
		       OR EXISTS (SELECT 1 FROM book_tags t WHERE t.book_id = b.id AND squash(t.tag) LIKE ?))
		GROUP BY b.id
		ORDER BY last_posted_at DESC, b.id
		LIMIT ? OFFSET ?`,
		f.Tag, f.Tag, f.Author, f.Author, pattern, pattern, pattern, pattern, limit, offset).Scan(ctx, &rows)
	return convert(rows, shelvedRow.toShelved), err
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
	ID          int64            `bun:"id"`
	BookID      int64            `bun:"book_id"`
	BandPostNo  int              `bun:"band_post_no"`
	Kind        book.MentionKind `bun:"kind"`
	MentionedAt time.Time        `bun:"mentioned_at"`
	Summary     string           `bun:"summary"`
	Links       []book.Link      `bun:"links,type:jsonb"`
}

// Book은 책 한 권과, 그 책에 대한 언급을 오래된 순서로 돌려준다. 관리자가 지운 언급은 빼고,
// 언급이 모두 지워진 책도 돌려준다.
func (s *Store) Book(ctx context.Context, id int64) (book.Book, []book.Mention, error) {
	var views []bookView
	if err := s.db.NewRaw(`SELECT `+bookColumns+` FROM books b WHERE b.id = ?`, id).Scan(ctx, &views); err != nil {
		return book.Book{}, nil, err
	}
	if len(views) == 0 {
		return book.Book{}, nil, ErrNotFound
	}
	mentions, err := s.visibleMentions(ctx, `m.book_id = ?`, id)
	if err != nil {
		return book.Book{}, nil, err
	}
	return views[0].toBook(), mentions, nil
}

// visibleMentions는 조건(언급의 별칭 m)에 맞는 언급 중 관리자가 지우지 않은 것을 오래된 순서로 돌려준다.
// 화면에 보일 언급은 모두 여기서 읽는다.
func (s *Store) visibleMentions(ctx context.Context, where string, args ...any) ([]book.Mention, error) {
	var rows []mentionView
	err := s.db.NewRaw(`
		SELECT m.id, m.book_id, p.band_post_no, m.kind, m.mentioned_at, m.summary, m.links
		FROM mentions m JOIN posts p ON p.id = m.post_id
		WHERE `+where+` AND NOT m.hidden
		ORDER BY m.mentioned_at, m.id`, args...).Scan(ctx, &rows)
	return convert(rows, mentionView.toMention), err
}

func (r mentionView) toMention() book.Mention {
	return book.Mention{ID: r.ID, BookID: r.BookID, PostNo: r.BandPostNo, Kind: r.Kind, At: r.MentionedAt, Summary: r.Summary, Links: r.Links}
}

// TagCounts는 태그별 책 수다.
func (s *Store) TagCounts(ctx context.Context) ([]book.Count, error) {
	return s.counts(ctx, `
		SELECT t.tag AS name, count(*) AS books
		FROM book_tags t
		WHERE EXISTS (SELECT 1 FROM mentions m WHERE m.book_id = t.book_id AND NOT m.hidden)
		GROUP BY t.tag
		ORDER BY books DESC, name`)
}

// AuthorCounts는 저자별 책 수다.
func (s *Store) AuthorCounts(ctx context.Context) ([]book.Count, error) {
	return s.counts(ctx, `
		SELECT a AS name, count(*) AS books
		FROM books b, unnest(b.authors) AS a
		WHERE `+visibleBook+`
		GROUP BY a
		ORDER BY books DESC, name`)
}

func (s *Store) counts(ctx context.Context, query string) ([]book.Count, error) {
	var rows []countRow
	err := s.db.NewRaw(query).Scan(ctx, &rows)
	return convert(rows, countRow.toCount), err
}

type countRow struct {
	Name  string `bun:"name"`
	Books int    `bun:"books"`
}

func (r countRow) toCount() book.Count {
	return book.Count{Name: r.Name, Books: r.Books}
}
