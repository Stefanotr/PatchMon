import i18n, { tk } from "../i18n";
import api from "./api";
import { skippedItemsSummary } from "./patchRun";

const DRY_RUN_POLL_INTERVAL_MS = 2000;
const DRY_RUN_MAX_POLL_INTERVAL_MS = 10000;
const DRY_RUN_TIMEOUT_MS = 30000;
// Covers the agent's Windows Update search (15 min) and winget list (5 min) limits.
const DRY_RUN_RUNNING_CAP_MS = 25 * 60 * 1000;
const DRY_RUN_MAX_FETCH_ERRORS = 3;

export const STILL_RUNNING_MESSAGE =
	"Validation is still running on the host. Check Runs & History for the result.";

/**
 * Build the absolute WebSocket URL for the live patch-run output stream.
 * The browser auto-attaches the auth cookie on same-origin upgrades, so no
 * ticket is required — the backend uses the shared JWT middleware.
 */
export function buildRunStreamURL(runId) {
	const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
	const host = window.location.host;
	return `${protocol}//${host}/api/v1/patching/runs/${encodeURIComponent(
		runId,
	)}/stream`;
}

const DRY_RUN_FAILED_STATUSES = ["failed", "cancelled", "timed_out"];
// The server lets a reconnecting agent move agent_disconnected back to running.
const DRY_RUN_ACTIVE_STATUSES = ["running", "agent_disconnected"];

const sleep = (ms, signal) =>
	new Promise((resolve) => {
		const timer = setTimeout(done, ms);
		function done() {
			clearTimeout(timer);
			signal?.removeEventListener("abort", done);
			resolve();
		}
		signal?.addEventListener("abort", done);
	});

/**
 * Poll a patch run until it reaches a terminal state or timeout.
 * The 30s offline timeout only applies until the server has dispatched the run
 * to the agent: a Windows Update search can take minutes.
 * onStatus receives each raw run status while the run is still in progress.
 * Returns { status, packages_affected, error }, or null once signal aborts.
 */
export async function pollDryRunUntilDone(runId, { signal, onStatus } = {}) {
	const start = Date.now();
	let interval = DRY_RUN_POLL_INTERVAL_MS;
	let fetchErrors = 0;
	for (;;) {
		if (signal?.aborted) return null;
		let run;
		try {
			run = await api.get(`/patching/runs/${runId}`).then((r) => r.data);
			fetchErrors = 0;
		} catch (err) {
			if (signal?.aborted) return null;
			fetchErrors += 1;
			if (fetchErrors >= DRY_RUN_MAX_FETCH_ERRORS) throw err;
			await sleep(DRY_RUN_POLL_INTERVAL_MS * 2 ** fetchErrors, signal);
			continue;
		}
		if (signal?.aborted) return null;
		const status = run?.status;
		if (
			status === "validated" ||
			status === "completed" ||
			status === "dry_run_completed"
		) {
			return {
				status: "validated",
				packages_affected: run.packages_affected || [],
				shell_output: run.shell_output || "",
				skip_note: skippedItemsSummary(run),
				error: null,
			};
		}
		if (DRY_RUN_FAILED_STATUSES.includes(status)) {
			return {
				status: "failed",
				packages_affected: [],
				shell_output: run.shell_output || "",
				error: run.error_message || "Dry run failed",
			};
		}
		const active = DRY_RUN_ACTIVE_STATUSES.includes(status);
		const elapsed = Date.now() - start;
		if (active && elapsed >= DRY_RUN_RUNNING_CAP_MS) {
			if (status === "agent_disconnected") {
				return {
					status: "failed",
					packages_affected: [],
					shell_output: run.shell_output || "",
					error: run.error_message || "Agent disconnected during validation",
				};
			}
			return {
				status: "still_running",
				packages_affected: [],
				shell_output: run.shell_output || "",
				error: STILL_RUNNING_MESSAGE,
			};
		}
		if (!active && elapsed >= DRY_RUN_TIMEOUT_MS) {
			return {
				status: "timeout",
				packages_affected: [],
				shell_output: "",
				error: "Validation skipped (host offline)",
			};
		}
		onStatus?.(status);
		if (active) {
			interval = Math.min(
				interval + DRY_RUN_POLL_INTERVAL_MS,
				DRY_RUN_MAX_POLL_INTERVAL_MS,
			);
		}
		await sleep(interval, signal);
	}
}

// Restart choices. The agent never restarts sooner than this after a run,
// so its final status and the inventory refresh reach the server first.
export const REBOOT_MIN_DELAY_MINUTES = 5;
export const REBOOT_MAX_DELAY_MINUTES = 7 * 24 * 60;
export const REBOOT_MAX_MESSAGE_LENGTH = 200;

export const DEFAULT_RESTART_CHOICE = {
	mode: "never",
	when: "delay",
	delayMinutes: 5,
	atTime: "03:00",
	message: "",
};

/**
 * Next time the clock shows hh:mm in the browser's time zone: today if it is
 * still ahead, tomorrow otherwise. The agent pushes a time closer than its
 * minimum delay back to that minimum.
 */
export function nextOccurrence(hhmm, now = new Date()) {
	const [h, m] = String(hhmm).split(":").map(Number);
	const t = new Date(now);
	t.setHours(h, m, 0, 0);
	if (t.getTime() <= now.getTime()) {
		t.setDate(t.getDate() + 1);
	}
	return t;
}

const utf8Length = (s) => new TextEncoder().encode(s).length;

/**
 * Validate a restart choice; returns an error message or null.
 */
export function restartChoiceError(choice) {
	if (!choice || choice.mode === "never") return null;
	if (utf8Length((choice.message || "").trim()) > REBOOT_MAX_MESSAGE_LENGTH) {
		return i18n.t("The message must be at most {{max}} bytes", {
			max: REBOOT_MAX_MESSAGE_LENGTH,
		});
	}
	if (choice.when === "at") {
		return /^([01]\d|2[0-3]):[0-5]\d$/.test(choice.atTime || "")
			? null
			: i18n.t("Pick a time of day");
	}
	const d = Number(choice.delayMinutes);
	if (
		!Number.isInteger(d) ||
		d < REBOOT_MIN_DELAY_MINUTES ||
		d > REBOOT_MAX_DELAY_MINUTES
	) {
		return i18n.t("The delay must be between {{min}} and {{max}} minutes", {
			min: REBOOT_MIN_DELAY_MINUTES,
			max: REBOOT_MAX_DELAY_MINUTES,
		});
	}
	return null;
}

/**
 * Turn a restart choice into the API's reboot object, or null when nothing
 * restarts. mode overrides the choice's mode (the restart command on a host
 * always restarts).
 */
export function buildRebootRequest(choice, { mode, now = new Date() } = {}) {
	const effectiveMode = mode || choice?.mode;
	if (!choice || !effectiveMode || effectiveMode === "never") return null;
	const body = { mode: effectiveMode };
	if (choice.when === "at") {
		body.at = nextOccurrence(choice.atTime, now).toISOString();
	} else {
		body.delay_minutes = Number(choice.delayMinutes);
	}
	const message = (choice.message || "").trim();
	if (message) body.message = message;
	return body;
}

// The fixed messages the server answers restart commands with, translated
// when shown. Any other server text is shown as it comes.
const SERVER_RESTART_MESSAGES = new Set([
	tk("Restart scheduled"),
	tk("Scheduled restart cancelled"),
	tk(
		"Sent to the agent, which did not answer in time: check the host. Agents older than this feature ignore restart commands.",
	),
]);
const serverMessage = (text) =>
	SERVER_RESTART_MESSAGES.has(text) ? i18n.t(text) : text;

/**
 * Turn the server's answer to a restart or cancel command into a toast:
 * { level: "success" | "warning", text }. 202 means the command went out but
 * the agent did not answer in time, so nothing is confirmed.
 */
export function describeRestartAnswer(res, fallback) {
	const data = res?.data || {};
	const serverText = data.message ? serverMessage(data.message) : "";
	if (res?.status === 202) {
		return {
			level: "warning",
			text: serverText || i18n.t("Sent to the agent, which did not answer"),
		};
	}
	const text = serverText || fallback;
	return {
		level: "success",
		text: data.reboot_at
			? i18n.t("{{text}}, for {{time}}", {
					text,
					time: new Date(data.reboot_at).toLocaleString(),
				})
			: text,
	};
}

/**
 * Describe the restart option recorded on a run, for the run detail page.
 */
export function describeReboot(reboot) {
	if (!reboot?.mode || reboot.mode === "never") return null;
	const what =
		reboot.mode === "always"
			? i18n.t("Restart after the run")
			: i18n.t("Restart after the run if one is needed");
	const when = reboot.at
		? i18n.t("at {{time}}", { time: new Date(reboot.at).toLocaleString() })
		: i18n.t("{{minutes}} min after it ends", {
				minutes: Math.max(reboot.delay_minutes || 0, REBOOT_MIN_DELAY_MINUTES),
			});
	return i18n.t("{{what}}, {{when}}", { what, when });
}

export const patchingAPI = {
	getDashboard: () => api.get("/patching/dashboard").then((res) => res.data),
	getRuns: (params = {}) =>
		api.get("/patching/runs", { params }).then((res) => res.data),
	getActiveRuns: () => api.get("/patching/runs/active").then((res) => res.data),
	getRunById: (id) => api.get(`/patching/runs/${id}`).then((res) => res.data),
	trigger: (
		host_id,
		patch_type,
		package_name = null,
		package_names = null,
		opts = {},
	) =>
		api
			.post("/patching/trigger", {
				host_id,
				patch_type,
				...(package_name ? { package_name } : {}),
				...(Array.isArray(package_names) && package_names.length > 0
					? { package_names }
					: {}),
				...(opts.dry_run ? { dry_run: true } : {}),
				...(opts.pending_approval ? { pending_approval: true } : {}),
				...(opts.schedule_override
					? { schedule_override: opts.schedule_override }
					: {}),
				...(opts.reboot ? { reboot: opts.reboot } : {}),
				...(opts.windows_scope
					? {
							windows_scope: {
								updates: !!opts.windows_scope.updates,
								apps: !!opts.windows_scope.apps,
							},
						}
					: {}),
			})
			.then((res) => res.data),
	approveRun: (id, opts = {}) =>
		api
			.post(`/patching/runs/${id}/approve`, {
				...(opts.schedule_override
					? { schedule_override: opts.schedule_override }
					: {}),
				...(opts.reboot ? { reboot: opts.reboot } : {}),
			})
			.then((res) => res.data),
	retryValidation: (id) =>
		api.post(`/patching/runs/${id}/retry-validation`).then((res) => res.data),
	stopRun: (id) =>
		api.post(`/patching/runs/${id}/stop`).then((res) => res.data),
	deleteRun: (id) => api.delete(`/patching/runs/${id}`),
	getPreviewRun: (host_id) =>
		api
			.get("/patching/preview-run", { params: { host_id } })
			.then((res) => res.data),

	// Policies
	getPolicies: () => api.get("/patching/policies").then((res) => res.data),
	getPolicyById: (id) =>
		api.get(`/patching/policies/${id}`).then((res) => res.data),
	createPolicy: (data) =>
		api.post("/patching/policies", data).then((res) => res.data),
	updatePolicy: (id, data) =>
		api.put(`/patching/policies/${id}`, data).then((res) => res.data),
	deletePolicy: (id) => api.delete(`/patching/policies/${id}`),
	getPolicyAssignments: (id) =>
		api.get(`/patching/policies/${id}/assignments`).then((res) => res.data),
	addPolicyAssignment: (id, target_type, target_id) =>
		api
			.post(`/patching/policies/${id}/assignments`, {
				target_type,
				target_id,
			})
			.then((res) => res.data),
	removePolicyAssignment: (id, assignmentId) =>
		api.delete(`/patching/policies/${id}/assignments/${assignmentId}`),
	addPolicyExclusion: (id, host_id) =>
		api
			.post(`/patching/policies/${id}/exclusions`, { host_id })
			.then((res) => res.data),
	removePolicyExclusion: (id, hostId) =>
		api.delete(`/patching/policies/${id}/exclusions/${hostId}`),
};
