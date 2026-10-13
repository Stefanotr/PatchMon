package forkschema

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeDB records the statements of each transaction. hasColumn is what the
// catalog query answers.
type fakeDB struct {
	hasColumn bool
	failOn    string
	txs       []*fakeTx
}

func (d *fakeDB) Begin(context.Context) (pgx.Tx, error) {
	tx := &fakeTx{db: d}
	d.txs = append(d.txs, tx)
	return tx, nil
}

type fakeTx struct {
	pgx.Tx // unused methods panic
	db     *fakeDB
	stmts  []string
	commit bool
	rolled bool
}

func (t *fakeTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	t.stmts = append(t.stmts, sql)
	if t.db.failOn != "" && strings.Contains(sql, t.db.failOn) {
		return pgconn.CommandTag{}, errors.New("boom")
	}
	return pgconn.CommandTag{}, nil
}

func (t *fakeTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	t.stmts = append(t.stmts, sql)
	return boolRow(t.db.hasColumn)
}

func (t *fakeTx) Commit(context.Context) error {
	t.commit = true
	return nil
}

func (t *fakeTx) Rollback(context.Context) error {
	if !t.commit {
		t.rolled = true
	}
	return nil
}

type boolRow bool

func (r boolRow) Scan(dest ...any) error {
	*(dest[0].(*bool)) = bool(r)
	return nil
}

func (t *fakeTx) ran(fragment string) bool {
	for _, s := range t.stmts {
		if strings.Contains(s, fragment) {
			return true
		}
	}
	return false
}

func TestAddsTheColumnAndGrantsItToPatchingRoles(t *testing.T) {
	db := &fakeDB{}
	if err := Apply(context.Background(), db, nil); err != nil {
		t.Fatal(err)
	}
	tx := db.txs[0]
	if !tx.ran("pg_advisory_xact_lock") || tx.stmts[0] != "SELECT pg_advisory_xact_lock($1)" {
		t.Fatalf("the lock is not taken first: %q", tx.stmts)
	}
	if !tx.ran("ADD COLUMN can_restart_hosts BOOLEAN NOT NULL DEFAULT false") {
		t.Fatal("column not added")
	}
	if !tx.ran("SET can_restart_hosts = true") || !tx.ran("WHERE can_manage_patching OR role IN ('superadmin', 'admin')") {
		t.Fatal("permission not granted to the roles that manage patching")
	}
	if !tx.commit {
		t.Fatal("not committed")
	}
}

func TestLeavesAnExistingColumnAlone(t *testing.T) {
	db := &fakeDB{hasColumn: true}
	if err := Apply(context.Background(), db, nil); err != nil {
		t.Fatal(err)
	}
	tx := db.txs[0]
	if tx.ran("ALTER TABLE") || tx.ran("UPDATE") {
		t.Fatalf("an existing column was changed: %q", tx.stmts)
	}
}

func TestAFailedGrantRollsTheColumnBack(t *testing.T) {
	db := &fakeDB{failOn: "UPDATE role_permissions"}
	err := Apply(context.Background(), db, nil)
	if err == nil || !strings.Contains(err.Error(), "role_permissions.can_restart_hosts") {
		t.Fatalf("err = %v", err)
	}
	tx := db.txs[0]
	if tx.commit || !tx.rolled {
		t.Fatal("a half-applied step was committed")
	}
}

func TestCreatesTheRemoteRestartTable(t *testing.T) {
	db := &fakeDB{}
	if err := Apply(context.Background(), db, nil); err != nil {
		t.Fatal(err)
	}
	if len(db.txs) != len(steps) {
		t.Fatalf("%d transactions for %d steps", len(db.txs), len(steps))
	}
	tx := db.txs[1]
	if tx.stmts[0] != "SELECT pg_advisory_xact_lock($1)" {
		t.Fatalf("the lock is not taken first: %q", tx.stmts)
	}
	if !tx.ran("CREATE TABLE host_remote_restart") || !tx.ran("REFERENCES hosts(id) ON DELETE CASCADE") {
		t.Fatalf("table not created: %q", tx.stmts)
	}
	if !tx.commit {
		t.Fatal("not committed")
	}
}

func TestLeavesAnExistingRemoteRestartTableAlone(t *testing.T) {
	db := &fakeDB{hasColumn: true}
	if err := Apply(context.Background(), db, nil); err != nil {
		t.Fatal(err)
	}
	if tx := db.txs[1]; tx.ran("CREATE TABLE") {
		t.Fatalf("an existing table was recreated: %q", tx.stmts)
	}
}
