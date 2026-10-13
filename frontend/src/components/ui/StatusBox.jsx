// Counter boxes of the dashboard status cards (patch runs, approvals, alerts,
// compliance). The layout follows the width of the card, see .status-boxes
// in index.css: a narrow card lists them one per row (icon, label, value), a
// wider one shows them as a 2x2 grid of tiles. Either way a label longer than
// expected, as translations often are, wraps between words instead of
// spilling out of its box.

// STATUS_BOX_CLASS is the class of the clickable box (a Link or a button).
export const STATUS_BOX_CLASS = "card status-box text-left w-full min-w-0";

export const StatusBoxGrid = ({ children }) => (
	<div className="status-boxes flex-1 min-h-0">
		<div className="status-boxes-grid">{children}</div>
	</div>
);

const StatusBox = ({ Icon, iconClass, label, value }) => (
	<>
		<Icon className={`status-box-icon h-5 w-5 ${iconClass}`} />
		<span className="status-box-value text-xl font-semibold text-secondary-900 dark:text-white truncate">
			{value}
		</span>
		<span className="status-box-label text-sm leading-snug text-secondary-500 dark:text-white break-words hyphens-auto">
			{label}
		</span>
	</>
);

export default StatusBox;
