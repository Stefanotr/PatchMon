import { useTranslation } from "react-i18next";
import { tk } from "../i18n";
import { QueuedStatusBadge } from "./QueuedStatusBadge";

const STATUS_MAP = {
	queued: {
		class:
			"bg-secondary-100 text-secondary-800 dark:bg-secondary-700 dark:text-secondary-200",
		label: tk("Queued"),
	},
	pending_validation: {
		class: "bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200",
		label: tk("Pending validation"),
	},
	pending_approval: {
		class: "bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200",
		label: tk("Pending approval"),
	},
	validated: {
		class:
			"bg-emerald-100 text-emerald-800 dark:bg-emerald-900 dark:text-emerald-200",
		label: tk("Validated"),
	},
	scheduled: {
		class:
			"bg-secondary-100 text-secondary-800 dark:bg-secondary-700 dark:text-secondary-200",
		label: tk("Scheduled"),
	},
	running: {
		class: "bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200",
		label: tk("Running"),
	},
	completed: {
		class: "bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200",
		label: tk("Completed"),
	},
	failed: {
		class: "bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200",
		label: tk("Failed"),
	},
	approved: {
		class:
			"bg-emerald-100 text-emerald-800 dark:bg-emerald-900 dark:text-emerald-200",
		label: tk("Approved"),
	},
	cancelled: {
		class:
			"bg-secondary-100 text-secondary-600 dark:bg-secondary-600 dark:text-secondary-200",
		label: tk("Cancelled"),
	},
	timed_out: {
		class: "bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200",
		label: tk("Timed out"),
	},
	agent_disconnected: {
		class: "bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200",
		label: tk("Agent disconnected"),
	},
};

/**
 * Status badge for patch runs. For queued or scheduled runs with scheduled_at, shows a countdown timer.
 */
export function PatchRunStatusBadge({ run }) {
	const { t } = useTranslation();
	const status = run?.status ?? "";
	const scheduledAt = run?.scheduled_at ?? null;

	if ((status === "queued" || status === "scheduled") && scheduledAt) {
		return (
			<QueuedStatusBadge
				scheduledAt={scheduledAt}
				label={t(STATUS_MAP[status].label)}
			/>
		);
	}

	const known = STATUS_MAP[status];
	const s = known || {
		class: "bg-secondary-100 text-secondary-800",
		label: status,
	};
	return (
		<span
			className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${s.class}`}
		>
			{known ? t(s.label) : s.label}
		</span>
	);
}
