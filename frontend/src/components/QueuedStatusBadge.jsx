import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import i18n from "../i18n";
import { formatDate } from "../utils/api";

/**
 * Formats remaining time until a target date as human-readable text.
 * @param {Date} target - Target date
 * @returns {string} e.g. "in 5 min", "in 2 h", "in 30 s", "soon"
 */
function formatTimeUntil(target) {
	const now = Date.now();
	const ms = target - now;
	if (ms <= 0) return i18n.t("soon");

	const sec = Math.floor(ms / 1000);
	const min = Math.floor(sec / 60);
	const h = Math.floor(min / 60);

	if (sec < 60) return i18n.t("in {{seconds}} s", { seconds: sec });
	if (min < 60) return i18n.t("in {{minutes}} min", { minutes: min });
	if (h < 24) return i18n.t("in {{hours}} h", { hours: h });
	return i18n.t("in {{days}} d", { days: Math.floor(h / 24) });
}

/**
 * Badge for queued or scheduled patch runs. Shows the label and a countdown timer when scheduled_at is set.
 */
export function QueuedStatusBadge({ scheduledAt, label, className = "" }) {
	const { t } = useTranslation();
	const text = label ?? t("Queued");
	const [countdown, setCountdown] = useState(null);

	useEffect(() => {
		if (!scheduledAt) {
			setCountdown(null);
			return;
		}
		const target = new Date(scheduledAt).getTime();
		const tick = () => {
			const now = Date.now();
			if (target <= now) {
				setCountdown(i18n.t("soon"));
				return;
			}
			setCountdown(formatTimeUntil(new Date(target)));
		};
		tick();
		const id = setInterval(tick, 1000);
		return () => clearInterval(id);
	}, [scheduledAt]);

	const baseClass =
		"inline-flex items-center gap-1.5 px-2 py-0.5 rounded text-xs font-medium bg-secondary-100 text-secondary-800 dark:bg-secondary-700 dark:text-secondary-200";

	return (
		<span
			className={`${baseClass} ${className}`}
			title={
				scheduledAt
					? t("Runs at {{date}}", { date: formatDate(scheduledAt) })
					: text
			}
		>
			{text}
			{scheduledAt && countdown && (
				<span className="opacity-90 font-normal">({countdown})</span>
			)}
		</span>
	);
}
