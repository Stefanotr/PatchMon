import {
	AlertTriangle,
	ArrowRight,
	Check,
	CheckCircle2,
	Clock,
	Download,
	Loader2,
	ShieldCheck,
	Wrench,
	X,
	XCircle,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { useTick } from "../../hooks/useTick";
import { formatDate, formatRelativeTime } from "../../utils/api";
import { formatJobLabel } from "./ActivityTypeBadge";
import {
	AGENT_SILENT_MS,
	AGENT_UPDATE_STAGE_TEXT,
	agentUpdateSteps,
	COMPLIANCE_PHASE,
	formatElapsed,
	isAgentUpdate,
	isEarlyEnd,
	isOpen,
	isRunning,
	jobStatusText,
	PATCH_RUN_OK,
	PATCH_RUN_STATUS,
	taskKey,
} from "./liveActivity";

// The live part of a host's activity: what its agent is doing now, step by
// step, and what just ended. Tasks come from useHostLiveActivity.

const toneOf = (task) => {
	if (isRunning(task)) return "running";
	if (isOpen(task)) return "idle";
	if (task.kind === "patch_run") {
		return PATCH_RUN_OK.has(task.status) ? "ok" : "error";
	}
	if (task.stage === "skipped") return "warn";
	return task.status === "completed" ? "ok" : "error";
};

const TONE = {
	idle: {
		ring: "border-secondary-200 dark:border-secondary-600",
		bg: "bg-secondary-50/60 dark:bg-secondary-800/40",
	},
	running: {
		ring: "border-primary-200 dark:border-primary-800",
		bg: "bg-primary-50/60 dark:bg-primary-900/20",
	},
	ok: {
		ring: "border-success-200 dark:border-success-800",
		bg: "bg-success-50/60 dark:bg-success-900/20",
	},
	warn: {
		ring: "border-warning-200 dark:border-warning-700",
		bg: "bg-warning-50/60 dark:bg-warning-900/20",
	},
	error: {
		ring: "border-danger-200 dark:border-danger-800",
		bg: "bg-danger-50/60 dark:bg-danger-900/20",
	},
};

const ToneIcon = ({ tone, className = "h-5 w-5" }) => {
	if (tone === "running")
		return (
			<Loader2
				className={`${className} animate-spin text-primary-600 dark:text-primary-400`}
			/>
		);
	if (tone === "idle")
		return (
			<Clock
				className={`${className} text-secondary-500 dark:text-secondary-300`}
			/>
		);
	if (tone === "ok")
		return (
			<CheckCircle2
				className={`${className} text-success-600 dark:text-success-400`}
			/>
		);
	if (tone === "warn")
		return (
			<AlertTriangle
				className={`${className} text-warning-600 dark:text-warning-400`}
			/>
		);
	return (
		<XCircle className={`${className} text-danger-600 dark:text-danger-400`} />
	);
};

// When: "for 12 s" while running, "planned in 5 min" for later, "ended 20 s
// ago" once over.
const TaskTiming = ({ task, now }) => {
	const { t } = useTranslation();
	if (isOpen(task) && task.scheduled_at && new Date(task.scheduled_at) > now) {
		return (
			<span title={formatDate(task.scheduled_at)}>
				{t("planned {{when}}", {
					when: formatRelativeTime(task.scheduled_at),
				})}
			</span>
		);
	}
	if (isOpen(task)) {
		if (!task.started_at) return null;
		return (
			<span title={formatDate(task.started_at)}>
				{t("for {{elapsed}}", {
					elapsed: formatElapsed(now - new Date(task.started_at)),
				})}
			</span>
		);
	}
	if (!task.finished_at) return null;
	return (
		<span title={formatDate(task.finished_at)}>
			{t("ended {{elapsed}} ago", {
				elapsed: formatElapsed(now - new Date(task.finished_at)),
			})}
		</span>
	);
};

const TaskCard = ({ task, icon, title, badge, now, children }) => {
	const tone = toneOf(task);
	return (
		<li
			className={`rounded-lg border ${TONE[tone].ring} ${TONE[tone].bg} p-3 sm:p-4 transition-colors`}
		>
			<div className="flex items-start gap-3">
				<div className="flex-shrink-0 mt-0.5">
					<ToneIcon tone={tone} />
				</div>
				<div className="min-w-0 flex-1 space-y-2">
					<div className="flex flex-wrap items-center gap-x-3 gap-y-1">
						<span className="inline-flex items-center gap-1.5 text-sm font-semibold text-secondary-900 dark:text-white">
							{icon}
							{title}
						</span>
						{badge}
						<span className="ml-auto text-xs text-secondary-500 dark:text-secondary-300 whitespace-nowrap">
							<TaskTiming task={task} now={now} />
						</span>
					</div>
					{children}
				</div>
			</div>
		</li>
	);
};

const StepDot = ({ state }) => {
	const base =
		"flex h-6 w-6 items-center justify-center rounded-full border-2 flex-shrink-0";
	if (state === "done")
		return (
			<span className={`${base} border-success-500 bg-success-500 text-white`}>
				<Check className="h-3.5 w-3.5" />
			</span>
		);
	if (state === "current")
		return (
			<span
				className={`${base} border-primary-500 bg-white dark:bg-secondary-800 text-primary-600`}
			>
				<Loader2 className="h-3.5 w-3.5 animate-spin" />
			</span>
		);
	if (state === "error")
		return (
			<span className={`${base} border-danger-500 bg-danger-500 text-white`}>
				<X className="h-3.5 w-3.5" />
			</span>
		);
	return (
		<span
			className={`${base} border-secondary-300 dark:border-secondary-600 bg-white dark:bg-secondary-800`}
		/>
	);
};

// Stepper of an agent update: one dot per step, joined by a line that fills
// as the update goes. Phones show the dots only: the line under the stepper
// says where the update is.
const AgentUpdateStepper = ({ task }) => {
	const { t } = useTranslation();
	const steps = agentUpdateSteps(task);
	return (
		<ol className="flex items-start" data-testid="agent-update-steps">
			{steps.map((step, i) => (
				<li
					key={step.stage}
					className="relative flex flex-1 flex-col items-center min-w-0"
					data-state={step.state}
					title={step.at ? formatDate(step.at) : undefined}
				>
					{i > 0 && (
						<span
							aria-hidden="true"
							className={`absolute top-3 right-1/2 h-0.5 w-full -translate-y-1/2 ${
								step.state === "pending"
									? "bg-secondary-200 dark:bg-secondary-600"
									: step.state === "error"
										? "bg-danger-300 dark:bg-danger-700"
										: "bg-success-400 dark:bg-success-600"
							}`}
						/>
					)}
					<span className="relative z-10">
						<StepDot state={step.state} />
					</span>
					<span
						className={`sr-only sm:not-sr-only sm:mt-1 sm:px-0.5 text-center text-[11px] leading-tight break-words ${
							step.state === "current"
								? "font-semibold text-primary-700 dark:text-primary-300"
								: step.state === "error"
									? "font-semibold text-danger-700 dark:text-danger-300"
									: "text-secondary-600 dark:text-secondary-300"
						}`}
					>
						{t(step.label)}
					</span>
				</li>
			))}
		</ol>
	);
};

const ProgressBar = ({ percent }) => (
	<div
		className="h-1.5 w-full overflow-hidden rounded-full bg-secondary-200 dark:bg-secondary-700"
		role="progressbar"
		aria-valuenow={percent}
		aria-valuemin={0}
		aria-valuemax={100}
	>
		<div
			className="h-full rounded-full bg-primary-500 transition-all duration-500"
			style={{ width: `${percent}%` }}
		/>
	</div>
);

const Versions = ({ from, to }) => {
	if (!from && !to) return null;
	return (
		<span className="inline-flex items-center gap-1 rounded bg-secondary-100 dark:bg-secondary-700 px-1.5 py-0.5 font-mono text-xs text-secondary-700 dark:text-secondary-200">
			{from ? `v${from}` : "?"}
			<ArrowRight className="h-3 w-3" />
			{to ? `v${to}` : "?"}
		</span>
	);
};

const AgentUpdateCard = ({ task, now }) => {
	const { t } = useTranslation();
	const silent =
		task.stage === "sent" &&
		isOpen(task) &&
		task.started_at &&
		now - new Date(task.started_at) > AGENT_SILENT_MS;
	const stageText = AGENT_UPDATE_STAGE_TEXT[task.stage];
	let line = stageText ? t(stageText) : null;
	if (task.stage === "up_to_date" && task.from_version) {
		line = t("The agent was already up to date (v{{version}})", {
			version: task.from_version,
		});
	}
	if (task.stage === "done" && task.to_version) {
		line = t("Updated to v{{version}}, the agent is back online", {
			version: task.to_version,
		});
	}
	const downloading =
		task.stage === "downloading" && typeof task.percent === "number";
	return (
		<TaskCard
			task={task}
			now={now}
			icon={<Download className="h-4 w-4 text-secondary-500" />}
			title={t("Agent update")}
			badge={
				<>
					<Versions from={task.from_version} to={task.to_version} />
					{task.trigger === "agent" && (
						<span className="text-xs text-secondary-500 dark:text-secondary-300">
							{t("started by the agent")}
						</span>
					)}
				</>
			}
		>
			{!isEarlyEnd(task) && <AgentUpdateStepper task={task} />}
			{downloading && <ProgressBar percent={task.percent} />}
			{line && (
				<p className="text-sm text-secondary-700 dark:text-secondary-200">
					{line}
					{downloading && ` (${task.percent}%)`}
				</p>
			)}
			{task.error && (
				<p className="text-sm text-danger-700 dark:text-danger-300 break-words">
					{task.error}
				</p>
			)}
			{silent && (
				<p className="text-xs text-secondary-600 dark:text-secondary-300">
					{t(
						"No news from the agent yet. Agents older than this server do not report their steps: the update ends when the agent comes back on the new version.",
					)}
				</p>
			)}
		</TaskCard>
	);
};

const PatchRunCard = ({ task, now }) => {
	const { t } = useTranslation();
	const status = PATCH_RUN_STATUS[task.status];
	return (
		<TaskCard
			task={task}
			now={now}
			icon={<Wrench className="h-4 w-4 text-secondary-500" />}
			title={
				task.name === "patch_all"
					? t("Patch all packages")
					: task.package
						? t("Patch {{package}}", { package: task.package })
						: t("Patch run")
			}
			badge={
				<>
					<span className="text-xs font-medium text-secondary-600 dark:text-secondary-300">
						{status ? t(status) : task.status}
					</span>
					{task.dry_run && (
						<span className="rounded bg-secondary-100 dark:bg-secondary-700 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-secondary-600 dark:text-secondary-300">
							{t("Dry run")}
						</span>
					)}
				</>
			}
		>
			{task.message && (
				<p className="truncate rounded bg-secondary-900/90 px-2 py-1 font-mono text-xs text-secondary-100 dark:bg-black/40">
					{task.message}
				</p>
			)}
			{task.error && (
				<p className="text-sm text-danger-700 dark:text-danger-300 break-words">
					{task.error}
				</p>
			)}
			<Link
				to={`/patching/runs/${task.id}`}
				className="inline-flex items-center gap-1 text-xs font-medium text-primary-600 hover:underline dark:text-primary-400"
			>
				{t("Open the run")}
				<ArrowRight className="h-3 w-3" />
			</Link>
		</TaskCard>
	);
};

const ComplianceCard = ({ task, now }) => {
	const { t } = useTranslation();
	const phase = COMPLIANCE_PHASE[task.stage];
	return (
		<TaskCard
			task={task}
			now={now}
			icon={<ShieldCheck className="h-4 w-4 text-secondary-500" />}
			title={t("Compliance scan")}
			badge={
				task.name ? (
					<span className="truncate text-xs text-secondary-600 dark:text-secondary-300">
						{task.name}
					</span>
				) : null
			}
		>
			{isOpen(task) && typeof task.percent === "number" && (
				<ProgressBar percent={task.percent} />
			)}
			<p className="text-sm text-secondary-700 dark:text-secondary-200">
				{phase ? t(phase) : task.stage}
				{isOpen(task) &&
					typeof task.percent === "number" &&
					` (${task.percent}%)`}
			</p>
			{task.error && (
				<p className="text-sm text-danger-700 dark:text-danger-300 break-words">
					{task.error}
				</p>
			)}
		</TaskCard>
	);
};

const JobCard = ({ task, now }) => {
	const { t } = useTranslation();
	return (
		<TaskCard
			task={task}
			now={now}
			icon={<Clock className="h-4 w-4 text-secondary-500" />}
			title={formatJobLabel(task.name)}
			badge={
				<>
					<span className="text-xs font-medium text-secondary-600 dark:text-secondary-300">
						{jobStatusText(task)}
					</span>
					{task.attempt > 1 && (
						<span className="text-xs text-secondary-500 dark:text-secondary-400">
							{t("Attempt {{attempt}}", { attempt: task.attempt })}
						</span>
					)}
				</>
			}
		>
			{task.error && (
				<p className="text-sm text-danger-700 dark:text-danger-300 break-words">
					{task.error}
				</p>
			)}
		</TaskCard>
	);
};

const LiveTask = ({ task, now }) => {
	if (task.kind === "job" && isAgentUpdate(task))
		return <AgentUpdateCard task={task} now={now} />;
	if (task.kind === "patch_run") return <PatchRunCard task={task} now={now} />;
	if (task.kind === "compliance_scan")
		return <ComplianceCard task={task} now={now} />;
	return <JobCard task={task} now={now} />;
};

const LiveActivityPanel = ({ tasks = [], isLoading = false, error = null }) => {
	const { t } = useTranslation();
	const running = tasks.filter(isRunning).length;
	// Tick every second while the counters move.
	const now = useTick(tasks.length > 0 ? 1000 : 60000);
	return (
		<section className="card p-4 sm:p-6" aria-live="polite">
			<div className="flex items-center gap-2 mb-3">
				<span className="relative flex h-2.5 w-2.5">
					{running > 0 && (
						<span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-success-400 opacity-75" />
					)}
					<span
						className={`relative inline-flex h-2.5 w-2.5 rounded-full ${
							running > 0 ? "bg-success-500" : "bg-secondary-300"
						}`}
					/>
				</span>
				<h3 className="text-base font-semibold text-secondary-900 dark:text-white">
					{t("Happening now")}
				</h3>
				<span className="text-xs text-secondary-500 dark:text-secondary-300">
					{running > 0
						? t("{{count}} task in progress", { count: running })
						: t("Live")}
				</span>
			</div>
			{error ? (
				<p className="text-sm text-danger-700 dark:text-danger-300">
					{t("Failed to load the live activity")}
				</p>
			) : isLoading ? (
				<div className="flex items-center gap-2 text-sm text-secondary-500 dark:text-secondary-300">
					<Loader2 className="h-4 w-4 animate-spin" />
					{t("Loading...")}
				</div>
			) : tasks.length === 0 ? (
				<p className="text-sm text-secondary-500 dark:text-secondary-300">
					{t(
						"Nothing in progress on this host. Updates, patch runs, scans and other tasks show here step by step while they run.",
					)}
				</p>
			) : (
				<ul className="space-y-3">
					{tasks.map((task) => (
						<LiveTask key={taskKey(task)} task={task} now={now} />
					))}
				</ul>
			)}
		</section>
	);
};

export default LiveActivityPanel;
