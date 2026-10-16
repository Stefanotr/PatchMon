import { CheckCircle, Clock, ListChecks, XCircle } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import StatusBox, { STATUS_BOX_CLASS, StatusBoxGrid } from "../../ui/StatusBox";

const PatchRunStatusBoxes = ({ data }) => {
	const { t } = useTranslation();
	const summary = data?.summary || {};
	const total = summary.total_runs ?? 0;
	const active =
		(summary.queued ?? 0) +
		(summary.pending_validation ?? 0) +
		(summary.running ?? 0);
	const completed = summary.completed ?? 0;
	const failed = summary.failed ?? 0;

	const boxes = [
		{
			label: t("Total Runs"),
			value: total,
			Icon: ListChecks,
			icon_class: "text-primary-600",
			to: "/patching?tab=runs",
		},
		{
			label: t("Active", { context: "runs" }),
			value: active,
			Icon: Clock,
			icon_class: "text-blue-600",
			to: "/patching?tab=runs&status=active",
		},
		{
			label: t("Completed"),
			value: completed,
			Icon: CheckCircle,
			icon_class: "text-green-600",
			to: "/patching?tab=runs&status=completed",
		},
		{
			label: t("Failed"),
			value: failed,
			Icon: XCircle,
			icon_class: "text-red-600",
			to: "/patching?tab=runs&status=failed",
		},
	];

	return (
		<div className="card p-4 sm:p-6 w-full h-full flex flex-col">
			<h3 className="text-lg font-medium text-secondary-900 dark:text-white mb-4 flex-shrink-0">
				{t("Patch Run Status")}
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

export default PatchRunStatusBoxes;
