import {
	AlertTriangle,
	ShieldAlert,
	ShieldCheck,
	ShieldOff,
} from "lucide-react";
import { Fragment } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import StatusBox, { STATUS_BOX_CLASS, StatusBoxGrid } from "../../ui/StatusBox";

const HostComplianceStatusBar = ({ data, onTabChange }) => {
	const { t } = useTranslation();
	const summary = data?.summary || {};
	const compliant = summary.hosts_compliant ?? 0;
	const warning = summary.hosts_warning ?? 0;
	const critical = summary.hosts_critical ?? 0;
	const unscanned = summary.unscanned ?? 0;

	const boxes = [
		{
			label: t("Compliant"),
			value: compliant,
			Icon: ShieldCheck,
			icon_class: "text-green-600",
			to: "/compliance",
		},
		{
			label: t("Warning"),
			value: warning,
			Icon: AlertTriangle,
			icon_class: "text-yellow-600",
			to: "/compliance",
		},
		{
			label: t("Critical"),
			value: critical,
			Icon: ShieldAlert,
			icon_class: "text-red-600",
			to: "/compliance",
		},
		{
			label: t("Never scanned"),
			value: unscanned,
			Icon: ShieldOff,
			icon_class: "text-secondary-600 dark:text-white",
			to: "/compliance",
		},
	];

	const box_class = `${STATUS_BOX_CLASS} hover:bg-secondary-50 dark:hover:bg-secondary-700/50 transition-colors`;

	return (
		<div className="card p-4 sm:p-6 w-full h-full flex flex-col">
			<h3 className="text-lg font-medium text-secondary-900 dark:text-white mb-4 flex-shrink-0">
				{t("Host Compliance Status")}
			</h3>
			<StatusBoxGrid>
				{boxes.map((box) => {
					const content = (
						<StatusBox
							Icon={box.Icon}
							iconClass={box.icon_class}
							label={box.label}
							value={box.value}
						/>
					);
					return (
						<Fragment key={box.label}>
							{onTabChange ? (
								<button
									type="button"
									onClick={() => onTabChange("hosts")}
									className={box_class}
								>
									{content}
								</button>
							) : (
								<Link
									to={box.to}
									state={{ complianceTab: "hosts" }}
									className={box_class}
								>
									{content}
								</Link>
							)}
						</Fragment>
					);
				})}
			</StatusBoxGrid>
		</div>
	);
};

export default HostComplianceStatusBar;
