// Package forkschema applies the database changes this fork makes on top of
// upstream PatchMon's migrations.
//
// Why these are not numbered migrations: golang-migrate records a single
// version per database, the highest migration applied, and only runs the
// files numbered above it. Upstream numbers its own migrations (000049 at the
// time of writing) and keeps adding 000050 and up. A fork migration would
// either take a number upstream later uses (the files collide when the fork
// syncs, and a database that ran the fork's file never runs upstream's), or
// sit far ahead (000900, say), and then every upstream migration numbered
// below it would be skipped as already applied. Either way, syncing the fork
// with upstream would leave databases behind.
//
// So the fork's changes are steps that check the catalog and only change what
// is missing. migrate.Run applies them right after the migrations, on every
// database the migrations run on: the main database when the server starts,
// and each tenant database when its pool is first opened. Each step runs in
// its own transaction, under an advisory lock, so that server instances
// starting together do not race; a step that fails rolls back entirely and is
// retried on the next start.
//
// The columns added here are also declared at the end of
// internal/sqlc/schema/schema.sql, so that sqlc sees them.
package forkschema

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// querier is what a step needs from its transaction.
type querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// beginner opens the transaction a step runs in (*pgx.Conn, a pool...).
type beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

type step struct {
	name string
	// apply makes the change if it is missing and reports whether it did.
	apply func(ctx context.Context, q querier) (bool, error)
}

// steps run in order. Append new ones; never change what a released step
// does, since databases have already run it.
var steps = []step{
	{name: "role_permissions.can_restart_hosts", apply: addCanRestartHosts},
}

// lockKey is the transaction-level advisory lock taken by every step. Any
// fixed value works, as long as nothing else uses it ("pmfork01").
const lockKey int64 = 0x706d666f726b3031

// ApplyURL connects to databaseURL and applies the steps.
func ApplyURL(ctx context.Context, databaseURL string, log *slog.Logger) error {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	return Apply(ctx, conn, log)
}

// Apply runs every step on db.
func Apply(ctx context.Context, db beginner, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	for _, s := range steps {
		changed, err := applyStep(ctx, db, s)
		if err != nil {
			return fmt.Errorf("step %s: %w", s.name, err)
		}
		if changed {
			log.Info("fork schema step applied", "step", s.name)
		}
	}
	return nil
}

func applyStep(ctx context.Context, db beginner, s step) (bool, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, err
	}
	// A no-op once the transaction is committed.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lockKey); err != nil {
		return false, err
	}
	changed, err := s.apply(ctx, tx)
	if err != nil {
		return false, err
	}
	return changed, tx.Commit(ctx)
}

// addCanRestartHosts adds role_permissions.can_restart_hosts: restarting a
// host used to come with can_manage_patching. Only when it adds the column
// does it grant the permission, to superadmin, admin and every role that can
// manage patching, so the roles that had the restart button keep it
// (host_manager by default). Once the column exists, what administrators set
// is never touched again.
func addCanRestartHosts(ctx context.Context, q querier) (bool, error) {
	var exists bool
	// regclass resolves the table the way the ALTER below does.
	if err := q.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_attribute
		WHERE attrelid = 'role_permissions'::regclass
		  AND attname = 'can_restart_hosts'
		  AND NOT attisdropped)`).Scan(&exists); err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	if _, err := q.Exec(ctx, `ALTER TABLE role_permissions
		ADD COLUMN can_restart_hosts BOOLEAN NOT NULL DEFAULT false`); err != nil {
		return false, err
	}
	if _, err := q.Exec(ctx, `UPDATE role_permissions
		SET can_restart_hosts = true
		WHERE can_manage_patching OR role IN ('superadmin', 'admin')`); err != nil {
		return false, err
	}
	return true, nil
}
