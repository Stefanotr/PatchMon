import { Info, Save, Shield, Trash2 } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	countPermissions,
	PERMISSION_GROUPS,
	riskBadgeClasses,
	riskBorderColor,
	riskGroupBg,
	riskLabel,
} from "../../../constants/permissionGroups";
import RoleHostAccess from "../RoleHostAccess";
import RoleBadges from "./RoleBadges";
import { permissionsChanged, roleLabel, roleRules } from "./roleRules";

// PermissionGroup lists the permissions of one group, with a shortcut to
// grant or remove them all.
const PermissionGroup = ({ group, draft, editable, idPrefix, onChange }) => {
	const { t } = useTranslation();
	const GroupIcon = group.icon;
	const granted = group.permissions.filter((p) => draft[p.key]).length;
	const allGranted = granted === group.permissions.length;

	const setAll = (value) => {
		for (const perm of group.permissions) onChange(perm.key, value);
	};

	return (
		<section
			className={`bg-white dark:bg-secondary-800 shadow sm:rounded-lg border-l-4 ${riskBorderColor(group.riskLevel)} overflow-hidden`}
		>
			<div
				className={`flex flex-wrap items-center justify-between gap-2 px-4 py-3 ${riskGroupBg(group.riskLevel)}`}
			>
				<div className="flex flex-wrap items-center gap-2 min-w-0">
					<GroupIcon className="h-4 w-4 text-secondary-500 dark:text-secondary-300" />
					<h4 className="text-sm font-semibold text-secondary-800 dark:text-secondary-100">
						{t(group.name)}
					</h4>
					<span
						className={`inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-medium ${riskBadgeClasses(group.riskLevel)}`}
					>
						{t(riskLabel(group.riskLevel))}
					</span>
					<span className="text-xs tabular-nums text-secondary-500 dark:text-secondary-400">
						{granted}/{group.permissions.length}
					</span>
				</div>
				{editable && (
					<button
						type="button"
						onClick={() => setAll(!allGranted)}
						className="text-xs font-medium text-primary-600 hover:text-primary-800 dark:text-primary-400 dark:hover:text-primary-300"
					>
						{allGranted ? t("Deselect all") : t("Select all")}
					</button>
				)}
			</div>
			<div className="grid grid-cols-1 md:grid-cols-2 gap-x-6 gap-y-1 p-3">
				{group.permissions.map((perm) => {
					const PermIcon = perm.icon;
					const id = `${idPrefix}-${perm.key}`;
					return (
						<div
							key={perm.key}
							className={`flex items-start gap-3 rounded-md p-2 ${
								editable
									? "hover:bg-secondary-50 dark:hover:bg-secondary-700/50"
									: ""
							}`}
						>
							<input
								id={id}
								type="checkbox"
								checked={Boolean(draft[perm.key])}
								disabled={!editable}
								aria-describedby={`${id}-description`}
								onChange={(e) => onChange(perm.key, e.target.checked)}
								className="mt-0.5 h-4 w-4 rounded border-secondary-300 text-primary-600 focus:ring-primary-500 disabled:opacity-60"
							/>
							<div className="min-w-0">
								<label
									htmlFor={id}
									className={`flex items-center gap-1.5 text-sm font-medium text-secondary-900 dark:text-white ${
										editable ? "cursor-pointer" : ""
									}`}
								>
									<PermIcon className="h-3.5 w-3.5 flex-shrink-0 text-secondary-400" />
									{t(perm.label)}
								</label>
								<p
									id={`${id}-description`}
									className="mt-0.5 text-xs text-secondary-500 dark:text-secondary-400"
								>
									{t(perm.description)}
								</p>
							</div>
						</div>
					);
				})}
			</div>
		</section>
	);
};

// RoleEditor edits the permissions of one role in place: a save bar shows up
// as soon as something changed. It is fixed to the window, not sticky: the
// app layout has an overflow-hidden ancestor, which disables sticky.
const RoleEditor = ({
	role,
	isOIDCSyncRoles,
	limited,
	saving,
	onSave,
	onDelete,
	onDirtyChange,
}) => {
	const { t } = useTranslation();
	const idPrefix = useId();
	const [draft, setDraft] = useState(role);
	const dirty = permissionsChanged(draft, role);
	const dirtyRef = useRef(dirty);
	dirtyRef.current = dirty;

	// Take fresh server data (after a save, or a refetch) unless the user is
	// in the middle of a change.
	useEffect(() => {
		if (!dirtyRef.current) setDraft(role);
	}, [role]);

	useEffect(() => {
		onDirtyChange(dirty);
	}, [dirty, onDirtyChange]);
	useEffect(() => () => onDirtyChange(false), [onDirtyChange]);

	const rules = roleRules(role.role, isOIDCSyncRoles);
	const { enabled, total } = countPermissions(draft);
	const setPermission = (key, value) =>
		setDraft((prev) => ({ ...prev, [key]: value }));

	return (
		<div className="min-w-0 space-y-4">
			<header className="flex flex-wrap items-start justify-between gap-3 bg-white dark:bg-secondary-800 shadow sm:rounded-lg p-4 md:p-5">
				<div className="min-w-0">
					<div className="flex flex-wrap items-center gap-2">
						<Shield className="h-5 w-5 text-primary-600" />
						<h3 className="text-lg font-semibold capitalize text-secondary-900 dark:text-white">
							{roleLabel(role.role)}
						</h3>
						<RoleBadges rules={rules} limited={limited} />
					</div>
					<p className="mt-1 text-sm text-secondary-500 dark:text-secondary-400">
						{t("{{enabled}} of {{total}} permissions", { enabled, total })}
					</p>
				</div>
				{rules.canDelete && (
					<button
						type="button"
						onClick={() => onDelete(role.role)}
						className="btn-outline flex items-center gap-2 text-danger-600 dark:text-danger-400"
					>
						<Trash2 className="h-4 w-4" />
						{t("Delete role")}
					</button>
				)}
			</header>

			{!rules.canEdit && (
				<div className="flex gap-2 rounded-md border border-blue-200 bg-blue-50 p-3 text-sm text-blue-800 dark:border-blue-800 dark:bg-blue-900/20 dark:text-blue-200">
					<Info className="mt-0.5 h-4 w-4 flex-shrink-0" />
					<span>
						{t(
							"This built-in role keeps its permissions. Create a role to choose them.",
						)}
					</span>
				</div>
			)}

			<div className="space-y-4">
				{PERMISSION_GROUPS.map((group) => (
					<PermissionGroup
						key={group.id}
						group={group}
						draft={draft}
						editable={rules.canEdit}
						idPrefix={idPrefix}
						onChange={setPermission}
					/>
				))}

				{dirty && (
					<div
						role="status"
						className="fixed bottom-6 left-4 right-4 z-40 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-primary-200 bg-white px-4 py-3 shadow-xl sm:left-auto sm:right-6 dark:border-primary-800 dark:bg-secondary-800"
					>
						<span className="text-sm font-medium text-secondary-800 dark:text-secondary-100">
							{t("Unsaved changes to this role")}
						</span>
						<div className="flex gap-2">
							<button
								type="button"
								onClick={() => setDraft(role)}
								disabled={saving}
								className="btn-outline"
							>
								{t("Discard")}
							</button>
							<button
								type="button"
								onClick={() => onSave(role.role, draft)}
								disabled={saving}
								className="btn-primary flex items-center gap-2"
							>
								<Save className="h-4 w-4" />
								{saving ? t("Saving...") : t("Save")}
							</button>
						</div>
					</div>
				)}
			</div>

			<RoleHostAccess role={role} />
		</div>
	);
};

export default RoleEditor;
