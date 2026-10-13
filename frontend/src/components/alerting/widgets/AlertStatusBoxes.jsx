import {
	AlertOctagon,
	AlertTriangle,
	CheckCircle2,
	ShieldAlert,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import StatusBox, { STATUS_BOX_CLASS, StatusBoxGrid } from "../../ui/StatusBox";

const AlertStatusBoxes = ({ stats, alerts }) => {
	const { t } = useTranslation();
	const active =
		(stats?.critical ?? 0) +
		(stats?.error ?? 0) +
		(stats?.warning ?? 0) +
		(stats?.informational ?? 0);
	const critical = stats?.critical ?? 0;
	const warnings = stats?.warning ?? 0;

	// Count alerts resolved today
	const today = new Date();
	today.setHours(0, 0, 0, 0);
	const resolved_today = (alerts || []).filter((a) => {
		if (!a.resolved_at) return false;
		const resolved = new Date(a.resolved_at);
		return resolved >= today;
	}).length;

	const boxes = [
		{
			label: t("Active Alerts"),
			value: active,
			Icon: AlertTriangle,
			icon_class: "text-red-500",
			to: "/reporting?tab=alerts",
		},
		{
			label: t("Critical"),
			value: critical,
			Icon: AlertOctagon,
			icon_class: "text-red-600",
			to: "/reporting?tab=alerts&severity=critical",
		},
		{
			label: t("Warnings"),
			value: warnings,
			Icon: ShieldAlert,
			icon_class: "text-amber-500",
			to: "/reporting?tab=alerts&severity=warning",
		},
		{
			label: t("Resolved Today"),
			value: resolved_today,
			Icon: CheckCircle2,
			icon_class: "text-green-500",
			to: "/reporting?tab=alerts&status=resolved",
		},
	];

	return (
		<div className="card p-4 sm:p-6 w-full h-full flex flex-col">
			<h3 className="text-lg font-medium text-secondary-900 dark:text-white mb-4 flex-shrink-0">
				{t("Alert Summary")}
			</h3>
			<StatusBoxGrid>
				{boxes.map((box) => (
					<Link
						key={box.label}
						to={box.to}
						className={`${STATUS_BOX_CLASS} hover:shadow-card-hover dark:hover:shadow-card-hover-dark transition-shadow duration-200`}
					>
						<StatusBox
							Icon={box.Icon}
							iconClass={box.icon_class}
							label={box.label}
							value={box.value}
						/>
					</Link>
				))}
			</StatusBoxGrid>
		</div>
	);
};

export default AlertStatusBoxes;
