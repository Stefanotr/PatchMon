// Package hostscope limits users to the hosts of some host groups (fork).
//
// A role can be limited to a list of host groups (table role_host_scopes). Its
// users then see and act on the hosts of those groups only: everywhere a host
// shows up, its packages, runs, scans, containers, alerts and so on.
//
// The limit is enforced by PostgreSQL rather than by each route. A limited
// request runs its queries as the database role ScopedRole (see
// internal/database/hostscope.go), and row-level security policies on every
// host table hide the rows of other hosts. A route that forgets to filter
// cannot leak, and upstream's queries stay untouched.
//
// Reconcile creates the role, its grants and the policies; it runs after the
// migrations on every database, at each start, so that tables added by later
// migrations are covered too. Check tells whether a database is fully set up;
// a limited request on a database that is not is refused, never run unlimited.
package hostscope

import (
	"context"
	"fmt"
	"sort"

	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PolicyName is the name of every policy this package creates. Change it (v2,
// v3...) whenever a policy expression changes: Reconcile replaces the policies
// of other names, it does not compare expressions.
const PolicyName = "patchmon_scope_v1"

// The visible hosts and groups, computed once per query (InitPlan).
const (
	visibleHosts  = "(SELECT patchmon_scope_host_ids())::text[]"
	visibleGroups = "(SELECT patchmon_scope_group_ids())::text[]"
)

// specialPolicies are the tables that do not simply follow a host_id or
// host_group_id column. A table missing from the database is skipped.
var specialPolicies = map[string]string{
	"hosts":       "id = ANY (" + visibleHosts + ")",
	"host_groups": "id = ANY (" + visibleGroups + ")",
	// A visible host can also belong to groups out of scope: hide those links.
	"host_group_memberships": "host_id = ANY (" + visibleHosts + ") AND host_group_id = ANY (" + visibleGroups + ")",
	"compliance_results":     "EXISTS (SELECT 1 FROM compliance_scans s WHERE s.id = compliance_results.scan_id)",
	// Host alerts carry their host in metadata; the others are not about a host.
	"alerts":        "metadata->>'host_id' IS NULL OR metadata->>'host_id' = ANY (" + visibleHosts + ")",
	"alert_history": "EXISTS (SELECT 1 FROM alerts a WHERE a.id = alert_history.alert_id)",
	"patch_policy_assignments": "(target_type = 'host' AND target_id = ANY (" + visibleHosts + "))" +
		" OR (target_type = 'host_group' AND target_id = ANY (" + visibleGroups + "))",
	// Catalogs shared by hosts: what at least one visible host has.
	"packages":     "EXISTS (SELECT 1 FROM host_packages hp WHERE hp.package_id = packages.id)",
	"repositories": "EXISTS (SELECT 1 FROM host_repositories hr WHERE hr.repository_id = repositories.id)",
	"docker_images": "EXISTS (SELECT 1 FROM docker_host_images d WHERE d.image_id = docker_images.id)" +
		" OR EXISTS (SELECT 1 FROM docker_containers c WHERE c.image_id = docker_images.id)",
	"docker_image_updates": "EXISTS (SELECT 1 FROM docker_images i WHERE i.id = docker_image_updates.image_id)",
	// Fleet-wide figures and delivered notifications: none.
	"system_statistics":         "false",
	"notification_delivery_log": "false",
}

// openWrites are the tables a limited request may still write freely while
// reading stays filtered: the delivery log, written after a limited user's
// action. Everywhere else a row it writes must be one it could read, so that,
// say, fleet statistics computed by mistake within a limited request fail
// rather than store a partial total.
var openWrites = map[string]bool{
	"notification_delivery_log": true,
}

// policy is the row filter of one table.
type policy struct {
	table string
	using string
}

// check is the condition rows written by a limited request must meet.
func (p policy) check() string {
	if openWrites[p.table] {
		return "true"
	}
	return p.using
}

// queryer is what reading the catalog needs (*pgx.Conn, pgx.Tx, a pool).
type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// expectedPolicies lists the policy each host table must have: the special
// tables, every other table with a host_id column, and every table with a
// host_group_id column but no host_id.
func expectedPolicies(ctx context.Context, q queryer) ([]policy, error) {
	rows, err := q.Query(ctx, `
		SELECT c.relname,
		       bool_or(a.attname = 'host_id'),
		       bool_or(a.attname = 'host_group_id')
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
		GROUP BY c.relname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []policy
	for rows.Next() {
		var table string
		var hostID, groupID bool
		if err := rows.Scan(&table, &hostID, &groupID); err != nil {
			return nil, err
		}
		switch using, special := specialPolicies[table]; {
		case special:
			out = append(out, policy{table, using})
		case hostID:
			out = append(out, policy{table, "host_id = ANY (" + visibleHosts + ")"})
		case groupID:
			out = append(out, policy{table, "host_group_id = ANY (" + visibleGroups + ")"})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].table < out[j].table })
	return out, nil
}

// functionsSQL defines the visible groups (from the session setting) and the
// visible hosts. The hosts function reads memberships as their owner, past
// their own policy.
var functionsSQL = []string{
	`CREATE OR REPLACE FUNCTION patchmon_scope_group_ids() RETURNS text[]
	LANGUAGE sql STABLE AS $f$
		SELECT coalesce(string_to_array(nullif(current_setting('` + database.ScopeGroupsSetting + `', true), ''), ','), '{}'::text[])
	$f$`,
	`CREATE OR REPLACE FUNCTION patchmon_scope_host_ids() RETURNS text[]
	LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $f$
		SELECT coalesce(array_agg(DISTINCT m.host_id), '{}'::text[])
		FROM public.host_group_memberships m
		WHERE m.host_group_id = ANY (public.patchmon_scope_group_ids())
	$f$`,
	`REVOKE ALL ON FUNCTION patchmon_scope_host_ids() FROM PUBLIC`,
	`GRANT EXECUTE ON FUNCTION patchmon_scope_host_ids() TO ` + database.ScopedRole,
}

// grantsSQL gives the scoped role the same data access as the server, minus
// the rows the policies hide; tables created later get it too.
var grantsSQL = []string{
	`GRANT USAGE ON SCHEMA public TO ` + database.ScopedRole,
	`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO ` + database.ScopedRole,
	`GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO ` + database.ScopedRole,
	`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO ` + database.ScopedRole,
	`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO ` + database.ScopedRole,
}

// createPolicySQL is the statement creating p.
func createPolicySQL(p policy) string {
	t := pgx.Identifier{p.table}.Sanitize()
	return fmt.Sprintf("CREATE POLICY %s ON %s TO %s USING (%s) WITH CHECK (%s)",
		PolicyName, t, database.ScopedRole, p.using, p.check())
}
