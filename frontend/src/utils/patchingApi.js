import api from "./api";

const DRY_RUN_POLL_INTERVAL_MS = 2000;
// How long a dry run may wait for the agent to pick it up before the host is
// reported offline.
const DRY_RUN_PICKUP_TIMEOUT_MS = 30000;
// How long to wait for a dry run the agent is working on. A Windows host runs
// an online Windows Update search first, which commonly takes minutes.
const DRY_RUN_MAX_WAIT_MS = 15 * 60 * 1000;
// Terminal statuses that end a dry run without a validation result.
const DRY_RUN_ABORTED_STATUSES = {
	cancelled: "Dry run cancelled",
	timed_out: "Dry run timed out on the host",
	agent_disconnected: "Agent disconnected during the dry run",
};

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

/**
 * Poll a patch run until it reaches a terminal state (validated, completed,
 * failed, cancelled, timed out) or the wait runs out.
 *
 * A run the agent never picks up times out after pickupTimeoutMs as "host
 * offline". Once the agent is running it, the wait extends to maxWaitMs; if
 * that runs out too, the result is a timeout flagged still_running, since the
 * run carries on and its result lands on the run itself.
 *
 * Returns { status, packages_affected, shell_output, error, still_running? }.
 */
export async function pollDryRunUntilDone(runId, opts = {}) {
	const pickupTimeoutMs = opts.pickupTimeoutMs ?? DRY_RUN_PICKUP_TIMEOUT_MS;
	const maxWaitMs = opts.maxWaitMs ?? DRY_RUN_MAX_WAIT_MS;
	const intervalMs = opts.intervalMs ?? DRY_RUN_POLL_INTERVAL_MS;
	const start = Date.now();
	let pickedUp = false;
	for (;;) {
		const run = await api.get(`/patching/runs/${runId}`).then((r) => r.data);
		const status = run?.status;
		if (status === "validated" || status === "completed") {
			return {
				status: "validated",
				packages_affected: run.packages_affected || [],
				shell_output: run.shell_output || "",
				error: null,
			};
		}
		if (
			status === "failed" ||
			Object.hasOwn(DRY_RUN_ABORTED_STATUSES, status)
		) {
			return {
				status: "failed",
				packages_affected: [],
				shell_output: run.shell_output || "",
				error:
					run.error_message ||
					DRY_RUN_ABORTED_STATUSES[status] ||
					"Dry run failed",
			};
		}
		if (status === "running") pickedUp = true;

		const elapsed = Date.now() - start;
		if (!pickedUp && elapsed >= pickupTimeoutMs) {
			return {
				status: "timeout",
				packages_affected: [],
				shell_output: "",
				error: "Validation skipped (host offline)",
			};
		}
		if (elapsed >= maxWaitMs) {
			return {
				status: "timeout",
				still_running: true,
				packages_affected: [],
				shell_output: run?.shell_output || "",
				error:
					"Validation is still running on the host; its result will appear on the run in Patching",
			};
		}
		await new Promise((r) => setTimeout(r, intervalMs));
	}
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
			})
			.then((res) => res.data),
	approveRun: (id, opts = {}) =>
		api
			.post(`/patching/runs/${id}/approve`, {
				...(opts.schedule_override
					? { schedule_override: opts.schedule_override }
					: {}),
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
