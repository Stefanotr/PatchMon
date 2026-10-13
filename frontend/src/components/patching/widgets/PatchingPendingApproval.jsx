import { AlertTriangle, CheckSquare, Clock, Play } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import StatusBox, { STATUS_BOX_CLASS, StatusBoxGrid } from "../../ui/StatusBox";

const PatchingPendingApproval = ({ data }) => {
	const { t } = useTranslation();
	const summary = data?.summary || {};
	const pending_validation = summary.pending_validation ?? 0;
	const pending_approval = summary.pending_approval ?? 0;
	const validated = summary.validated ?? 0;
	const running = summary.running ?? 0;

	// "Needs action" groups every run that's waiting for a human decision:
	// a validated dry-run that needs sign-off, an approval submission that
	// hasn't been acted on, and a stalled dry-run that may need retrying.
	const needs_action = pending_validation + pending_approval + validated;

	const boxes = [
		{
			label: t("Pending Validation"),
			value: pending_validation,
			Icon: Clock,
			icon_class: "text-amber-500",
			highlight: pending_validation > 0,
			to: "/patching?tab=runs&status=pending_validation",
		},
		{
			label: t("Pending Approval"),
			value: pending_approval,
			Icon: CheckSquare,
			icon_class: "text-amber-500",
			highlight: pending_approval > 0,
			to: "/patching?tab=runs&status=pending_approval",
		},
		{
			label: t("Awaiting Approval"),
			value: validated,
			Icon: AlertTriangle,
			icon_class: "text-amber-600",
			highlight: validated > 0,
			to: "/patching?tab=runs&status=validated",
		},
		{
			label: t("Running"),
			value: running,
			Icon: Play,
			icon_class: "text-green-500",
			highlight: false,
			to: "/patching?tab=runs&status=running",
		},
	];

	return (
		<div className="card p-4 sm:p-6 w-full h-full flex flex-col">
			<div className="flex flex-wrap items-center justify-between gap-2 mb-4 flex-shrink-0">
				<h3 className="text-lg font-medium text-secondary-900 dark:text-white min-w-0">
					{t("Pending Approval")}
				</h3>
				{needs_action > 0 && (
					<span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium whitespace-nowrap bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300">
						<AlertTriangle className="h-3 w-3" />
						{t("{{count}} need action", { count: needs_action })}
					</span>
				)}
			</div>
			<StatusBoxGrid>
				{boxes.map((box) => (
					<Link
						key={box.label}
						to={box.to}
						className={`${STATUS_BOX_CLASS} hover:shadow-card-hover dark:hover:shadow-card-hover-dark transition-shadow duration-200 ${
							box.highlight
								? "border border-amber-400 dark:border-amber-600 bg-amber-50 dark:bg-amber-900/10"
								: ""
						}`}
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
			{needs_action > 0 && (
				<Link
					to="/patching?tab=runs&status=validated"
					className="mt-3 flex items-center justify-center gap-1.5 w-full py-2 text-sm font-medium text-amber-600 hover:text-amber-700 dark:text-amber-400 dark:hover:text-amber-300 hover:underline flex-shrink-0"
				>
					{t("Review {{count}} run needing action", { count: needs_action })}
				</Link>
			)}
		</div>
	);
};

export default PatchingPendingApproval;
