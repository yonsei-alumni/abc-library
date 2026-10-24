// Package pipeline은 일일 배치의 흐름이다.
// 밴드에서 새 글, 지난번에 처리하지 못한 글, 최근 글을 읽고, 내용이 바뀐 글만 Claude로 추출해서 저장한다.
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/yonsei-alumni/abc-library/internal/band"
	"github.com/yonsei-alumni/abc-library/internal/book"
	"github.com/yonsei-alumni/abc-library/internal/extract"
	"github.com/yonsei-alumni/abc-library/internal/store"
)

// Source는 밴드 게시글을 읽는다. *band.Crawler가 구현한다.
type Source interface {
	LatestPostNo() (int, error)
	Post(no int) (band.Post, error)
}

// Extractor는 게시글에서 책을 찾는다. *extract.Extractor가 구현한다.
type Extractor interface {
	Extract(ctx context.Context, in extract.Input) ([]book.Finding, error)
}

// Store는 게시글과 책, 배치 실행 기록을 저장한다. *store.Store가 구현한다.
type Store interface {
	LastPostNo(ctx context.Context) (int, error)
	PostHash(ctx context.Context, no int) (string, bool, error)
	PostNosSince(ctx context.Context, since time.Time) ([]int, error)
	SavePost(ctx context.Context, p book.Post, findings []book.Finding) error
	StartRun(ctx context.Context) (int64, error)
	FinishRun(ctx context.Context, run store.Run) error
	MarkPending(ctx context.Context, no int, reason, message string) error
	PendingPostNos(ctx context.Context, maxAttempts int) ([]int, error)
}

// MaxAttempts는 처리하지 못한 글을 다시 시도하는 최대 횟수다.
const MaxAttempts = 5

// Options는 배치 한 번의 범위다.
type Options struct {
	// Limit는 한 번에 Claude로 추출할 게시글 수의 상한이다. 0이면 제한하지 않는다.
	Limit int
	// RecheckDays는 수정된 본문과 새 댓글을 다시 확인할 최근 게시글의 기간이다.
	RecheckDays int
	// RedoFrom이 0보다 크면 이 번호부터는 이미 저장한 글도 내용이 같든 다르든 다시 추출한다. 프롬프트를 바꾼 뒤에 쓴다.
	RedoFrom int
	// RedoPosts의 글은 이미 저장했어도 내용이 같든 다르든 다시 추출한다. 몇 개 글만 고칠 때 쓴다.
	RedoPosts []int
	Now       func() time.Time
}

func (o Options) redo(no int) bool {
	return (o.RedoFrom > 0 && no >= o.RedoFrom) || slices.Contains(o.RedoPosts, no)
}

// Report는 배치 한 번의 결과다.
type Report struct {
	// LastPostNo는 실행 전에 저장되어 있던 마지막 글 번호이고, LatestPostNo는 밴드의 최신 글 번호다.
	LastPostNo, LatestPostNo                              int
	Checked, Extracted, Unchanged, Missing, Failed, Books int
}

// Run은 새 글, 다시 시도할 글, 최근 글을 처리하고 실행 기록을 남긴다.
// 세션이 만료되면 바로 멈추고, 처리에 실패한 글이 있으면 오류를 돌려준다. 실패한 글은 다음 실행에서 다시 시도한다.
func Run(ctx context.Context, src Source, ex Extractor, st Store, opts Options, log *slog.Logger) (r Report, err error) {
	id, err := st.StartRun(ctx)
	if err != nil {
		return r, err
	}
	defer func() {
		run := store.Run{
			ID: id, LastPostNo: r.LastPostNo, LatestPostNo: r.LatestPostNo,
			Checked: r.Checked, Extracted: r.Extracted, Unchanged: r.Unchanged,
			Missing: r.Missing, Failed: r.Failed, Books: r.Books,
		}
		if err != nil {
			run.Error = err.Error()
		}
		if ferr := st.FinishRun(ctx, run); ferr != nil {
			log.Error("실행 기록을 남기지 못했다", "오류", ferr)
		}
	}()
	err = process(ctx, src, ex, st, opts, log, &r)
	return r, err
}

func process(ctx context.Context, src Source, ex Extractor, st Store, opts Options, log *slog.Logger, r *Report) error {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	var err error
	if r.LastPostNo, err = st.LastPostNo(ctx); err != nil {
		return err
	}
	if r.LatestPostNo, err = src.LatestPostNo(); err != nil {
		return err
	}
	pending, err := st.PendingPostNos(ctx, MaxAttempts)
	if err != nil {
		return err
	}
	recent, err := st.PostNosSince(ctx, opts.Now().AddDate(0, 0, -opts.RecheckDays))
	if err != nil {
		return err
	}
	var targets []int
	queued := map[int]bool{}
	add := func(no int) {
		if !queued[no] {
			queued[no] = true
			targets = append(targets, no)
		}
	}
	for no := opts.RedoFrom; opts.RedoFrom > 0 && no <= r.LastPostNo; no++ {
		add(no)
	}
	for _, no := range opts.RedoPosts {
		if no > 0 && no <= r.LastPostNo {
			add(no)
		}
	}
	for _, no := range pending {
		add(no)
	}
	for _, no := range recent {
		add(no)
	}
	for no := r.LastPostNo + 1; no <= r.LatestPostNo; no++ {
		add(no)
	}
	log.Info("배치 시작", "저장된 마지막 글", r.LastPostNo, "밴드 최신 글", r.LatestPostNo, "다시 시도할 글", len(pending), "확인할 글", len(targets))

	markPending := func(no int, reason string, cause error) {
		msg := ""
		if cause != nil {
			msg = cause.Error()
		}
		if err := st.MarkPending(ctx, no, reason, msg); err != nil {
			log.Error("처리하지 못한 글을 기록하지 못했다", "글", no, "오류", err)
		}
	}

	for _, no := range targets {
		if opts.Limit > 0 && r.Extracted+r.Failed >= opts.Limit {
			log.Info("추출 상한에 도달해서 나머지는 다음 실행으로 미룬다", "상한", opts.Limit)
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		r.Checked++
		post, err := src.Post(no)
		switch {
		case errors.Is(err, band.ErrSessionExpired):
			return err
		case errors.Is(err, band.ErrNotFound):
			r.Missing++
			markPending(no, store.PendingMissing, nil)
			continue
		case err != nil:
			r.Failed++
			log.Error("게시글을 읽지 못했다", "글", no, "오류", err)
			markPending(no, store.PendingFailed, err)
			continue
		}

		hash := ContentHash(post)
		if old, ok, err := st.PostHash(ctx, no); err != nil {
			return err
		} else if ok && old == hash && !opts.redo(no) {
			r.Unchanged++
			continue
		}

		findings, err := ex.Extract(ctx, input(post))
		if errors.Is(err, extract.ErrRefused) {
			// 거절된 글은 책 없이 저장해서 매일 다시 보내지 않는다.
			log.Warn("Claude가 게시글 처리를 거절했다", "글", no)
			findings = nil
		} else if err != nil {
			r.Failed++
			log.Error("책을 추출하지 못했다", "글", no, "오류", err)
			markPending(no, store.PendingFailed, err)
			continue
		}
		if err := st.SavePost(ctx, book.Post{No: no, PostedAt: post.PostedAt, ContentHash: hash}, findings); err != nil {
			r.Failed++
			log.Error("저장하지 못했다", "글", no, "오류", err)
			markPending(no, store.PendingFailed, err)
			continue
		}
		r.Extracted++
		r.Books += len(findings)
		log.Info("게시글 처리", "글", no, "책", len(findings))
	}
	log.Info("배치 끝", "확인", r.Checked, "추출", r.Extracted, "변경 없음", r.Unchanged, "없는 글", r.Missing, "실패", r.Failed, "책", r.Books)
	if r.Failed > 0 {
		return fmt.Errorf("게시글 %d개를 처리하지 못했습니다. 다음 실행에서 다시 시도합니다", r.Failed)
	}
	return nil
}

// ContentHash는 게시글 내용(본문, 링크, 사진, 댓글)이 바뀌었는지 알기 위한 해시다.
func ContentHash(p band.Post) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00", p.Text)
	for _, l := range p.Links {
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00", l.URL, l.Title)
	}
	for _, img := range p.Images {
		sum := sha256.Sum256(img.Data)
		h.Write(sum[:])
	}
	for _, c := range p.Comments {
		_, _ = fmt.Fprintf(h, "c\x00%s\x00%s\x00", c.At.UTC().Format(time.RFC3339), c.Text)
		for _, l := range c.Links {
			_, _ = fmt.Fprintf(h, "%s\x00%s\x00", l.URL, l.Title)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func input(p band.Post) extract.Input {
	in := extract.Input{PostedAt: p.PostedAt, Text: p.Text, Links: p.Links}
	for _, img := range p.Images {
		in.Images = append(in.Images, extract.Image{MediaType: img.MediaType, Data: img.Data})
	}
	for _, c := range p.Comments {
		in.Comments = append(in.Comments, extract.Comment{At: c.At, Text: c.Text, Links: c.Links})
	}
	return in
}
