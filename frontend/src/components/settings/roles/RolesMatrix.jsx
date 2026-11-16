import { Check, Minus } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
	countPermissions,
	PERMISSION_GROUPS,
	riskBorderColor,
	riskGroupBg,
} from "../../../constants/permissionGroups";
import { RoleMarks } from "./RoleBadges";
import { roleLabel, roleRules } from "./roleRules";

// RolesMatrix compares every role side by side. The role names and the
// permission names stay in view while scrolling the table.
const RolesMatrix = ({ roles, limitedRoles, isOIDCSyncRoles, onOpen }) => {
	const { t } = useTranslation();

	return (
		<div className="bg-white dark:bg-secondary-800 shadow sm:rounded-lg overflow-hidden">
			<div className="max-h-[75vh] overflow-auto">
				<table className="min-w-full border-separate border-spacing-0">
					<thead>
						<tr>
							<th className="sticky left-0 top-0 z-30 border-b border-r border-secondary-200 bg-secondary-50 px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-secondary-500 dark:border-secondary-600 dark:bg-secondary-700 dark:text-white">
								{t("Permission")}
							</th>
							{roles.map((r) => {
								const { enabled, total } = countPermissions(r);
								return (
									<th
										key={r.role}
										className="sticky top-0 z-20 border-b border-secondary-200 bg-secondary-50 px-3 py-2 text-center dark:border-secondary-600 dark:bg-secondary-700"
									>
										<button
											type="button"
											onClick={() => onOpen(r.role)}
											title={t("Edit role permissions")}
											className="rounded-md px-2 py-1 text-xs font-medium text-secondary-700 hover:bg-secondary-200 dark:text-white dark:hover:bg-secondary-600"
										>
											<span className="flex items-center justify-center gap-1 capitalize whitespace-nowrap">
												{roleLabel(r.role)}
												<RoleMarks
													rules={roleRules(r.role, isOIDCSyncRoles)}
													limited={limitedRoles.has(r.role)}
												/>
											</span>
											<span className="block text-[10px] font-normal tabular-nums text-secondary-400 dark:text-secondary-300">
												{enabled}/{total}
											</span>
										</button>
									</th>
								);
							})}
						</tr>
					</thead>
					<tbody>
						{PERMISSION_GROUPS.map((group) => {
							const GroupIcon = group.icon;
							return [
								<tr key={`group-${group.id}`}>
									<td
										className={`sticky left-0 z-10 border-b border-l-4 border-secondary-200 px-4 py-2 dark:border-secondary-600 ${riskBorderColor(group.riskLevel)} ${riskGroupBg(group.riskLevel)}`}
									>
										<span className="flex items-center gap-2 whitespace-nowrap text-sm font-semibold text-secondary-800 dark:text-secondary-100">
											<GroupIcon className="h-4 w-4 text-secondary-500 dark:text-secondary-300" />
											{t(group.name)}
										</span>
									</td>
									<td
										colSpan={roles.length}
										className={`border-b border-secondary-200 dark:border-secondary-600 ${riskGroupBg(group.riskLevel)}`}
									/>
								</tr>,
								...group.permissions.map((perm) => {
									const PermIcon = perm.icon;
									return (
										<tr
											key={perm.key}
											className="group hover:bg-secondary-50 dark:hover:bg-secondary-700/50"
										>
											<td
												className="sticky left-0 z-10 border-b border-r border-secondary-100 bg-white px-4 py-2 text-sm text-secondary-700 group-hover:bg-secondary-50 dark:border-secondary-700 dark:bg-secondary-800 dark:text-secondary-200 dark:group-hover:bg-secondary-700"
												title={t(perm.description)}
											>
												<span className="flex items-center gap-2 whitespace-nowrap">
													<PermIcon className="h-4 w-4 flex-shrink-0 text-secondary-400" />
													{t(perm.label)}
												</span>
											</td>
											{roles.map((r) => (
												<td
													key={`${r.role}-${perm.key}`}
													className="border-b border-secondary-100 px-3 py-2 text-center dark:border-secondary-700"
												>
													{r[perm.key] ? (
														<span className="inline-flex h-6 w-6 items-center justify-center rounded-full bg-green-100 dark:bg-green-900/30">
															<Check
																className="h-3.5 w-3.5 text-green-600 dark:text-green-400"
																aria-label={t("Yes")}
															/>
														</span>
													) : (
														<span className="inline-flex h-6 w-6 items-center justify-center rounded-full bg-secondary-100 dark:bg-secondary-700">
															<Minus
																className="h-3.5 w-3.5 text-secondary-400 dark:text-secondary-500"
																aria-label={t("No")}
															/>
														</span>
													)}
												</td>
											))}
										</tr>
									);
								}),
							];
						})}
					</tbody>
				</table>
			</div>
		</div>
	);
};

export default RolesMatrix;
