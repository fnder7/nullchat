package postgres

import "testing"

func TestApplyMigrationsIsIdempotent(t *testing.T) {
	pool := openTestPool(t)

	if err := ApplyMigrations(testContext, pool); err != nil {
		t.Fatal(err)
	}
	if err := ApplyMigrations(testContext, pool); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := pool.QueryRow(testContext, `SELECT count(*) FROM schema_migrations WHERE version IN (1, 2, 3)`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("version rows = %d, want 3", count)
	}

	var indexed bool
	if err := pool.QueryRow(testContext, `SELECT EXISTS (
SELECT 1 FROM pg_indexes
WHERE schemaname = current_schema()
  AND tablename = 'invites'
  AND indexname = 'invites_room_idx'
)`).Scan(&indexed); err != nil {
		t.Fatal(err)
	}
	if !indexed {
		t.Fatal("invites(room_id) index is missing")
	}
}
