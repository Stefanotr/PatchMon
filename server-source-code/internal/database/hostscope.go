package database

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Host scoping (fork): a user whose role is limited to some host groups
// queries the database as ScopedRole, with the ids of those groups in the
// ScopeGroupsSetting session setting. Row-level security policies on the host
// tables, created by internal/forkschema, then hide every row of a host outside
// those groups, whatever the query. Every other session runs as the pool's own
// user, which the policies do not apply to.
//
// This guards against a route that forgets to filter, not against SQL
// injection: a session can always RESET ROLE.
const (
	ScopedRole         = "patchmon_scoped"
	ScopeGroupsSetting = "patchmon.scope_groups"
	// ScopeSchema holds, for each view of the public schema, a twin that
	// runs as the caller: row security does not reach into a materialised
	// view, nor into a view run as its owner. It comes first in a limited
	// session's search path.
	ScopeSchema = "patchmon_scope"
)

type hostScopeKey struct{}

// WithHostScope returns ctx limited to the host groups groupIDs: queries made
// with it see only the hosts of those groups. No group means no host at all.
func WithHostScope(ctx context.Context, groupIDs []string) context.Context {
	ids := make([]string, len(groupIDs))
	copy(ids, groupIDs)
	return context.WithValue(ctx, hostScopeKey{}, ids)
}

// HostScope returns the host groups ctx is limited to, and whether it is
// limited at all.
func HostScope(ctx context.Context) ([]string, bool) {
	ids, ok := ctx.Value(hostScopeKey{}).([]string)
	return ids, ok
}

// scopedConns holds the connections currently switched to ScopedRole, so that
// an unscoped query on one of them switches it back first.
var scopedConns sync.Map

// prepareHostScope runs before every connection is handed out (PrepareConn).
// It fails closed: if the scope of a limited request cannot be applied, the
// connection is dropped and the query fails rather than run unlimited.
func prepareHostScope(ctx context.Context, conn *pgx.Conn) (bool, error) {
	groups, scoped := HostScope(ctx)
	if !scoped {
		if _, dirty := scopedConns.Load(conn); !dirty {
			return true, nil
		}
		// No arguments: one simple-protocol round trip for the three.
		if _, err := conn.Exec(ctx, "RESET ROLE; RESET search_path; RESET "+ScopeGroupsSetting); err != nil {
			// Drop it; the pool hands out another one.
			return false, nil
		}
		scopedConns.Delete(conn)
		return true, nil
	}
	for _, id := range groups {
		if id == "" || strings.Contains(id, ",") {
			return false, fmt.Errorf("host scope: invalid host group id %q", id)
		}
	}
	// Marked first: a half-applied switch is reset before any unscoped use.
	scopedConns.Store(conn, struct{}{})
	if _, err := conn.Exec(ctx, `SELECT set_config($1, $2, false), set_config('search_path', $3, false),
		set_config('role', $4, false)`,
		ScopeGroupsSetting, strings.Join(groups, ","), ScopeSchema+", public", ScopedRole); err != nil {
		return false, fmt.Errorf("host scope: %w", err)
	}
	return true, nil
}

// withHostScope installs the host scope hooks on a pool configuration, after
// any hook already set.
func withHostScope(poolCfg *pgxpool.Config) {
	if poolCfg == nil {
		return
	}
	prev := poolCfg.PrepareConn
	poolCfg.PrepareConn = func(ctx context.Context, conn *pgx.Conn) (bool, error) {
		if prev != nil {
			if ok, err := prev(ctx, conn); !ok || err != nil {
				return ok, err
			}
		}
		return prepareHostScope(ctx, conn)
	}
	prevClose := poolCfg.BeforeClose
	poolCfg.BeforeClose = func(conn *pgx.Conn) {
		scopedConns.Delete(conn)
		if prevClose != nil {
			prevClose(conn)
		}
	}
}

// Pool returns the underlying pool, for the host scope checks
// (internal/hostscope), which read the catalog with it.
func (d *DB) Pool() *pgxpool.Pool {
	return d.pool
}
