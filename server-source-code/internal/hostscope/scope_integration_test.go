package hostscope_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/PatchMon/PatchMon/server-source-code/internal/config"
	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/PatchMon/PatchMon/server-source-code/internal/hostscope"
	"github.com/PatchMon/PatchMon/server-source-code/internal/middleware"
	"github.com/PatchMon/PatchMon/server-source-code/internal/pgtest"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.Main(m))
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// openOneConn opens a pool of a single connection, so that limited and
// unlimited queries share it and the reset between them is exercised.
func openOneConn(t *testing.T) *database.DB {
	t.Helper()
	d, err := database.NewFromURL(context.Background(), pgtest.URL(t), 1, 1, &config.Config{DBConnectTimeout: 5})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d
}

var seedOnce sync.Once

// seed: groups gA and gB, host h1 in gA, h2 in gB, h3 in none, and rows of
// each kind for h1 and h2. The migrations, the fork steps and Reconcile ran
// when pgtest created the database.
func seed(t *testing.T, d *database.DB) {
	t.Helper()
	var err error
	seedOnce.Do(func() {
		for _, s := range []string{
			`INSERT INTO host_groups (id, name, updated_at) VALUES ('gA', 'A', now()), ('gB', 'B', now())`,
			`INSERT INTO hosts (id, friendly_name, os_type, os_version, updated_at, api_id, api_key) VALUES
				('h1', 'web-1', 'linux', '12', now(), 'api1', 'k1'),
				('h2', 'db-1', 'linux', '12', now(), 'api2', 'k2'),
				('h3', 'lonely', 'linux', '12', now(), 'api3', 'k3')`,
			`INSERT INTO host_group_memberships (id, host_id, host_group_id) VALUES ('m1', 'h1', 'gA'), ('m2', 'h2', 'gB')`,
			`INSERT INTO packages (id, name, updated_at) VALUES ('p1', 'openssl', now()), ('p2', 'postgresql', now())`,
			`INSERT INTO host_packages (id, host_id, package_id, current_version) VALUES
				('hp1', 'h1', 'p1', '1'), ('hp2', 'h2', 'p2', '1'), ('hp3', 'h2', 'p1', '1')`,
			`REFRESH MATERIALIZED VIEW mv_package_stats`,
			`INSERT INTO patch_runs (id, host_id, job_id, patch_type, status) VALUES
				('r1', 'h1', 'j1', 'patch_all', 'completed'), ('r2', 'h2', 'j2', 'patch_all', 'completed')`,
			`INSERT INTO compliance_profiles (id, name, type, updated_at) VALUES ('cp', 'cis', 'openscap', now())`,
			`INSERT INTO compliance_rules (id, profile_id, rule_ref, title) VALUES ('cr', 'cp', 'r', 't')`,
			`INSERT INTO compliance_scans (id, host_id, profile_id, started_at, status, updated_at) VALUES
				('s1', 'h1', 'cp', now(), 'completed', now()), ('s2', 'h2', 'cp', now(), 'completed', now())`,
			`INSERT INTO compliance_results (id, scan_id, rule_id, status) VALUES ('c1', 's1', 'cr', 'pass'), ('c2', 's2', 'cr', 'fail')`,
			`INSERT INTO alerts (id, type, severity, title, message, metadata, updated_at) VALUES
				('a1', 'host_down', 'critical', 'web-1 down', 'm', '{"host_id":"h1"}', now()),
				('a2', 'host_down', 'critical', 'db-1 down', 'm', '{"host_id":"h2"}', now()),
				('a3', 'server_update', 'informational', 'update', 'm', '{}', now())`,
			`INSERT INTO repositories (id, name, url, distribution, components, repo_type, updated_at) VALUES
				('repo1', 'r1', 'http://a', 'bookworm', 'main', 'deb', now()), ('repo2', 'r2', 'http://b', 'bookworm', 'main', 'deb', now())`,
			`INSERT INTO host_repositories (id, host_id, repository_id) VALUES ('hr1', 'h1', 'repo1'), ('hr2', 'h2', 'repo2')`,
			`INSERT INTO patch_policies (id, name, patch_delay_type) VALUES ('pol', 'p', 'immediate')`,
			`INSERT INTO patch_policy_assignments (id, patch_policy_id, target_type, target_id) VALUES
				('pa1', 'pol', 'host', 'h1'), ('pa2', 'pol', 'host', 'h2'), ('pa3', 'pol', 'host_group', 'gA'), ('pa4', 'pol', 'host_group', 'gB')`,
			`INSERT INTO system_statistics (id, unique_packages_count, unique_security_count, total_packages, total_hosts, hosts_needing_updates)
				VALUES ('st', 2, 0, 3, 3, 0)`,
			`INSERT INTO role_permissions (id, role, can_view_hosts, can_manage_patching, updated_at)
				VALUES ('rp-ops', 'ops_a', true, true, now())`,
		} {
			if _, err = d.Exec(context.Background(), s); err != nil {
				return
			}
		}
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// ids returns the first column of query, sorted.
func ids(t *testing.T, ctx context.Context, d *database.DB, query string) []string {
	t.Helper()
	rows, err := d.Raw(ctx, query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	slices.Sort(got)
	return got
}

func expect(t *testing.T, ctx context.Context, d *database.DB, query string, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	got := ids(t, ctx, d, query)
	if got == nil {
		got = []string{}
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s: got %v, want %v", query, got, want)
	}
}

func TestScopingIsSetUp(t *testing.T) {
	d := openOneConn(t)
	st, err := hostscope.Check(context.Background(), d.Pool())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Available {
		t.Fatalf("not available: %s", st.Reason)
	}
	// A second run changes nothing and leaves it working.
	hostscope.Reconcile(context.Background(), d.Pool(), quiet)
	if st, err := hostscope.Check(context.Background(), d.Pool()); err != nil || !st.Available {
		t.Fatalf("after a second reconcile: %+v %v", st, err)
	}
}

func TestALimitedRequestSeesItsGroupsOnly(t *testing.T) {
	d := openOneConn(t)
	seed(t, d)
	all := context.Background()
	a := database.WithHostScope(all, []string{"gA"})

	// Unlimited first, on the same connection: the cached statement must
	// follow the role in force.
	expect(t, all, d, `SELECT id FROM hosts`, "h1", "h2", "h3")

	expect(t, a, d, `SELECT id FROM hosts`, "h1")
	expect(t, a, d, `SELECT id FROM host_groups`, "gA")
	expect(t, a, d, `SELECT id FROM host_group_memberships`, "m1")
	expect(t, a, d, `SELECT id FROM host_packages`, "hp1")
	expect(t, a, d, `SELECT id FROM packages`, "p1")
	expect(t, a, d, `SELECT id FROM patch_runs`, "r1")
	expect(t, a, d, `SELECT id FROM compliance_scans`, "s1")
	expect(t, a, d, `SELECT id FROM compliance_results`, "c1")
	expect(t, a, d, `SELECT id FROM alerts`, "a1", "a3")
	expect(t, a, d, `SELECT id FROM repositories`, "repo1")
	expect(t, a, d, `SELECT id FROM patch_policy_assignments`, "pa1", "pa3")
	expect(t, a, d, `SELECT id FROM system_statistics`)
	// Joins and counts follow.
	expect(t, a, d, `SELECT count(*)::text FROM hosts h JOIN host_packages hp ON hp.host_id = h.id`, "1")
	// The materialised view is answered by its scoped twin: openssl is on
	// h1 and h2, but only h1 is visible.
	expect(t, a, d, `SELECT package_id || '=' || total_installs FROM mv_package_stats`, "p1=1")
	expect(t, all, d, `SELECT package_id || '=' || total_installs FROM mv_package_stats`, "p1=2", "p2=1")
	// Global settings stay readable.
	expect(t, a, d, `SELECT role FROM role_permissions WHERE role = 'ops_a'`, "ops_a")

	// And back: the same connection sees everything again.
	expect(t, all, d, `SELECT id FROM hosts`, "h1", "h2", "h3")
	expect(t, all, d, `SELECT id FROM patch_runs`, "r1", "r2")

	// A limited role whose groups are all gone sees nothing.
	expect(t, database.WithHostScope(all, nil), d, `SELECT id FROM hosts`)
}

func TestALimitedRequestCannotWriteOtherHosts(t *testing.T) {
	d := openOneConn(t)
	seed(t, d)
	all := context.Background()
	a := database.WithHostScope(all, []string{"gA"})

	if n, err := d.Exec(a, `UPDATE hosts SET notes = 'x' WHERE id = 'h2'`); err != nil || n != 0 {
		t.Fatalf("update of a hidden host: %d rows, %v", n, err)
	}
	if n, err := d.Exec(a, `DELETE FROM patch_runs WHERE id = 'r2'`); err != nil || n != 0 {
		t.Fatalf("delete of a hidden run: %d rows, %v", n, err)
	}
	for _, s := range []string{
		`INSERT INTO patch_runs (id, host_id, job_id, patch_type, status) VALUES ('r9', 'h2', 'j9', 'patch_all', 'queued')`,
		`INSERT INTO host_group_memberships (id, host_id, host_group_id) VALUES ('m9', 'h1', 'gB')`,
		`INSERT INTO host_groups (id, name, updated_at) VALUES ('gC', 'C', now())`,
		`INSERT INTO system_statistics (id, unique_packages_count, unique_security_count, total_packages, total_hosts, hosts_needing_updates) VALUES ('st2', 1, 0, 1, 1, 0)`,
	} {
		_, err := d.Exec(a, s)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("%s: want a row security error, got %v", s, err)
		}
	}
	// Its own hosts stay writable.
	if _, err := d.Exec(a, `INSERT INTO patch_runs (id, host_id, job_id, patch_type, status) VALUES ('r8', 'h1', 'j8', 'patch_all', 'queued')`); err != nil {
		t.Fatalf("insert for a visible host: %v", err)
	}
	if n, err := d.Exec(a, `DELETE FROM patch_runs WHERE id = 'r8'`); err != nil || n != 1 {
		t.Fatalf("delete of its own run: %d, %v", n, err)
	}
	// The originals of views are out of reach when named explicitly.
	if _, err := d.Exec(a, `SELECT 1 FROM public.mv_package_stats`); err == nil {
		t.Error("the shared materialised view is readable by a limited request")
	}
	expect(t, all, d, `SELECT id FROM patch_runs`, "r1", "r2")
}

// Every table pointing at a scoped table must be scoped itself, or reviewed
// here: a table added upstream that links to hosts without a host_id column
// fails this test until its policy is written.
func TestEveryTableLinkedToHostsIsScoped(t *testing.T) {
	d := openOneConn(t)
	reviewed := map[string]string{
		"auto_enrollment_tokens": "settings: roles holding can_manage_settings are never limited",
	}
	rows, err := d.Raw(context.Background(), `
		SELECT DISTINCT src.relname
		FROM pg_constraint con
		JOIN pg_class src ON src.oid = con.conrelid
		JOIN pg_class dst ON dst.oid = con.confrelid
		JOIN pg_namespace n ON n.oid = src.relnamespace
		WHERE con.contype = 'f' AND n.nspname = 'public'
		  AND dst.relrowsecurity AND NOT src.relrowsecurity`)
	if err != nil {
		t.Fatal(err)
	}
	unscoped, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range unscoped {
		if _, ok := reviewed[table]; !ok {
			t.Errorf("table %s references a host table but has no scope policy (internal/hostscope/policies.go)", table)
		}
	}
}

func TestMiddlewareLimitsTheRequest(t *testing.T) {
	d := openOneConn(t)
	seed(t, d)
	scopes := hostscope.NewStore(d)
	if err := scopes.Limit(context.Background(), "ops_a", []string{"gA"}, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scopes.Unlimit(context.Background(), "ops_a") })

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			role := req.Header.Get("X-Test-Role")
			next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), middleware.UserRoleKey, role)))
		})
	})
	r.Use(middleware.HostScope(scopes, quiet))
	r.Get("/hosts", func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.WriteString(w, strings.Join(ids(t, req.Context(), d, `SELECT id FROM hosts`), ","))
	})
	r.With(middleware.RequireAllHosts).Post("/host-groups", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	r.With(middleware.RequireVisibleHost(scopes, "hostId")).Get("/hosts/{hostId}/queue", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	do := func(method, path, role string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("X-Test-Role", role)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if rec := do("GET", "/hosts", "ops_a"); rec.Body.String() != "h1" {
		t.Errorf("limited role sees %q", rec.Body.String())
	}
	if rec := do("GET", "/hosts", "admin"); rec.Body.String() != "h1,h2,h3" {
		t.Errorf("admin sees %q", rec.Body.String())
	}
	if rec := do("POST", "/host-groups", "ops_a"); rec.Code != http.StatusForbidden {
		t.Errorf("limited role creating a group: %d", rec.Code)
	}
	if rec := do("POST", "/host-groups", "admin"); rec.Code != http.StatusCreated {
		t.Errorf("admin creating a group: %d", rec.Code)
	}
	if rec := do("GET", "/hosts/h2/queue", "ops_a"); rec.Code != http.StatusNotFound {
		t.Errorf("limited role on a hidden host: %d", rec.Code)
	}
	if rec := do("GET", "/hosts/h1/queue", "ops_a"); rec.Code != http.StatusOK {
		t.Errorf("limited role on its host: %d", rec.Code)
	}
	if rec := do("GET", "/hosts/h2/queue", "admin"); rec.Code != http.StatusOK {
		t.Errorf("admin on any host: %d", rec.Code)
	}
}

func TestALimitedRoleCannotBeGivenFleetWidePermissions(t *testing.T) {
	d := openOneConn(t)
	seed(t, d)
	scopes := hostscope.NewStore(d)
	if err := scopes.Limit(context.Background(), "ops_a", []string{"gA"}, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scopes.Unlimit(context.Background(), "ops_a") })

	r := chi.NewRouter()
	r.With(middleware.GuardLimitedRole(scopes)).Put("/permissions/roles/{role}", func(w http.ResponseWriter, req *http.Request) {
		// The handler must still get the body.
		body, _ := io.ReadAll(req.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
	put := func(role, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PUT", "/permissions/roles/"+role, strings.NewReader(body))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	for _, body := range []string{
		`{"can_manage_users": true}`,
		// encoding/json matches field names case-insensitively.
		`{"CAN_MANAGE_SETTINGS": true}`,
		// The handler decodes the first JSON value and ignores the rest.
		`{"can_manage_billing": true} trailing`,
		`not json`,
	} {
		if rec := put("ops_a", body); rec.Code != http.StatusBadRequest {
			t.Errorf("limited role given %s: %d", body, rec.Code)
		}
	}
	ok := `{"can_view_hosts": true, "can_manage_users": false}`
	if rec := put("ops_a", ok); rec.Code != http.StatusOK || rec.Body.String() != ok {
		t.Errorf("limited role given host permissions: %d %q", rec.Code, rec.Body.String())
	}
	if rec := put("ops_b", `{"can_manage_users": true}`); rec.Code != http.StatusOK {
		t.Errorf("unlimited role: %d", rec.Code)
	}
}
