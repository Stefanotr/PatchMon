import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CalendarClock, Edit, Info, Play, Plus, Trash2, X } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { useAuth } from "../../contexts/AuthContext";
import { useConfirm } from "../../contexts/ConfirmContext";
import { useToast } from "../../contexts/ToastContext";
import { formatDate, hostGroupsAPI } from "../../utils/api";
import { patchingAPI } from "../../utils/patchingApi";
import {
	DEFAULT_FORM,
	describeRecurrence,
	formToPayload,
	MONTH_WEEKS,
	scheduleToForm,
	summarizeResult,
	WEEK_ORDER,
	weekdayName,
} from "../../utils/patchSchedules";
import ModalPortal from "../ui/ModalPortal";

const QUERY_KEY = ["patching-schedules"];

const inputClass =
	"w-full rounded border border-secondary-300 dark:border-secondary-600 bg-white dark:bg-secondary-700 text-secondary-900 dark:text-white px-3 py-2";
const labelClass =
	"block text-sm font-medium text-secondary-700 dark:text-secondary-300 mb-1";
const headClass =
	"px-4 py-3 text-left text-xs font-medium text-secondary-500 dark:text-white uppercase tracking-wider";

// PatchSchedulesTab lists the recurring dry runs: at each slot, PatchMon
// dry-runs the pending updates of a host group, and the dry runs wait for
// someone to approve them in Runs & History.
export default function PatchSchedulesTab() {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const toast = useToast();
	const confirm = useConfirm();
	const { canManagePatching } = useAuth();
	const canManage = canManagePatching();
	const [editing, setEditing] = useState(null);
	const [form, setForm] = useState(null);

	const { data, isLoading } = useQuery({
		queryKey: QUERY_KEY,
		queryFn: () => patchingAPI.getSchedules(),
	});
	const schedules = data?.schedules || [];
	const timezone = data?.timezone || "UTC";

	const { data: hostGroups = [] } = useQuery({
		queryKey: ["hostGroups"],
		queryFn: () => hostGroupsAPI.list().then((res) => res.data),
	});

	const refresh = () => queryClient.invalidateQueries({ queryKey: QUERY_KEY });
	const onError = (err) =>
		toast.error(err.response?.data?.error || err.message);

	const saveMutation = useMutation({
		mutationFn: ({ id, payload }) =>
			id
				? patchingAPI.updateSchedule(id, payload)
				: patchingAPI.createSchedule(payload),
		onSuccess: (_, { id }) => {
			refresh();
			setForm(null);
			setEditing(null);
			toast.success(id ? t("Schedule updated") : t("Schedule created"));
		},
		onError,
	});

	const toggleMutation = useMutation({
		mutationFn: (schedule) =>
			patchingAPI.updateSchedule(
				schedule.id,
				formToPayload({
					...scheduleToForm(schedule),
					enabled: !schedule.enabled,
				}),
			),
		onSuccess: refresh,
		onError,
	});

	const deleteMutation = useMutation({
		mutationFn: (id) => patchingAPI.deleteSchedule(id),
		onSuccess: () => {
			refresh();
			toast.success(t("Schedule deleted"));
		},
		onError,
	});

	const runMutation = useMutation({
		mutationFn: (id) => patchingAPI.runSchedule(id),
		onSuccess: (res) => {
			refresh();
			toast.success(summarizeResult(t, res?.result));
		},
		onError,
	});

	const targetName = (s) => s.host_group_name || t("All hosts");

	const openCreate = () => {
		setEditing(null);
		setForm({ ...DEFAULT_FORM, weekdays: [...DEFAULT_FORM.weekdays] });
	};

	const openEdit = (schedule) => {
		setEditing(schedule);
		setForm(scheduleToForm(schedule));
	};

	const handleRun = async (schedule) => {
		const ok = await confirm({
			title: t("Run the schedule now"),
			message: t(
				"Dry-run the pending updates of {{target}} now? Nothing is installed: each dry run waits for approval in Runs & History.",
				{ target: targetName(schedule) },
			),
			confirmLabel: t("Run now"),
		});
		if (ok) runMutation.mutate(schedule.id);
	};

	const handleDelete = async (schedule) => {
		const ok = await confirm({
			title: t("Delete schedule"),
			message: t(
				'Delete the patch schedule "{{name}}"? The runs it started stay in Runs & History.',
				{ name: schedule.name },
			),
			confirmLabel: t("Delete schedule"),
		});
		if (ok) deleteMutation.mutate(schedule.id);
	};

	const handleSubmit = (e) => {
		e.preventDefault();
		if (form.frequency === "weekly" && form.weekdays.length === 0) {
			toast.error(t("Pick at least one day of the week"));
			return;
		}
		saveMutation.mutate({ id: editing?.id, payload: formToPayload(form) });
	};

	const toggleDay = (day) =>
		setForm((f) => ({
			...f,
			weekdays: f.weekdays.includes(day)
				? f.weekdays.filter((d) => d !== day)
				: [...f.weekdays, day],
		}));

	return (
		<div className="mt-4 space-y-4">
			<div className="card p-4 flex gap-3 text-sm text-secondary-700 dark:text-secondary-300">
				<Info className="h-5 w-5 flex-shrink-0 text-primary-600 dark:text-primary-400" />
				<div className="space-y-1">
					<p>
						{t(
							"At each scheduled time, PatchMon dry-runs the pending updates of the hosts in the group. Nothing is installed: each dry run waits for someone to approve it, and the host's patch policy then decides when the approved run executes.",
						)}
					</p>
					<p>
						{t(
							"Windows hosts and pacman hosts are not dry-run by schedules. Times are in {{timezone}}.",
							{ timezone },
						)}{" "}
						<Link
							to="/patching?tab=runs&status=validated"
							className="text-primary-600 dark:text-primary-400 hover:underline"
						>
							{t("See the dry runs waiting for approval")}
						</Link>
					</p>
				</div>
			</div>

			{canManage && (
				<div className="flex justify-end">
					<button
						type="button"
						onClick={openCreate}
						className="btn-primary flex items-center gap-2"
					>
						<Plus className="h-4 w-4" />
						{t("Create schedule")}
					</button>
				</div>
			)}

			<div className="card p-4 md:p-6">
				{isLoading ? (
					<div className="p-8 text-center text-secondary-500">
						{t("Loading...")}
					</div>
				) : schedules.length === 0 ? (
					<div className="p-8 text-center text-secondary-500">
						{t("No patch schedules yet.")}
					</div>
				) : (
					<div className="overflow-x-auto">
						<table className="min-w-full divide-y divide-secondary-200 dark:divide-secondary-600">
							<thead className="bg-secondary-50 dark:bg-secondary-700">
								<tr>
									<th scope="col" className={headClass}>
										{t("Name")}
									</th>
									<th scope="col" className={headClass}>
										{t("Hosts")}
									</th>
									<th scope="col" className={headClass}>
										{t("When")}
									</th>
									<th scope="col" className={headClass}>
										{t("Next run")}
									</th>
									<th scope="col" className={headClass}>
										{t("Last run")}
									</th>
									<th scope="col" className={headClass}>
										{t("Enabled")}
									</th>
									{canManage && (
										<th scope="col" className={`${headClass} text-right`}>
											{t("Actions")}
										</th>
									)}
								</tr>
							</thead>
							<tbody className="bg-white dark:bg-secondary-800 divide-y divide-secondary-200 dark:divide-secondary-600">
								{schedules.map((s) => (
									<tr
										key={s.id}
										className="hover:bg-secondary-50 dark:hover:bg-secondary-700 transition-colors"
									>
										<td className="px-4 py-3 text-sm font-medium text-secondary-900 dark:text-white">
											{s.name}
											<div className="text-xs font-normal text-secondary-500 dark:text-secondary-400">
												{s.scope === "security"
													? t("Security updates")
													: t("All updates")}
											</div>
										</td>
										<td className="px-4 py-3 text-sm text-secondary-600 dark:text-secondary-400">
											{targetName(s)}
										</td>
										<td className="px-4 py-3 text-sm text-secondary-600 dark:text-secondary-400">
											<span className="inline-flex items-center gap-1">
												<CalendarClock className="h-3.5 w-3.5 flex-shrink-0" />
												{describeRecurrence(t, s)}
											</span>
										</td>
										<td className="px-4 py-3 whitespace-nowrap text-sm text-secondary-600 dark:text-secondary-400">
											{s.enabled && s.next_run_at
												? formatDate(s.next_run_at)
												: "-"}
										</td>
										<td className="px-4 py-3 text-sm text-secondary-600 dark:text-secondary-400">
											{s.last_run_at ? (
												<>
													<div className="whitespace-nowrap">
														{formatDate(s.last_run_at)}
													</div>
													<div className="text-xs">
														{summarizeResult(t, s.last_result)}
													</div>
												</>
											) : (
												"-"
											)}
										</td>
										<td className="px-4 py-3 text-sm">
											<button
												type="button"
												role="switch"
												aria-checked={s.enabled}
												aria-label={t("Enabled")}
												disabled={!canManage || toggleMutation.isPending}
												onClick={() => toggleMutation.mutate(s)}
												className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors disabled:opacity-50 ${
													s.enabled
														? "bg-primary-600"
														: "bg-secondary-300 dark:bg-secondary-600"
												}`}
											>
												<span
													className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
														s.enabled ? "translate-x-4" : "translate-x-0.5"
													}`}
												/>
											</button>
										</td>
										{canManage && (
											<td className="px-4 py-3 whitespace-nowrap text-right">
												<div className="flex items-center justify-end gap-2">
													<button
														type="button"
														onClick={() => handleRun(s)}
														disabled={runMutation.isPending}
														className="p-1.5 rounded hover:bg-secondary-100 dark:hover:bg-secondary-700 text-primary-600 dark:text-primary-400 disabled:opacity-50"
														title={t("Run now")}
													>
														<Play className="h-4 w-4" />
													</button>
													<button
														type="button"
														onClick={() => openEdit(s)}
														className="p-1.5 rounded hover:bg-secondary-100 dark:hover:bg-secondary-700 text-secondary-600 dark:text-secondary-300"
														title={t("Edit")}
													>
														<Edit className="h-4 w-4" />
													</button>
													<button
														type="button"
														onClick={() => handleDelete(s)}
														className="p-1.5 rounded hover:bg-red-100 dark:hover:bg-red-900/30 text-red-600"
														title={t("Delete")}
													>
														<Trash2 className="h-4 w-4" />
													</button>
												</div>
											</td>
										)}
									</tr>
								))}
							</tbody>
						</table>
					</div>
				)}
			</div>

			{form && (
				<ModalPortal>
					<div className="fixed inset-0 z-[120] flex items-center justify-center p-4 bg-black/50">
						<div className="bg-white dark:bg-secondary-800 rounded-lg shadow-xl max-w-md w-full max-h-[90vh] overflow-y-auto">
							<div className="flex items-center justify-between p-4 border-b border-secondary-200 dark:border-secondary-600">
								<h3 className="text-lg font-semibold text-secondary-900 dark:text-white">
									{editing ? t("Edit schedule") : t("Create schedule")}
								</h3>
								<button
									type="button"
									onClick={() => setForm(null)}
									className="p-1 rounded hover:bg-secondary-100 dark:hover:bg-secondary-700"
									aria-label={t("Close")}
								>
									<X className="h-5 w-5" />
								</button>
							</div>
							<form onSubmit={handleSubmit} className="p-4 space-y-4">
								<div>
									<label htmlFor="schedule-name" className={labelClass}>
										{t("Name")}
									</label>
									<input
										id="schedule-name"
										type="text"
										value={form.name}
										maxLength={100}
										onChange={(e) =>
											setForm((f) => ({ ...f, name: e.target.value }))
										}
										className={inputClass}
										required
									/>
								</div>
								<div>
									<label htmlFor="schedule-group" className={labelClass}>
										{t("Hosts")}
									</label>
									<select
										id="schedule-group"
										value={form.host_group_id}
										onChange={(e) =>
											setForm((f) => ({ ...f, host_group_id: e.target.value }))
										}
										className={inputClass}
									>
										<option value="">{t("All hosts")}</option>
										{hostGroups.map((g) => (
											<option key={g.id} value={g.id}>
												{g.name}
											</option>
										))}
									</select>
								</div>
								<fieldset>
									<legend className={labelClass}>{t("Updates")}</legend>
									<div className="flex gap-4 text-sm text-secondary-700 dark:text-secondary-300">
										{[
											["security", t("Security updates")],
											["all", t("All updates")],
										].map(([value, label]) => (
											<label key={value} className="flex items-center gap-2">
												<input
													type="radio"
													name="schedule-scope"
													value={value}
													checked={form.scope === value}
													onChange={() =>
														setForm((f) => ({ ...f, scope: value }))
													}
												/>
												{label}
											</label>
										))}
									</div>
								</fieldset>
								<div>
									<label htmlFor="schedule-frequency" className={labelClass}>
										{t("Frequency")}
									</label>
									<select
										id="schedule-frequency"
										value={form.frequency}
										onChange={(e) =>
											setForm((f) => ({ ...f, frequency: e.target.value }))
										}
										className={inputClass}
									>
										<option value="weekly">{t("Weekly")}</option>
										<option value="monthly">{t("Monthly")}</option>
									</select>
								</div>
								{form.frequency === "weekly" ? (
									<fieldset>
										<legend className={labelClass}>{t("Days")}</legend>
										<div className="flex flex-wrap gap-2">
											{WEEK_ORDER.map((day) => (
												<label
													key={day}
													className={`px-2.5 py-1 rounded border text-sm cursor-pointer ${
														form.weekdays.includes(day)
															? "border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/40 dark:text-primary-300"
															: "border-secondary-300 dark:border-secondary-600 text-secondary-700 dark:text-secondary-300"
													}`}
												>
													<input
														type="checkbox"
														className="sr-only"
														checked={form.weekdays.includes(day)}
														onChange={() => toggleDay(day)}
													/>
													{weekdayName(day, "short")}
												</label>
											))}
										</div>
									</fieldset>
								) : (
									<div className="grid grid-cols-2 gap-3">
										<div>
											<label htmlFor="schedule-week" className={labelClass}>
												{t("Week of the month")}
											</label>
											<select
												id="schedule-week"
												value={form.month_week}
												onChange={(e) =>
													setForm((f) => ({
														...f,
														month_week: Number(e.target.value),
													}))
												}
												className={inputClass}
											>
												{MONTH_WEEKS.map((w) => (
													<option key={w.value} value={w.value}>
														{t(w.label)}
													</option>
												))}
											</select>
										</div>
										<div>
											<label htmlFor="schedule-weekday" className={labelClass}>
												{t("Day")}
											</label>
											<select
												id="schedule-weekday"
												value={form.month_weekday}
												onChange={(e) =>
													setForm((f) => ({
														...f,
														month_weekday: Number(e.target.value),
													}))
												}
												className={inputClass}
											>
												{WEEK_ORDER.map((day) => (
													<option key={day} value={day}>
														{weekdayName(day)}
													</option>
												))}
											</select>
										</div>
									</div>
								)}
								<div>
									<label htmlFor="schedule-time" className={labelClass}>
										{t("Time ({{timezone}})", { timezone })}
									</label>
									<input
										id="schedule-time"
										type="time"
										value={form.time_of_day}
										onChange={(e) =>
											setForm((f) => ({ ...f, time_of_day: e.target.value }))
										}
										className={inputClass}
										required
									/>
								</div>
								<label className="flex items-center gap-2 text-sm text-secondary-700 dark:text-secondary-300">
									<input
										type="checkbox"
										checked={form.enabled}
										onChange={(e) =>
											setForm((f) => ({ ...f, enabled: e.target.checked }))
										}
									/>
									{t("Enabled")}
								</label>
								<div className="flex justify-end gap-2 pt-2">
									<button
										type="button"
										onClick={() => setForm(null)}
										className="btn-outline"
									>
										{t("Cancel")}
									</button>
									<button
										type="submit"
										disabled={saveMutation.isPending}
										className="btn-primary disabled:opacity-50"
									>
										{saveMutation.isPending ? t("Saving...") : t("Save")}
									</button>
								</div>
							</form>
						</div>
					</div>
				</ModalPortal>
			)}
		</div>
	);
}
