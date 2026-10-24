package store

import (
	"context"
	"time"

	"github.com/uptrace/bun"
)

// 처리하지 못한 글의 이유.
const (
	// PendingFailed는 읽기, 추출, 저장에 실패했다는 뜻이다.
	PendingFailed = "failed"
	// PendingMissing은 게시글 화면이 뜨지 않았다는 뜻이다. 삭제된 글이거나 일시적인 문제다.
	PendingMissing = "missing"
)

// Run은 배치 실행 한 번의 기록이다.
type Run struct {
	bun.BaseModel `bun:"table:batch_runs"`

	ID           int64     `bun:",pk,autoincrement"`
	StartedAt    time.Time `bun:",nullzero,notnull,default:current_timestamp"`
	FinishedAt   time.Time `bun:",nullzero"`
	LastPostNo   int       `bun:",notnull"`
	LatestPostNo int       `bun:",notnull"`
	Checked      int       `bun:",notnull"`
	Extracted    int       `bun:",notnull"`
	Unchanged    int       `bun:",notnull"`
	Missing      int       `bun:",notnull"`
	Failed       int       `bun:",notnull"`
	Books        int       `bun:",notnull"`
	Error        string    `bun:",notnull"`
}

// Pending은 다음 실행에서 다시 시도할 글이다.
type Pending struct {
	bun.BaseModel `bun:"table:pending_posts"`

	BandPostNo  int       `bun:",pk"`
	Reason      string    `bun:",notnull"`
	Attempts    int       `bun:",notnull"`
	LastError   string    `bun:",notnull"`
	LastTriedAt time.Time `bun:",notnull"`
}

// StartRun은 배치 실행 기록을 시작하고 그 ID를 돌려준다.
func (s *Store) StartRun(ctx context.Context) (int64, error) {
	run := &Run{}
	_, err := s.db.NewInsert().Model(run).Returning("id").Exec(ctx)
	return run.ID, err
}

// FinishRun은 배치 실행 결과를 기록한다. 실행이 취소되어도 기록하도록 ctx의 취소를 따르지 않는다.
func (s *Store) FinishRun(ctx context.Context, run Run) error {
	run.FinishedAt = time.Now()
	_, err := s.db.NewUpdate().Model(&run).ExcludeColumn("started_at").WherePK().Exec(context.WithoutCancel(ctx))
	return err
}

// RecentRuns는 최근 배치 실행 기록이다.
func (s *Store) RecentRuns(ctx context.Context, n int) ([]Run, error) {
	var runs []Run
	err := s.db.NewSelect().Model(&runs).Order("id DESC").Limit(int64(n)).Scan(ctx)
	return runs, err
}

// MarkPending은 처리하지 못한 글을 기록한다. 이미 기록된 글이면 시도 횟수를 늘린다.
func (s *Store) MarkPending(ctx context.Context, no int, reason, message string) error {
	_, err := s.db.NewInsert().Model(&Pending{BandPostNo: no, Reason: reason, Attempts: 1, LastError: message, LastTriedAt: time.Now()}).
		On("CONFLICT (band_post_no) DO UPDATE").
		Set("reason = EXCLUDED.reason").
		Set("attempts = pending.attempts + 1").
		Set("last_error = EXCLUDED.last_error").
		Set("last_tried_at = EXCLUDED.last_tried_at").
		Exec(context.WithoutCancel(ctx))
	return err
}

// PendingPostNos는 시도 횟수가 maxAttempts보다 적은, 다시 시도할 글 번호다.
func (s *Store) PendingPostNos(ctx context.Context, maxAttempts int) ([]int, error) {
	var nos []int
	err := s.db.NewSelect().Model((*Pending)(nil)).Column("band_post_no").
		Where("attempts < ?", maxAttempts).Order("band_post_no").Scan(ctx, &nos)
	return nos, err
}

// PendingPosts는 처리하지 못한 글 전체다.
func (s *Store) PendingPosts(ctx context.Context) ([]Pending, error) {
	var ps []Pending
	err := s.db.NewSelect().Model(&ps).Order("band_post_no").Scan(ctx)
	return ps, err
}
