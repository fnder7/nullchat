package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	migrationLockID         int64 = 982463117
	migrationCleanupTimeout       = 5 * time.Second
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func ApplyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	defer releaseMigrationLock(migrationPoolConn{conn: conn})

	files, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	migrations, err := migrationList(files)
	if err != nil {
		return err
	}

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
  version bigint PRIMARY KEY,
  applied_at timestamptz NOT NULL DEFAULT now()
)`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	applied, err := appliedVersions(ctx, conn)
	if err != nil {
		return err
	}

	for _, migration := range migrations {
		if applied[migration.version] {
			continue
		}

		sql, err := fs.ReadFile(migrationFiles, migration.name)
		if err != nil {
			return fmt.Errorf("read migration %q: %w", migration.name, err)
		}
		if err := applyMigration(ctx, conn, migration.version, string(sql)); err != nil {
			return err
		}
	}

	return nil
}

type migrationLockConn interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Close(context.Context) error
}

type migrationPoolConn struct {
	conn *pgxpool.Conn
}

func (conn migrationPoolConn) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return conn.conn.Exec(ctx, sql, args...)
}

func (conn migrationPoolConn) Close(ctx context.Context) error {
	return conn.conn.Conn().Close(ctx)
}

func releaseMigrationLock(conn migrationLockConn) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), migrationCleanupTimeout)
	defer cancel()

	if _, err := conn.Exec(cleanupCtx, `SELECT pg_advisory_unlock($1)`, migrationLockID); err != nil {
		_ = conn.Close(cleanupCtx)
	}
}

type migration struct {
	name    string
	version int64
}

func migrationList(names []string) ([]migration, error) {
	names = append([]string(nil), names...)
	sort.Strings(names)

	migrations := make([]migration, 0, len(names))
	versions := make(map[int64]string, len(names))
	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			return nil, err
		}
		if previous, exists := versions[version]; exists {
			return nil, fmt.Errorf("duplicate migration version %d in %q and %q", version, previous, name)
		}
		versions[version] = name
		migrations = append(migrations, migration{name: name, version: version})
	}

	return migrations, nil
}

func appliedVersions(ctx context.Context, conn *pgxpool.Conn) (map[int64]bool, error) {
	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int64]bool)
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}

	return applied, nil
}

func applyMigration(ctx context.Context, conn *pgxpool.Conn, version int64, migration string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", version, err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, migration); err != nil {
		return fmt.Errorf("apply migration %d: %w", version, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
		return fmt.Errorf("record migration %d: %w", version, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %d: %w", version, err)
	}

	return nil
}

func migrationVersion(name string) (int64, error) {
	base := path.Base(name)
	prefix, _, found := strings.Cut(base, "_")
	if !found {
		return 0, fmt.Errorf("migration %q has no version separator", name)
	}

	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil || version < 1 {
		return 0, fmt.Errorf("migration %q has invalid version", name)
	}
	return version, nil
}
