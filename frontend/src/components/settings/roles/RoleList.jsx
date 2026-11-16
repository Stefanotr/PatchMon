import { Search } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { countPermissions } from "../../../constants/permissionGroups";
import { RoleMarks } from "./RoleBadges";
import { roleLabel, roleRules } from "./roleRules";

// Above this many roles the list gets a filter box.
const FILTER_FROM = 8;

// RoleList shows every role at once, compact, with how much each one can do,
// and picks the role the editor shows. Small screens get a select instead.
const RoleList = ({
	roles,
	selected,
	onSelect,
	limitedRoles,
	isOIDCSyncRoles,
}) => {
	const { t } = useTranslation();
	const selectId = useId();
	const [filter, setFilter] = useState("");
	const activeRef = useRef(null);

	// Keep the selected role in view when the list has to scroll.
	useEffect(() => {
		if (selected) activeRef.current?.scrollIntoView?.({ block: "nearest" });
	}, [selected]);

	const needle = filter.trim().toLowerCase();
	const shown = needle
		? roles.filter((r) => roleLabel(r.role).toLowerCase().includes(needle))
		: roles;

	return (
		<>
			<div className="lg:hidden">
				<label
					htmlFor={selectId}
					className="block text-sm font-medium text-secondary-700 dark:text-secondary-200 mb-1"
				>
					{t("Role")}
				</label>
				<select
					id={selectId}
					value={selected ?? ""}
					onChange={(e) => onSelect(e.target.value)}
					className="input capitalize"
				>
					{roles.map((r) => {
						const { enabled, total } = countPermissions(r);
						return (
							<option key={r.role} value={r.role}>
								{`${roleLabel(r.role)} (${enabled}/${total})`}
							</option>
						);
					})}
				</select>
			</div>

			<nav
				aria-label={t("Roles")}
				className="hidden lg:block bg-white dark:bg-secondary-800 shadow sm:rounded-lg overflow-hidden"
			>
				{roles.length > FILTER_FROM && (
					<div className="relative border-b border-secondary-200 dark:border-secondary-600 p-3">
						<Search className="pointer-events-none absolute left-5 top-1/2 h-4 w-4 -translate-y-1/2 text-secondary-400" />
						<input
							type="search"
							value={filter}
							onChange={(e) => setFilter(e.target.value)}
							placeholder={t("Filter roles")}
							aria-label={t("Filter roles")}
							className="input pl-8"
						/>
					</div>
				)}
				<ul className="max-h-[70vh] overflow-y-auto py-1">
					{shown.map((r) => {
						const { enabled, total } = countPermissions(r);
						const active = r.role === selected;
						return (
							<li key={r.role}>
								<button
									type="button"
									ref={active ? activeRef : undefined}
									onClick={() => onSelect(r.role)}
									aria-current={active ? "true" : undefined}
									className={`w-full text-left px-3 py-2 border-l-4 transition-colors ${
										active
											? "border-primary-600 bg-primary-50 dark:bg-primary-900/30"
											: "border-transparent hover:bg-secondary-50 dark:hover:bg-secondary-700/60"
									}`}
								>
									<span className="flex items-center gap-1.5">
										<span
											className={`truncate text-sm font-medium capitalize ${
												active
													? "text-primary-800 dark:text-primary-200"
													: "text-secondary-900 dark:text-white"
											}`}
										>
											{roleLabel(r.role)}
										</span>
										<RoleMarks
											rules={roleRules(r.role, isOIDCSyncRoles)}
											limited={limitedRoles.has(r.role)}
										/>
										<span className="ml-auto shrink-0 text-xs tabular-nums text-secondary-500 dark:text-secondary-400">
											{enabled}/{total}
										</span>
									</span>
									<span className="mt-1.5 block h-1 rounded-full bg-secondary-100 dark:bg-secondary-700">
										<span
											className="block h-1 rounded-full bg-primary-500"
											style={{
												width: `${total ? Math.round((enabled / total) * 100) : 0}%`,
											}}
										/>
									</span>
								</button>
							</li>
						);
					})}
					{shown.length === 0 && (
						<li className="px-4 py-3 text-sm text-secondary-500 dark:text-secondary-400">
							{t("No role matches this filter")}
						</li>
					)}
				</ul>
			</nav>
		</>
	);
};

export default RoleList;
