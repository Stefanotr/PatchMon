import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Columns3, Info, ListChecks } from "lucide-react";
import { useCallback, useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router-dom";
import {
	countPermissions,
	PERMISSION_GROUPS,
	ROLE_PRESETS,
	riskBadgeClasses,
	riskBorderColor,
	riskLabel,
} from "../../constants/permissionGroups";
import { useAuth } from "../../contexts/AuthContext";
import { useConfirm } from "../../contexts/ConfirmContext";
import { useToast } from "../../contexts/ToastContext";
import { hostScopeAPI, permissionsAPI } from "../../utils/api";
import ModalPortal from "../ui/ModalPortal";
import RoleEditor from "./roles/RoleEditor";
import RoleList from "./roles/RoleList";
import RolesMatrix from "./roles/RolesMatrix";
import { roleLabel, sortRoles } from "./roles/roleRules";

// The role shown and the view live in the URL (?role=, ?view=compare), so a
// reload or a shared link opens the same place.
const RolesTab = () => {
	const { t } = useTranslation();
	const confirm = useConfirm();
	const toast = useToast();
	const [showAddModal, setShowAddModal] = useState(false);
	const [dirty, setDirty] = useState(false);
	const [searchParams, setSearchParams] = useSearchParams();
	const queryClient = useQueryClient();
	const { refreshPermissions } = useAuth();

	// Fetch OIDC config to determine if OIDC is enabled
	const { data: oidcConfig } = useQuery({
		queryKey: ["oidcConfig"],
		queryFn: async () => {
			const response = await fetch("/api/v1/auth/oidc/config");
			if (response.ok) {
				return response.json();
			}
			return { enabled: false };
		},
	});

	const isOIDCEnabled = oidcConfig?.enabled || false;
	const isOIDCSyncRoles = isOIDCEnabled && (oidcConfig?.syncRoles || false);

	// Listen for the header button event to open add modal (only blocked when OIDC sync roles is active)
	useEffect(() => {
		const handleOpenAddModal = () => {
			if (!isOIDCSyncRoles) {
				setShowAddModal(true);
			}
		};
		window.addEventListener("openAddRoleModal", handleOpenAddModal);
		return () =>
			window.removeEventListener("openAddRoleModal", handleOpenAddModal);
	}, [isOIDCSyncRoles]);

	// Fetch all role permissions
	const {
		data: rolesData,
		isLoading,
		error,
	} = useQuery({
		queryKey: ["rolePermissions"],
		queryFn: () => permissionsAPI.getRoles().then((res) => res.data),
	});

	// Roles limited to some host groups (fork).
	const { data: hostScopeData } = useQuery({
		queryKey: ["host-scope", "roles"],
		queryFn: () => hostScopeAPI.listRoles().then((res) => res.data),
	});
	const limitedRoles = new Set(
		(hostScopeData?.roles ?? []).map((scope) => scope.role),
	);

	const roles = Array.isArray(rolesData) ? sortRoles(rolesData) : [];
	const view = searchParams.get("view") === "compare" ? "compare" : "roles";
	const selected =
		roles.find((r) => r.role === searchParams.get("role")) ?? roles[0] ?? null;

	const setParams = (changes) =>
		setSearchParams(
			(prev) => {
				const next = new URLSearchParams(prev);
				for (const [key, value] of Object.entries(changes)) {
					if (value) next.set(key, value);
					else next.delete(key);
				}
				return next;
			},
			{ replace: true },
		);

	// Leaving the editor drops its unsaved changes: ask first.
	const canLeaveEditor = async () =>
		!dirty ||
		confirm({
			title: t("Discard changes?"),
			message: t("Your changes to this role are not saved."),
			confirmLabel: t("Discard"),
		});

	const openRole = async (role) => {
		if (view === "roles" && role === selected?.role) return;
		if (await canLeaveEditor()) setParams({ role, view: null });
	};

	const showView = async (next) => {
		if (next === view) return;
		if (next === "compare" && !(await canLeaveEditor())) return;
		setParams({ view: next === "compare" ? "compare" : null });
	};

	const handleDirtyChange = useCallback((value) => setDirty(value), []);

	// Update role permissions mutation
	const updateRoleMutation = useMutation({
		mutationFn: ({ role, permissions }) =>
			permissionsAPI.updateRole(role, permissions),
		onSuccess: () => {
			queryClient.invalidateQueries({ queryKey: ["rolePermissions"] });
			// Refresh user permissions to apply changes immediately
			refreshPermissions();
		},
	});

	// Delete role mutation
	const deleteRoleMutation = useMutation({
		mutationFn: (role) => permissionsAPI.deleteRole(role),
		onSuccess: () => {
			queryClient.invalidateQueries({ queryKey: ["rolePermissions"] });
		},
	});

	const handleSavePermissions = async (role, permissions) => {
		try {
			await updateRoleMutation.mutateAsync({ role, permissions });
			toast.success(t('Role "{{role}}" saved', { role: roleLabel(role) }));
		} catch (error) {
			console.error("Failed to update permissions:", error);
			toast.error(error.response?.data?.error || t("Failed to update role"));
		}
	};

	const handleDeleteRole = async (role) => {
		const confirmed = await confirm({
			title: t("Delete role"),
			message: t('Are you sure you want to delete the "{{role}}" role?', {
				role,
			}),
			confirmLabel: t("Delete role"),
		});
		if (!confirmed) return;

		try {
			await deleteRoleMutation.mutateAsync(role);
			toast.success(t('Role "{{role}}" deleted', { role }));
			setDirty(false);
			setParams({ role: null });
		} catch (error) {
			console.error("Failed to delete role:", error);
			toast.error(error.response?.data?.error || t("Failed to delete role"));
		}
	};

	if (isLoading) {
		return (
			<div className="flex items-center justify-center h-64">
				<div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary-600" />
			</div>
		);
	}

	if (error) {
		return (
			<div className="bg-danger-50 border border-danger-200 rounded-md p-4">
				<div className="flex">
					<AlertTriangle className="h-5 w-5 text-danger-400" />
					<div className="ml-3">
						<h3 className="text-sm font-medium text-danger-800">
							{t("Error loading permissions")}
						</h3>
						<p className="mt-1 text-sm text-danger-700">{error.message}</p>
					</div>
				</div>
			</div>
		);
	}

	const viewButton = (id, Icon, label) => (
		<button
			type="button"
			onClick={() => showView(id)}
			aria-pressed={view === id}
			className={`inline-flex items-center gap-2 rounded-md px-3 py-1.5 text-sm font-medium transition-colors ${
				view === id
					? "bg-white text-secondary-900 shadow-sm dark:bg-secondary-600 dark:text-white"
					: "text-secondary-600 hover:text-secondary-900 dark:text-secondary-300 dark:hover:text-white"
			}`}
		>
			<Icon className="h-4 w-4" />
			{label}
		</button>
	);

	return (
		<div className="space-y-6">
			{/* OIDC Info Banner - only show when OIDC sync roles is active */}
			{isOIDCSyncRoles && (
				<div className="bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded-lg p-4">
					<div className="flex">
						<Info className="h-5 w-5 text-blue-500 dark:text-blue-400 flex-shrink-0" />
						<div className="ml-3">
							<h3 className="text-sm font-medium text-blue-800 dark:text-blue-200">
								{t("OIDC Role Mapping Enabled")}
							</h3>
							<p className="mt-1 text-sm text-blue-700 dark:text-blue-300">
								{t(
									"Roles are managed via your Identity Provider (IdP) groups. Configure the following environment variables to map IdP groups to roles:",
								)}
							</p>
							<ul className="mt-2 text-sm text-blue-600 dark:text-blue-400 list-disc list-inside space-y-1">
								<li>
									<code className="bg-blue-100 dark:bg-blue-900 px-1 rounded">
										OIDC_SUPERADMIN_GROUP
									</code>{" "}
									+{" "}
									<code className="bg-blue-100 dark:bg-blue-900 px-1 rounded">
										OIDC_ADMIN_GROUP
									</code>{" "}
									{t("maps to Super Admin (highest)")}
								</li>
								<li>
									<code className="bg-blue-100 dark:bg-blue-900 px-1 rounded">
										OIDC_ADMIN_GROUP
									</code>{" "}
									{t("maps to Admin")}
								</li>
								<li>
									<code className="bg-blue-100 dark:bg-blue-900 px-1 rounded">
										OIDC_HOST_MANAGER_GROUP
									</code>{" "}
									{t("maps to Host Manager")}
								</li>
								<li>
									<code className="bg-blue-100 dark:bg-blue-900 px-1 rounded">
										OIDC_USER_GROUP
									</code>{" "}
									{t("maps to User (can export data)")}
								</li>
								<li>
									<code className="bg-blue-100 dark:bg-blue-900 px-1 rounded">
										OIDC_READONLY_GROUP
									</code>{" "}
									{t("maps to Readonly (view only, lowest)")}
								</li>
							</ul>
						</div>
					</div>
				</div>
			)}

			{roles.length > 0 && (
				<div className="inline-flex rounded-lg bg-secondary-100 p-1 dark:bg-secondary-700">
					{viewButton("roles", ListChecks, t("By role"))}
					{viewButton("compare", Columns3, t("Compare roles"))}
				</div>
			)}

			{roles.length === 0 && (
				<p className="text-sm text-secondary-500 dark:text-secondary-400">
					{t("No role yet.")}
				</p>
			)}

			{roles.length > 0 && view === "compare" && (
				<RolesMatrix
					roles={roles}
					limitedRoles={limitedRoles}
					isOIDCSyncRoles={isOIDCSyncRoles}
					onOpen={openRole}
				/>
			)}

			{selected && view === "roles" && (
				<div className="grid items-start gap-6 lg:grid-cols-[16rem_minmax(0,1fr)]">
					<RoleList
						roles={roles}
						selected={selected.role}
						onSelect={openRole}
						limitedRoles={limitedRoles}
						isOIDCSyncRoles={isOIDCSyncRoles}
					/>
					<RoleEditor
						key={selected.role}
						role={selected}
						isOIDCSyncRoles={isOIDCSyncRoles}
						limited={limitedRoles.has(selected.role)}
						saving={updateRoleMutation.isPending}
						onSave={handleSavePermissions}
						onDelete={handleDeleteRole}
						onDirtyChange={handleDirtyChange}
					/>
				</div>
			)}

			{/* Add Role Modal - only hidden when OIDC sync roles is active */}
			{!isOIDCSyncRoles && (
				<AddRoleModal
					isOpen={showAddModal}
					onClose={() => setShowAddModal(false)}
					onSuccess={(role) => {
						queryClient.invalidateQueries({ queryKey: ["rolePermissions"] });
						setShowAddModal(false);
						openRole(role);
					}}
				/>
			)}
		</div>
	);
};

// Add Role Modal Component
const AddRoleModal = ({ isOpen, onClose, onSuccess }) => {
	const { t } = useTranslation();
	const roleNameInputId = useId();
	const [formData, setFormData] = useState({
		role: "",
		...ROLE_PRESETS.clear,
	});
	const [isLoading, setIsLoading] = useState(false);
	const [error, setError] = useState("");

	const handleSubmit = async (e) => {
		e.preventDefault();
		setIsLoading(true);
		setError("");

		try {
			await permissionsAPI.updateRole(formData.role, formData);
			onSuccess(formData.role);
		} catch (err) {
			setError(err.response?.data?.error || t("Failed to create role"));
		} finally {
			setIsLoading(false);
		}
	};

	const handleInputChange = (e) => {
		const { name, value, type, checked } = e.target;
		setFormData({
			...formData,
			[name]: type === "checkbox" ? checked : value,
		});
	};

	const applyPreset = (presetKey) => {
		setFormData((prev) => ({
			...prev,
			...ROLE_PRESETS[presetKey],
		}));
	};

	const toggleGroup = (group, selectAll) => {
		setFormData((prev) => {
			const updated = { ...prev };
			for (const perm of group.permissions) {
				updated[perm.key] = selectAll;
			}
			return updated;
		});
	};

	const isGroupAllSelected = (group) =>
		group.permissions.every((p) => formData[p.key]);

	const { enabled, total } = countPermissions(formData);

	if (!isOpen) return null;

	const modal = (
		<div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-[120]">
			<div className="bg-white dark:bg-secondary-800 rounded-lg p-6 w-full max-w-2xl max-h-[90vh] overflow-y-auto">
				<h3 className="text-lg font-medium text-secondary-900 dark:text-white mb-4">
					{t("Add New Role")}
				</h3>

				<form onSubmit={handleSubmit} className="space-y-4">
					{/* Role name input */}
					<div>
						<label
							htmlFor={roleNameInputId}
							className="block text-sm font-medium text-secondary-700 dark:text-secondary-200 mb-1"
						>
							{t("Role Name")}
						</label>
						<input
							id={roleNameInputId}
							type="text"
							name="role"
							required
							value={formData.role}
							onChange={handleInputChange}
							className="block w-full border-secondary-300 dark:border-secondary-600 rounded-md shadow-sm focus:ring-primary-500 focus:border-primary-500 bg-white dark:bg-secondary-700 text-secondary-900 dark:text-white"
							placeholder={t("e.g., host_manager, readonly")}
						/>
						<p className="mt-1 text-xs text-secondary-500 dark:text-white">
							{t("Use lowercase with underscores (e.g., host_manager)")}
						</p>
					</div>

					{/* Preset buttons */}
					<div className="flex flex-wrap gap-2">
						<button
							type="button"
							onClick={() => applyPreset("readonly")}
							className="px-3 py-1.5 text-xs font-medium rounded-md border border-green-300 dark:border-green-700 text-green-700 dark:text-green-400 bg-green-50 dark:bg-green-900/20 hover:bg-green-100 dark:hover:bg-green-900/40"
						>
							{t("Read Only")}
						</button>
						<button
							type="button"
							onClick={() => applyPreset("operator")}
							className="px-3 py-1.5 text-xs font-medium rounded-md border border-yellow-300 dark:border-yellow-700 text-yellow-700 dark:text-yellow-400 bg-yellow-50 dark:bg-yellow-900/20 hover:bg-yellow-100 dark:hover:bg-yellow-900/40"
						>
							{t("Operator")}
						</button>
						<button
							type="button"
							onClick={() => applyPreset("admin")}
							className="px-3 py-1.5 text-xs font-medium rounded-md border border-red-300 dark:border-red-700 text-red-700 dark:text-red-400 bg-red-50 dark:bg-red-900/20 hover:bg-red-100 dark:hover:bg-red-900/40"
						>
							{t("Admin")}
						</button>
						<button
							type="button"
							onClick={() => applyPreset("clear")}
							className="px-3 py-1.5 text-xs font-medium rounded-md border border-secondary-300 dark:border-secondary-600 text-secondary-700 dark:text-secondary-300 bg-white dark:bg-secondary-700 hover:bg-secondary-50 dark:hover:bg-secondary-600"
						>
							{t("Clear All")}
						</button>
					</div>

					{/* Grouped permissions */}
					<div className="space-y-5">
						{PERMISSION_GROUPS.map((group) => {
							const GroupIcon = group.icon;
							const allSelected = isGroupAllSelected(group);
							return (
								<div
									key={group.id}
									className={`rounded-lg border border-secondary-200 dark:border-secondary-700 border-l-4 ${riskBorderColor(group.riskLevel)} overflow-hidden`}
								>
									{/* Group header */}
									<div className="flex items-center justify-between px-4 py-2.5 bg-secondary-50 dark:bg-secondary-700">
										<div className="flex items-center gap-2">
											<GroupIcon className="h-4 w-4 text-secondary-500 dark:text-secondary-300" />
											<span className="text-sm font-semibold text-secondary-800 dark:text-secondary-100">
												{t(group.name)}
											</span>
											<span
												className={`inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-medium ${riskBadgeClasses(group.riskLevel)}`}
											>
												{t(riskLabel(group.riskLevel))}
											</span>
										</div>
										<button
											type="button"
											onClick={() => toggleGroup(group, !allSelected)}
											className="text-xs text-primary-600 dark:text-primary-400 hover:text-primary-800 dark:hover:text-primary-300 font-medium"
										>
											{allSelected ? t("Deselect all") : t("Select all")}
										</button>
									</div>
									{/* Permission checkboxes */}
									<div className="grid grid-cols-1 sm:grid-cols-2 gap-3 p-4">
										{group.permissions.map((perm) => {
											const PermIcon = perm.icon;
											return (
												<div key={perm.key} className="flex items-start gap-2">
													<input
														id={`add-role-${perm.key}`}
														type="checkbox"
														name={perm.key}
														checked={formData[perm.key]}
														onChange={handleInputChange}
														className="mt-0.5 h-4 w-4 text-primary-600 focus:ring-primary-500 border-secondary-300 rounded"
													/>
													<label
														htmlFor={`add-role-${perm.key}`}
														className="flex-1 min-w-0"
													>
														<div className="flex items-center gap-1.5">
															<PermIcon className="h-3.5 w-3.5 text-secondary-400 flex-shrink-0" />
															<span className="text-sm font-medium text-secondary-800 dark:text-secondary-100">
																{t(perm.label)}
															</span>
														</div>
														<p className="text-xs text-secondary-500 dark:text-secondary-400 mt-0.5">
															{t(perm.description)}
														</p>
													</label>
												</div>
											);
										})}
									</div>
								</div>
							);
						})}
					</div>

					{error && (
						<div className="bg-danger-50 dark:bg-danger-900 border border-danger-200 dark:border-danger-700 rounded-md p-3">
							<p className="text-sm text-danger-700 dark:text-danger-300">
								{error}
							</p>
						</div>
					)}

					<div className="flex items-center justify-between pt-2">
						<span className="text-sm text-secondary-500 dark:text-secondary-400">
							{t("{{enabled}}/{{total}} permissions selected", {
								enabled,
								total,
							})}
						</span>
						<div className="flex space-x-3">
							<button
								type="button"
								onClick={onClose}
								className="px-4 py-2 text-sm font-medium text-secondary-700 dark:text-secondary-200 bg-white dark:bg-secondary-700 border border-secondary-300 dark:border-secondary-600 rounded-md hover:bg-secondary-50 dark:hover:bg-secondary-600"
							>
								{t("Cancel")}
							</button>
							<button
								type="submit"
								disabled={isLoading}
								className="px-4 py-2 text-sm font-medium text-white bg-primary-600 border border-transparent rounded-md hover:bg-primary-700 disabled:opacity-50"
							>
								{isLoading ? t("Creating...") : t("Create Role")}
							</button>
						</div>
					</div>
				</form>
			</div>
		</div>
	);

	return <ModalPortal>{modal}</ModalPortal>;
};

export default RolesTab;
