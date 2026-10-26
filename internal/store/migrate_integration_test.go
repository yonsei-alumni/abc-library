//go:build integration

package store

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// withMigration은 실제 마이그레이션에 파일 하나를 더한 파일 시스템이다.
func withMigration(t *testing.T, name, sql string) fs.FS {
	t.Helper()
	files := fstest.MapFS{}
	err := fs.WalkDir(migrationFiles, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := migrationFiles.ReadFile(path)
		files[path] = &fstest.MapFile{Data: data}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	files["migrations/"+name] = &fstest.MapFile{Data: []byte(sql)}
	return files
}

func tableExists(t *testing.T, s *Store, name string) bool {
	t.Helper()
	var exists bool
	if err := s.db.QueryRowContext(context.Background(), "SELECT to_regclass(?) IS NOT NULL", name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

func applied(t *testing.T, s *Store, name string) bool {
	t.Helper()
	exists, err := s.db.NewSelect().Table("bun_migrations").Where("name = ?", name).Exists(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return exists
}

func TestMigrationFilesRunInTransactions(t *testing.T) {
	// 이미 적용한 마이그레이션의 파일 이름을 .tx.up.sql로 바꿔도 다시 적용하지 않는다.
	err := fs.WalkDir(migrationFiles, ".", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.HasSuffix(path, ".tx.up.sql") && !strings.HasSuffix(path, ".tx.down.sql") {
			t.Errorf("%s: 마이그레이션은 트랜잭션 파일(.tx.up.sql, .tx.down.sql)이어야 한다", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	s := openTestStore(t)
	if group, err := s.Migrate(context.Background()); err != nil || group != "" {
		t.Errorf("다시 실행하면 적용할 것이 없어야 한다: %q, %v", group, err)
	}
}

func TestFailedMigrationLeavesNoTrace(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	const id = "29991231000000"

	// 첫 문장은 성공하고 둘째 문장이 실패한다.
	broken := withMigration(t, id+"_probe.tx.up.sql", "CREATE TABLE probe (n int);\n--bun:split\nINSERT INTO probe VALUES (1 / 0);\n")
	if _, err := s.migrate(ctx, broken); err == nil {
		t.Fatal("실패하는 마이그레이션이 성공했다")
	}
	if tableExists(t, s, "probe") {
		t.Error("실패한 마이그레이션의 앞 문장이 되돌려지지 않았다")
	}
	if applied(t, s, id) {
		t.Error("실패한 마이그레이션이 적용됐다고 기록됐다")
	}

	// 고친 뒤 다시 실행하면 적용된다. 실패한 실행이 잠금을 남기지 않았다.
	fixed := withMigration(t, id+"_probe.tx.up.sql", "CREATE TABLE probe (n int);\n--bun:split\nINSERT INTO probe VALUES (1);\n")
	group, err := s.migrate(ctx, fixed)
	if err != nil || group == "" {
		t.Fatalf("고친 마이그레이션 = %q, %v", group, err)
	}
	if !tableExists(t, s, "probe") || !applied(t, s, id) {
		t.Error("고친 마이그레이션이 적용되지 않았다")
	}
}

func TestInterruptedMigrationReleasesLock(t *testing.T) {
	s := openTestStore(t)
	const id = "29991231000000"

	// Ctrl+C나 SIGTERM으로 ctx가 마이그레이션 도중에 취소되는 경우다.
	slow := withMigration(t, id+"_slow.tx.up.sql", "CREATE TABLE probe (n int);\n--bun:split\nSELECT pg_sleep(10);\n")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := s.migrate(ctx, slow); !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "cancel") {
		t.Fatalf("취소된 마이그레이션의 오류 = %v", err)
	}
	if tableExists(t, s, "probe") || applied(t, s, id) {
		t.Error("취소된 마이그레이션은 아무것도 남기지 않아야 한다")
	}

	quick := withMigration(t, id+"_slow.tx.up.sql", "CREATE TABLE probe (n int);\n")
	if _, err := s.migrate(context.Background(), quick); err != nil {
		t.Fatalf("취소된 뒤에 잠금이 남았다: %v", err)
	}
}

func TestUnlockMigrationsReleasesStaleLock(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// SIGKILL처럼 Unlock이 돌지 못하고 끝난 경우를 흉내 낸다.
	if _, err := s.db.ExecContext(ctx, "INSERT INTO bun_migration_locks (table_name) VALUES ('bun_migrations')"); err != nil {
		t.Fatal(err)
	}
	_, err := s.Migrate(ctx)
	if err == nil || !strings.Contains(err.Error(), "batch unlock") {
		t.Fatalf("잠금이 남았으면 푸는 방법을 알려야 한다: %v", err)
	}
	if err := s.UnlockMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatalf("잠금을 푼 뒤에도 마이그레이션이 막혔다: %v", err)
	}
	// 잠금이 없을 때 풀어도 오류가 아니다.
	if err := s.UnlockMigrations(ctx); err != nil {
		t.Errorf("잠금이 없을 때 = %v", err)
	}
}
