package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestReleaseMigrationLockDiscardsConnectionAfterUnlockFailure(t *testing.T) {
	conn := &failingUnlockConn{unlockErr: errors.New("unlock failed")}

	releaseMigrationLock(conn)

	if conn.unlockWasCanceled {
		t.Fatal("unlock used a canceled context")
	}
	if !conn.unlockHadDeadline {
		t.Fatal("unlock context has no deadline")
	}
	if !conn.closed {
		t.Fatal("connection was not discarded")
	}
	if conn.closeWasCanceled {
		t.Fatal("close used a canceled context")
	}
}

func TestMigrationListRejectsDuplicateNumericVersions(t *testing.T) {
	_, err := migrationList([]string{
		"migrations/0001_initial.sql",
		"migrations/1_other.sql",
	})
	if err == nil {
		t.Fatal("accepted duplicate migration version")
	}
	if !strings.Contains(err.Error(), "duplicate migration version 1") {
		t.Fatalf("error = %v", err)
	}
}

type failingUnlockConn struct {
	unlockErr         error
	unlockWasCanceled bool
	unlockHadDeadline bool
	closeWasCanceled  bool
	closed            bool
}

func (conn *failingUnlockConn) Exec(ctx context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	conn.unlockWasCanceled = ctx.Err() != nil
	_, conn.unlockHadDeadline = ctx.Deadline()
	return pgconn.CommandTag{}, conn.unlockErr
}

func (conn *failingUnlockConn) Close(ctx context.Context) error {
	conn.closeWasCanceled = ctx.Err() != nil
	conn.closed = true
	return nil
}
