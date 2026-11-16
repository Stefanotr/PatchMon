package hostscope

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/jackc/pgx/v5"
)

// lockKey serialises Reconcile across server instances ("pmscope1").
const lockKey int64 = 0x706d73636f706531

// canSwitchSQL tells whether the current user may switch to the role r
// (SELECT ... FROM pg_roles r): from PostgreSQL 16 on, a role's creator is a
// member without the SET option unless granted it.
const canSwitchSQL = `CASE WHEN current_setting('is_superuser') = 'on' THEN true
	WHEN current_setting('server_version_num')::int >= 160000 THEN pg_has_role(current_user, r.oid, 'SET')
	ELSE pg_has_role(current_user, r.oid, 'MEMBER') END`

// beginner opens the transaction Reconcile runs in (*pgx.Conn, a pool...).
type beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Reconcile sets host scoping up on a database: the scoped role, its grants,
// the scope functions and a policy on every host table. It changes only what
// is missing, in one transaction. It never fails the start: on error nothing
// is changed, the error is logged, and limited users are refused on this
// database until a later start succeeds (see Check).
func Reconcile(ctx context.Context, db beginner, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	changes, err := reconcile(ctx, db)
	if err != nil {
		log.Warn("host scoping is not set up on this database; users limited to host groups are refused until it is", "error", err)
		return
	}
	for _, c := range changes {
		log.Info("host scoping set up", "change", c)
	}
}

func reconcile(ctx context.Context, db beginner) ([]string, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lockKey); err != nil {
		return nil, err
	}
	// Enabling row security takes the table's strongest lock: never queue
	// behind a long transaction for more than a moment.
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '5s'"); err != nil {
		return nil, err
	}
	var changes []string

	var roleExists, superuser bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1),
		current_setting('is_superuser') = 'on'`, database.ScopedRole).Scan(&roleExists, &superuser); err != nil {
		return nil, err
	}
	if !roleExists {
		// Roles are shared by every database of the cluster: another tenant
		// may create it at the same time.
		if _, err := tx.Exec(ctx, `DO $$ BEGIN
			CREATE ROLE `+database.ScopedRole+` NOLOGIN;
		EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
		END $$`); err != nil {
			return nil, fmt.Errorf("create role %s (the database user needs CREATEROLE): %w", database.ScopedRole, err)
		}
		changes = append(changes, "role "+database.ScopedRole)
	}
	if !superuser {
		var canSwitch bool
		if err := tx.QueryRow(ctx, `SELECT `+canSwitchSQL+` FROM pg_roles r WHERE r.rolname = $1`,
			database.ScopedRole).Scan(&canSwitch); err != nil {
			return nil, err
		}
		if !canSwitch {
			if _, err := tx.Exec(ctx, `GRANT `+database.ScopedRole+` TO CURRENT_USER`); err != nil {
				return nil, fmt.Errorf("grant role %s to the database user: %w", database.ScopedRole, err)
			}
			changes = append(changes, "membership of "+database.ScopedRole)
		}
	}
	for _, s := range grantsSQL {
		if _, err := tx.Exec(ctx, s); err != nil {
			return nil, fmt.Errorf("grants: %w", err)
		}
	}
	for _, s := range functionsSQL {
		if _, err := tx.Exec(ctx, s); err != nil {
			return nil, fmt.Errorf("scope functions: %w", err)
		}
	}

	policies, err := expectedPolicies(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, p := range policies {
		c, err := reconcileTable(ctx, tx, p)
		if err != nil {
			return nil, fmt.Errorf("table %s: %w", p.table, err)
		}
		changes = append(changes, c...)
	}
	c, err := reconcileViews(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("views: %w", err)
	}
	changes = append(changes, c...)
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return changes, nil
}

// publicViews lists the views and materialised views of the public schema.
func publicViews(ctx context.Context, q queryer) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT c.relname FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('v', 'm') ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// reconcileViews gives every public view a twin in database.ScopeSchema that
// runs as the caller, so that a limited session (whose search path puts that
// schema first) gets rows its policies filter: a materialised view holds
// rows computed for everyone, and a plain view runs as its owner. The twins
// are rebuilt at each start, from the current definitions.
func reconcileViews(ctx context.Context, q queryer) ([]string, error) {
	views, err := publicViews(ctx, q)
	if err != nil {
		return nil, err
	}
	schema := pgx.Identifier{database.ScopeSchema}.Sanitize()
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`,
		database.ScopeSchema).Scan(&exists); err != nil {
		return nil, err
	}
	var changes []string
	if !exists {
		if _, err := q.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
			return nil, err
		}
		changes = append(changes, "schema "+database.ScopeSchema)
	}
	if _, err := q.Exec(ctx, "GRANT USAGE ON SCHEMA "+schema+" TO "+database.ScopedRole); err != nil {
		return nil, err
	}
	if len(views) > 0 {
		var version int
		if err := q.QueryRow(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&version); err != nil {
			return nil, err
		}
		if version < 150000 {
			return nil, errors.New("needs PostgreSQL 15 or later (security_invoker views)")
		}
	}
	// Twins of views that no longer exist go.
	rows, err := q.Query(ctx, `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind = 'v'`, database.ScopeSchema)
	if err != nil {
		return nil, err
	}
	twins, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	for _, v := range views {
		keep[v] = true
	}
	for _, t := range twins {
		if !keep[t] {
			if _, err := q.Exec(ctx, "DROP VIEW "+schema+"."+pgx.Identifier{t}.Sanitize()); err != nil {
				return nil, err
			}
			changes = append(changes, "dropped twin of "+t)
		}
	}
	for _, v := range views {
		var def string
		if err := q.QueryRow(ctx, `SELECT pg_get_viewdef(format('public.%I', $1::text)::regclass, true)`, v).Scan(&def); err != nil {
			return nil, err
		}
		def = strings.TrimSuffix(strings.TrimSpace(def), ";")
		twin := schema + "." + pgx.Identifier{v}.Sanitize()
		// The original stays out of reach: a query naming public.v
		// explicitly fails for a limited user instead of leaking.
		stmts := []string{"REVOKE ALL ON " + pgx.Identifier{"public", v}.Sanitize() + " FROM " + database.ScopedRole}
		var current *string
		if err := q.QueryRow(ctx, `SELECT pg_get_viewdef(to_regclass(format('%I.%I', $1::text, $2::text)), true)`,
			database.ScopeSchema, v).Scan(&current); err != nil {
			return nil, err
		}
		if current == nil || strings.TrimSuffix(strings.TrimSpace(*current), ";") != def {
			stmts = append(stmts,
				"DROP VIEW IF EXISTS "+twin,
				"CREATE VIEW "+twin+" WITH (security_invoker = true) AS "+def)
			changes = append(changes, "twin of "+v)
		}
		stmts = append(stmts, "GRANT SELECT ON "+twin+" TO "+database.ScopedRole)
		for _, stmt := range stmts {
			if _, err := q.Exec(ctx, stmt); err != nil {
				return nil, fmt.Errorf("twin of %s: %w", v, err)
			}
		}
	}
	return changes, nil
}

// tableState is what the catalog says of a table's row security.
type tableState struct {
	rowSecurity bool
	policies    []string // this package's policies, any version
}

func readTableState(ctx context.Context, q queryer, table string) (tableState, error) {
	var st tableState
	err := q.QueryRow(ctx, `SELECT c.relrowsecurity,
		coalesce((SELECT array_agg(p.polname::text) FROM pg_policy p
			WHERE p.polrelid = c.oid AND p.polname LIKE 'patchmon\_scope\_%'), '{}')
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relname = $1`, table).Scan(&st.rowSecurity, &st.policies)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, errors.New("table not found")
	}
	return st, err
}

func reconcileTable(ctx context.Context, q queryer, p policy) ([]string, error) {
	st, err := readTableState(ctx, q, p.table)
	if err != nil {
		return nil, err
	}
	t := pgx.Identifier{p.table}.Sanitize()
	var changes []string
	has := false
	for _, name := range st.policies {
		if name == PolicyName {
			has = true
			continue
		}
		// An older version of the policy.
		if _, err := q.Exec(ctx, "DROP POLICY "+pgx.Identifier{name}.Sanitize()+" ON "+t); err != nil {
			return nil, err
		}
		changes = append(changes, "dropped "+name+" on "+p.table)
	}
	if !has {
		if _, err := q.Exec(ctx, createPolicySQL(p)); err != nil {
			return nil, err
		}
		changes = append(changes, "policy on "+p.table)
	}
	if !st.rowSecurity {
		if _, err := q.Exec(ctx, "ALTER TABLE "+t+" ENABLE ROW LEVEL SECURITY"); err != nil {
			return nil, err
		}
		changes = append(changes, "row security on "+p.table)
	}
	return changes, nil
}

// Status says whether limited users can be served on a database.
type Status struct {
	Available bool `json:"available"`
	// Reason is why not, for administrators.
	Reason string `json:"reason,omitempty"`
}

// Check reports whether host scoping is fully set up on the database q
// queries: the role exists and the server may switch to it, and every host
// table has row security on with the current policy. It is read-only.
func Check(ctx context.Context, q queryer) (Status, error) {
	var roleOK, functionsOK bool
	if err := q.QueryRow(ctx, `SELECT
		coalesce((SELECT `+canSwitchSQL+` FROM pg_roles r WHERE r.rolname = $1), false),
		to_regprocedure('patchmon_scope_host_ids()') IS NOT NULL
			AND to_regprocedure('patchmon_scope_group_ids()') IS NOT NULL`,
		database.ScopedRole).Scan(&roleOK, &functionsOK); err != nil {
		return Status{}, err
	}
	if !roleOK {
		return Status{Reason: "the database role " + database.ScopedRole + " is missing or the PatchMon database user cannot switch to it"}, nil
	}
	if !functionsOK {
		return Status{Reason: "the scope functions are missing"}, nil
	}
	policies, err := expectedPolicies(ctx, q)
	if err != nil {
		return Status{}, err
	}
	for _, p := range policies {
		st, err := readTableState(ctx, q, p.table)
		if err != nil {
			return Status{}, err
		}
		has := false
		for _, name := range st.policies {
			has = has || name == PolicyName
		}
		if !st.rowSecurity || !has {
			return Status{Reason: "table " + p.table + " has no scope policy yet; restart the server to set it up"}, nil
		}
	}
	views, err := publicViews(ctx, q)
	if err != nil {
		return Status{}, err
	}
	for _, v := range views {
		var twin bool
		if err := q.QueryRow(ctx, `SELECT to_regclass(format('%I.%I', $1::text, $2::text)) IS NOT NULL`,
			database.ScopeSchema, v).Scan(&twin); err != nil {
			return Status{}, err
		}
		if !twin {
			return Status{Reason: "view " + v + " has no scoped twin yet; restart the server to set it up"}, nil
		}
	}
	return Status{Available: true}, nil
}
