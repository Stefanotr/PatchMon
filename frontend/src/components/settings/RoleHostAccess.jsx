import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Save, Server } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { PERMISSION_GROUPS } from "../../constants/permissionGroups";
import { useToast } from "../../contexts/ToastContext";
import { hostGroupsAPI, hostScopeAPI } from "../../utils/api";

// Roles that always see every host (the server refuses to limit them).
const FULL_ACCESS_ROLES = new Set(["superadmin", "admin"]);

const PERMISSION_LABELS = Object.fromEntries(
	PERMISSION_GROUPS.flatMap((g) => g.permissions).map((p) => [p.key, p.label]),
);

const sameSet = (a = [], b = []) =>
	a.length === b.length && a.every((id) => b.includes(id));

// RoleHostAccess edits which hosts a role's users can see: every host, or
// the hosts of some host groups only (fork, internal/hostscope).
const RoleHostAccess = ({ role }) => {
	const { t } = useTranslation();
	const toast = useToast();
	const queryClient = useQueryClient();
	const radioName = useId();

	const { data: scopeData, isLoading } = useQuery({
		queryKey: ["host-scope", "roles"],
		queryFn: () => hostScopeAPI.listRoles().then((res) => res.data),
	});
	const { data: groups = [] } = useQuery({
		queryKey: ["hostGroups"],
		queryFn: () => hostGroupsAPI.list().then((res) => res.data),
	});

	const current = scopeData?.roles?.find((r) => r.role === role.role);
	const [limited, setLimited] = useState(Boolean(current));
	const [selected, setSelected] = useState(current?.host_group_ids ?? []);
	useEffect(() => {
		setLimited(Boolean(current));
		setSelected(current?.host_group_ids ?? []);
	}, [current]);

	const mutation = useMutation({
		mutationFn: () =>
			hostScopeAPI.updateRole(role.role, {
				limited,
				hostGroupIds: limited ? selected : [],
			}),
		onSuccess: () => {
			queryClient.invalidateQueries({ queryKey: ["host-scope"] });
			toast.success(t("Host access updated"));
		},
		onError: (error) => {
			toast.error(
				error.response?.data?.error || t("Failed to update host access"),
			);
		},
	});

	if (isLoading) return null;

	const fullAccess = FULL_ACCESS_ROLES.has(role.role);
	const status = scopeData?.status ?? {};
	const incompatible = (scopeData?.incompatible_permissions ?? []).filter(
		(perm) => role[perm],
	);
	const canLimit = !fullAccess && incompatible.length === 0 && status.available;
	const dirty =
		limited !== Boolean(current) ||
		(limited && !sameSet(selected, current?.host_group_ids));

	const toggleGroup = (id) =>
		setSelected((prev) =>
			prev.includes(id) ? prev.filter((g) => g !== id) : [...prev, id],
		);

	return (
		<section className="bg-white dark:bg-secondary-800 shadow sm:rounded-lg p-4 md:p-6">
			<div className="flex items-center gap-2 mb-1">
				<Server className="h-5 w-5 text-secondary-500 dark:text-secondary-300" />
				<h3 className="text-base font-semibold text-secondary-900 dark:text-white">
					{t("Host access")}
				</h3>
			</div>
			<p className="text-sm text-secondary-500 dark:text-secondary-300 mb-4">
				{t("Which hosts users with this role can see and act on.")}
			</p>

			{fullAccess ? (
				<p className="text-sm text-secondary-700 dark:text-secondary-200">
					{t("This role always has every host.")}
				</p>
			) : (
				<div className="space-y-4">
					<fieldset className="space-y-2">
						<legend className="sr-only">{t("Host access")}</legend>
						<label className="flex items-center gap-2 text-sm text-secondary-800 dark:text-secondary-100">
							<input
								type="radio"
								name={radioName}
								checked={!limited}
								onChange={() => setLimited(false)}
							/>
							{t("Every host")}
						</label>
						<label
							className={`flex items-center gap-2 text-sm ${canLimit || limited ? "text-secondary-800 dark:text-secondary-100" : "text-secondary-400 dark:text-secondary-500"}`}
						>
							<input
								type="radio"
								name={radioName}
								checked={limited}
								disabled={!canLimit && !limited}
								onChange={() => setLimited(true)}
							/>
							{t("Only the hosts of some host groups")}
						</label>
					</fieldset>

					{!status.available && (
						<div className="flex gap-2 rounded-md border border-warning-200 bg-warning-50 p-3 text-sm text-warning-800 dark:border-warning-800 dark:bg-warning-900/20 dark:text-warning-200">
							<AlertTriangle className="h-4 w-4 flex-shrink-0 mt-0.5" />
							<span>
								{t(
									"Limiting roles is not available on this server: {{reason}}",
									{ reason: status.reason || "-" },
								)}
							</span>
						</div>
					)}
					{incompatible.length > 0 && (
						<div className="flex gap-2 rounded-md border border-warning-200 bg-warning-50 p-3 text-sm text-warning-800 dark:border-warning-800 dark:bg-warning-900/20 dark:text-warning-200">
							<AlertTriangle className="h-4 w-4 flex-shrink-0 mt-0.5" />
							<span>
								{t(
									"A role limited to some host groups cannot hold these permissions, which act on every host: {{permissions}}. Remove them first.",
									{
										permissions: incompatible
											.map((perm) => t(PERMISSION_LABELS[perm] ?? perm))
											.join(", "),
									},
								)}
							</span>
						</div>
					)}

					{limited && (
						<div className="space-y-2">
							{groups.length === 0 ? (
								<p className="text-sm text-secondary-500 dark:text-secondary-300">
									{t("No host group yet. Create host groups first.")}
								</p>
							) : (
								<div className="max-h-60 overflow-y-auto rounded-md border border-secondary-200 dark:border-secondary-600 divide-y divide-secondary-100 dark:divide-secondary-700">
									{groups.map((g) => (
										<label
											key={g.id}
											className="flex items-center gap-2 px-3 py-2 text-sm text-secondary-800 dark:text-secondary-100"
										>
											<input
												type="checkbox"
												checked={selected.includes(g.id)}
												onChange={() => toggleGroup(g.id)}
											/>
											{g.name}
										</label>
									))}
								</div>
							)}
							{selected.length === 0 && (
								<p className="text-sm text-warning-700 dark:text-warning-300">
									{t(
										"No group selected: users with this role will see no host.",
									)}
								</p>
							)}
							<p className="text-xs text-secondary-500 dark:text-secondary-400">
								{t(
									"Users with this role see only these groups' hosts, with their packages, patch runs, containers, scans and alerts. They cannot add hosts, manage host groups, patch policies or patch schedules, or see the automation queues.",
								)}
							</p>
						</div>
					)}

					<div className="flex justify-end">
						<button
							type="button"
							className="btn-primary flex items-center gap-2"
							disabled={!dirty || mutation.isPending}
							onClick={() => mutation.mutate()}
						>
							<Save className="h-4 w-4" />
							{t("Save host access")}
						</button>
					</div>
				</div>
			)}
		</section>
	);
};

export default RoleHostAccess;
