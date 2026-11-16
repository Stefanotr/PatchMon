package hostscope

import "github.com/PatchMon/PatchMon/server-source-code/internal/models"

// IncompatiblePermissions are the permissions a limited role cannot hold:
// each acts on every host, or would let the role widen its own limit.
var IncompatiblePermissions = []string{
	"can_manage_users",
	"can_manage_superusers",
	"can_manage_settings",
	"can_manage_notifications",
	"can_manage_automation",
	"can_manage_billing",
}

// FullAccessRoles can never be limited.
var FullAccessRoles = map[string]bool{"superadmin": true, "admin": true}

// MaxGroups bounds the host groups of one role.
const MaxGroups = 200

// IncompatibleHeld returns the incompatible permissions p holds.
func IncompatibleHeld(p *models.RolePermission) []string {
	held := map[string]bool{
		"can_manage_users":         p.CanManageUsers,
		"can_manage_superusers":    p.CanManageSuperusers,
		"can_manage_settings":      p.CanManageSettings,
		"can_manage_notifications": p.CanManageNotifications,
		"can_manage_automation":    p.CanManageAutomation,
		"can_manage_billing":       p.CanManageBilling,
	}
	var out []string
	for _, perm := range IncompatiblePermissions {
		if held[perm] {
			out = append(out, perm)
		}
	}
	return out
}
