package hostscope

import (
	"strings"
	"testing"

	"github.com/PatchMon/PatchMon/server-source-code/internal/models"
)

func TestPoliciesApplyToTheScopedRoleOnly(t *testing.T) {
	sql := createPolicySQL(policy{table: "hosts", using: specialPolicies["hosts"]})
	want := `CREATE POLICY patchmon_scope_v1 ON "hosts" TO patchmon_scoped USING (id = ANY ((SELECT patchmon_scope_host_ids())::text[])) WITH CHECK (id = ANY ((SELECT patchmon_scope_host_ids())::text[]))`
	if sql != want {
		t.Fatalf("got  %s\nwant %s", sql, want)
	}
}

// Rows a limited request writes must be rows it could read, except in the
// delivery log.
func TestWritesAreCheckedLikeReads(t *testing.T) {
	for table, using := range specialPolicies {
		p := policy{table: table, using: using}
		sql := createPolicySQL(p)
		if openWrites[table] {
			if !strings.HasSuffix(sql, "WITH CHECK (true)") {
				t.Errorf("%s: %s", table, sql)
			}
			continue
		}
		if !strings.HasSuffix(sql, "WITH CHECK ("+using+")") {
			t.Errorf("%s: writes not checked: %s", table, sql)
		}
	}
	if len(openWrites) != 1 || !openWrites["notification_delivery_log"] {
		t.Fatalf("open writes widened: %v", openWrites)
	}
}

// Every expression goes through the per-query helpers, never a per-row call.
func TestPoliciesComputeTheScopeOncePerQuery(t *testing.T) {
	for table, using := range specialPolicies {
		for _, fn := range []string{"patchmon_scope_host_ids()", "patchmon_scope_group_ids()"} {
			if strings.Count(using, fn) != strings.Count(using, "(SELECT "+fn+")::text[]") {
				t.Errorf("%s calls %s outside an InitPlan: %s", table, fn, using)
			}
		}
	}
}

func TestIncompatiblePermissions(t *testing.T) {
	p := &models.RolePermission{CanViewHosts: true, CanManagePatching: true, CanUseRemoteAccess: true}
	if got := IncompatibleHeld(p); len(got) != 0 {
		t.Fatalf("an operator role was refused: %v", got)
	}
	p.CanManageSettings = true
	p.CanManageUsers = true
	got := IncompatibleHeld(p)
	if strings.Join(got, ",") != "can_manage_users,can_manage_settings" {
		t.Fatalf("got %v", got)
	}
	for _, perm := range IncompatiblePermissions {
		if !strings.HasPrefix(perm, "can_manage_") {
			t.Errorf("unexpected permission %s", perm)
		}
	}
}
