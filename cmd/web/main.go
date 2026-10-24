// web은 책과 게시글 요약을 보여 주는 웹 서버다.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yonsei-alumni/abc-library/internal/config"
	"github.com/yonsei-alumni/abc-library/internal/store"
	"github.com/yonsei-alumni/abc-library/internal/web"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("웹 서버 종료", "오류", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	dsn, err := config.DatabaseURL()
	if err != nil {
		return err
	}
	st, err := store.Open(dsn)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	srv, err := web.New(st, config.Get("BAND_NO", "102765569"), log)
	if err != nil {
		return err
	}
	httpSrv := &http.Server{
		Addr:              ":" + config.Get("PORT", "8080"),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Cloud Run은 인스턴스를 내릴 때 SIGTERM을 보낸다. 처리 중인 요청을 마치고 끝낸다.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
	}()

	log.Info("웹 서버 시작", "addr", httpSrv.Addr)
	if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
