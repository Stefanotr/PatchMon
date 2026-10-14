import { ArrowDown, ArrowUp } from "lucide-react";
import { useTranslation } from "react-i18next";
import i18n, { tk } from "../../i18n";

// Direction is conveyed by the arrow icon (up = inbound, down = outbound),
// not by colour. Pills use a single neutral palette to reduce table noise.
const NEUTRAL_PILL_CLASSES =
	"bg-secondary-100 text-secondary-700 dark:bg-secondary-700 dark:text-secondary-100";

const REPORT_LABELS = {
	ping: tk("PING"),
	full: tk("FULL"),
	partial: tk("PARTIAL"),
	docker: tk("DOCKER"),
	compliance: tk("COMPLIANCE"),
};

// Jobs (server -> agent, outbound). Single amber/orange palette so the
// "direction" reads instantly even before scanning the arrow.
const JOB_LABELS = {
	report_now: tk("Report Now"),
	"report-now": tk("Report Now"),
	refresh_integration_status: tk("Refresh Integration Status"),
	"refresh-integration-status": tk("Refresh Integration Status"),
	docker_inventory_refresh: tk("Docker Inventory Refresh"),
	"docker-inventory-refresh": tk("Docker Inventory Refresh"),
	update_agent: tk("Agent Update"),
	"update-agent": tk("Agent Update"),
	run_scan: tk("Compliance Scan"),
	"run-scan": tk("Compliance Scan"),
	install_compliance_tools: tk("Install Compliance Scanner"),
	"install-compliance-tools": tk("Install Compliance Scanner"),
	ssg_upgrade: tk("SSG Content Upgrade"),
	"ssg-upgrade": tk("SSG Content Upgrade"),
	run_patch: tk("Run Patch"),
	"run-patch": tk("Run Patch"),
	reboot: tk("Restart"),
	reboot_cancel: tk("Restart Cancel"),
	scheduled_reports_dispatch: tk("Scheduled Reports Dispatch"),
	"scheduled-reports-dispatch": tk("Scheduled Reports Dispatch"),
	scheduled_report_run: tk("Scheduled Report Run"),
	"scheduled-report-run": tk("Scheduled Report Run"),
	"update-threshold-monitor": tk("Update Threshold Monitor"),
	"host-status-monitor": tk("Host Status Monitor"),
	"metrics-send": tk("Metrics Send"),
	"agent-reports-cleanup": tk("Agent Reports Cleanup"),
	"patch-run-cleanup": tk("Patch Run Cleanup"),
	"compliance-scan-cleanup": tk("Compliance Scan Cleanup"),
	"ssg-update-check": tk("SSG Update Check"),
	"version-update-check": tk("Version Update Check"),
	"system-statistics": tk("System Statistics"),
	"docker-inventory-cleanup": tk("Docker Inventory Cleanup"),
	"orphaned-package-cleanup": tk("Orphaned Package Cleanup"),
	"orphaned-repo-cleanup": tk("Orphaned Repo Cleanup"),
	"session-cleanup": tk("Session Cleanup"),
};

// formatJobLabel is the display name of a queue job (translated).
export const formatJobLabel = (jobName) => {
	if (!jobName) return i18n.t("Job");
	const lower = jobName.toString().toLowerCase();
	if (JOB_LABELS[lower]) return i18n.t(JOB_LABELS[lower]);
	return jobName
		.toString()
		.replace(/[_-]+/g, " ")
		.replace(/\s+/g, " ")
		.trim()
		.split(" ")
		.map((word) =>
			word.length === 0 ? word : word[0].toUpperCase() + word.slice(1),
		)
		.join(" ");
};

const baseChip =
	"inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium whitespace-nowrap";

const ActivityTypeBadge = ({ kind, type, jobName }) => {
	const { t } = useTranslation();
	if (kind === "job") {
		const label = formatJobLabel(jobName || type);
		return (
			<span
				className={`${baseChip} ${NEUTRAL_PILL_CLASSES}`}
				title={t("Outbound: server queued {{label}} for the agent", { label })}
			>
				<ArrowDown className="h-3 w-3 flex-shrink-0" />
				<span>{label}</span>
			</span>
		);
	}

	const reportKey = (type || "").toString().toLowerCase();
	const label = REPORT_LABELS[reportKey]
		? t(REPORT_LABELS[reportKey])
		: type
			? type.toString().toUpperCase()
			: t("REPORT");

	return (
		<span
			className={`${baseChip} ${NEUTRAL_PILL_CLASSES}`}
			title={t("Inbound: agent sent a {{type}} report to the server", {
				type: label.toLowerCase(),
			})}
		>
			<ArrowUp className="h-3 w-3 flex-shrink-0" />
			<span>{label}</span>
		</span>
	);
};

export default ActivityTypeBadge;
