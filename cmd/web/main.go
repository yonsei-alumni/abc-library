// web은 책과 게시글 요약을 보여 주는 웹 서버다.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/yonsei-alumni/abc-library/internal/auth"
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

	port := config.Get("PORT", "8080")
	cfg, err := webConfig(port)
	if err != nil {
		return err
	}
	if cfg.Login == nil {
		log.Info("GOOGLE_CLIENT_ID가 없어서 관리자 로그인을 쓰지 않는다")
	}
	srv, err := web.New(st, cfg, log)
	if err != nil {
		return err
	}
	httpSrv := &http.Server{
		Addr:              ":" + port,
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

// webConfig는 환경 변수로 웹 서버 설정을 만든다. GOOGLE_CLIENT_ID가 없으면 관리자 로그인을 쓰지 않는다.
func webConfig(port string) (web.Config, error) {
	siteURL := strings.TrimRight(config.Get("SITE_URL", "http://localhost:"+port), "/")
	cfg := web.Config{BandNo: config.Get("BAND_NO", "102765569"), SiteURL: siteURL}
	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	if clientID == "" {
		return cfg, nil
	}
	secret := os.Getenv("GOOGLE_CLIENT_SECRET")
	if secret == "" {
		return cfg, errors.New("GOOGLE_CLIENT_ID가 있으면 GOOGLE_CLIENT_SECRET도 있어야 합니다")
	}
	sessions, err := auth.NewSessions(os.Getenv("SESSION_SECRET"), strings.HasPrefix(siteURL, "https://"))
	if err != nil {
		return cfg, fmt.Errorf("SESSION_SECRET: %w", err)
	}
	cfg.Sessions = sessions
	cfg.Login = &auth.Google{ClientID: clientID, ClientSecret: secret, RedirectURL: siteURL + "/auth/callback"}
	return cfg, nil
}
