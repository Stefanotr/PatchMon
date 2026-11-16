import { PERMISSION_GROUPS } from "../../../constants/permissionGroups";

// Built-in roles first, from the most to the least privileged; custom roles
// follow by name.
const ROLE_ORDER = {
	superadmin: 0,
	admin: 1,
	host_manager: 2,
	user: 3,
	readonly: 4,
};

// Built-in roles keep their permissions and cannot be deleted.
const BUILT_IN_ROLES = new Set(["superadmin", "admin", "user"]);
// Roles mapped from IdP groups: not deletable while OIDC role sync is on.
const OIDC_ROLES = new Set([
	"superadmin",
	"admin",
	"host_manager",
	"readonly",
	"user",
]);

export const PERMISSION_KEYS = PERMISSION_GROUPS.flatMap((group) =>
	group.permissions.map((perm) => perm.key),
);

export const sortRoles = (roles) =>
	[...roles].sort((a, b) => {
		const byOrder = (ROLE_ORDER[a.role] ?? 999) - (ROLE_ORDER[b.role] ?? 999);
		return byOrder !== 0 ? byOrder : a.role.localeCompare(b.role);
	});

export const roleLabel = (role) => role.replace(/_/g, " ");

export const roleRules = (role, isOIDCSyncRoles) => {
	const builtIn = BUILT_IN_ROLES.has(role);
	const oidcRole = OIDC_ROLES.has(role);
	return {
		builtIn,
		oidcSynced: isOIDCSyncRoles && oidcRole,
		canEdit: !builtIn,
		canDelete: isOIDCSyncRoles ? !oidcRole : !builtIn,
	};
};

// True when the draft grants or removes at least one permission of the role.
export const permissionsChanged = (draft, role) =>
	PERMISSION_KEYS.some((key) => Boolean(draft[key]) !== Boolean(role[key]));
