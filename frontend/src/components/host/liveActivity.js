import i18n, { tk } from "../../i18n";

// What the live part of a host's activity shows: the tasks the server
// returns (GET /dashboard/hosts/:id/activity/live) and how to word them.
// Values compared here are the API's; labels are translated where shown.

// Steps of an agent update, in order. An update the agent started itself
// (after a report) has no "sent" step.
export const AGENT_UPDATE_STEPS = [
	{ stage: "sent", label: tk("Sent") },
	{ stage: "downloading", label: tk("Download") },
	{ stage: "verifying", label: tk("Verification") },
	{ stage: "installing", label: tk("Installation") },
	{ stage: "restarting", label: tk("Agent restart") },
	{ stage: "done", label: tk("Back online") },
];

// What the agent is doing at each stage, as a sentence.
export const AGENT_UPDATE_STAGE_TEXT = {
	sent: tk("Sent to the agent, waiting for it to start"),
	checking: tk("The agent is checking the version available"),
	downloading: tk("Downloading the new agent"),
	verifying: tk("Checking the downloaded agent"),
	installing: tk("Installing the new agent"),
	restarting: tk("Restarting the agent"),
	done: tk("Updated"),
	up_to_date: tk("The agent was already up to date"),
	skipped: tk("Postponed by the agent"),
	failed: tk("Update failed"),
};

// Short stage names, for the pill in the host header.
export const AGENT_UPDATE_STAGE_SHORT = {
	sent: tk("sent"),
	checking: tk("checking"),
	downloading: tk("downloading"),
	verifying: tk("verifying"),
	installing: tk("installing"),
	restarting: tk("restarting"),
};

// Index of the step a stage belongs to (checking is still the first one).
export function agentUpdateStepIndex(stage) {
	if (stage === "checking") return 0;
	return AGENT_UPDATE_STEPS.findIndex((s) => s.stage === stage);
}

// steps returns the steps an update goes through, each with its state:
// done, current, error or pending.
export function agentUpdateSteps(task) {
	const steps =
		task.trigger === "agent"
			? AGENT_UPDATE_STEPS.filter((s) => s.stage !== "sent")
			: AGENT_UPDATE_STEPS;
	const recorded = task.stages || {};
	let reached = agentUpdateStepIndex(task.stage);
	// Failed as the agent said, or closed for want of news (the stage is then
	// the last one reported).
	const failed =
		task.stage === "failed" || (task.finished && task.status === "failed");
	if (failed) {
		// The step that failed: the last one the update reached.
		reached = Math.max(
			0,
			...Object.keys(recorded).map((s) => agentUpdateStepIndex(s)),
		);
	}
	const done = task.stage === "done";
	return steps.map((step) => {
		const index = agentUpdateStepIndex(step.stage);
		let state = "pending";
		if (done || index < reached) state = "done";
		else if (index === reached) state = failed ? "error" : "current";
		return { ...step, state, at: recorded[step.stage] || null };
	});
}

// isEarlyEnd: an update that ended without going through its steps.
export const isEarlyEnd = (task) =>
	task.stage === "up_to_date" || task.stage === "skipped";

// agentUpdatePillText is the header pill's text for an open update.
export function agentUpdatePillText(task) {
	if (task.kind === "queued") return i18n.t("Agent update queued");
	const stage = AGENT_UPDATE_STAGE_SHORT[task.stage];
	if (!stage) return i18n.t("Agent update in progress");
	if (task.stage === "downloading" && typeof task.percent === "number") {
		return i18n.t("Agent update: {{stage}} {{percent}}%", {
			stage: i18n.t(stage),
			percent: task.percent,
		});
	}
	return i18n.t("Agent update: {{stage}}", { stage: i18n.t(stage) });
}

// No news from an agent this long after the command went out: it may be
// older than this server and not report its stages.
export const AGENT_SILENT_MS = 60 * 1000;

// Patch run statuses, as the live view words them.
export const PATCH_RUN_STATUS = {
	queued: tk("Queued"),
	running: tk("Running"),
	completed: tk("Completed"),
	failed: tk("Failed"),
	cancelled: tk("Cancelled"),
	timed_out: tk("Timed out"),
	agent_disconnected: tk("Agent disconnected"),
	validated: tk("Validated"),
	dry_run_completed: tk("Dry run completed"),
};

export const PATCH_RUN_OK = new Set([
	"completed",
	"validated",
	"dry_run_completed",
]);

// Compliance scan phases the agent reports.
export const COMPLIANCE_PHASE = {
	started: tk("Starting"),
	evaluating: tk("Evaluating the rules"),
	parsing: tk("Processing the results"),
	sending: tk("Sending the results"),
	completed: tk("Completed"),
	failed: tk("Failed"),
	cancelled: tk("Cancelled"),
};

// Wording of a job's status in the live view. A job row waiting is waiting
// for the agent's answer; a queued task waiting is not picked up yet.
export function jobStatusText(task) {
	switch (task.status) {
		case "active":
			return i18n.t("In progress");
		case "waiting":
			return task.kind === "queued"
				? i18n.t("Queued")
				: i18n.t("Waiting for the agent's answer");
		case "delayed":
			return task.attempt > 1 ? i18n.t("Retry scheduled") : i18n.t("Scheduled");
		case "completed":
			return i18n.t("Completed");
		case "failed":
			return i18n.t("Failed");
		default:
			return task.status;
	}
}

// formatElapsed words a duration the way a live counter shows it.
export function formatElapsed(ms) {
	const total = Math.max(0, Math.floor(ms / 1000));
	const h = Math.floor(total / 3600);
	const m = Math.floor((total % 3600) / 60);
	const s = total % 60;
	if (h > 0) return i18n.t("{{h}} h {{m}} min", { h, m });
	if (m > 0) return i18n.t("{{m}} min {{s}} s", { m, s });
	return i18n.t("{{s}} s", { s });
}

// isOpen: a task not over yet (running, queued or scheduled).
export const isOpen = (task) => !task.finished;

// isRunning: work going on, or about to (what makes the page poll fast and
// shows a spinner); a task scheduled for later is open but not running.
export const isRunning = (task) => !task.finished && task.running !== false;

// isAgentUpdate: an agent update, recorded or still in the queue.
export const isAgentUpdate = (task) =>
	(task.kind === "job" || task.kind === "queued") &&
	task.name === "update_agent";

// taskKey identifies a task across polls.
export const taskKey = (task) => `${task.kind}:${task.id}`;
