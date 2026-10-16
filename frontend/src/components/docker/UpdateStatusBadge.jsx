import { useTranslation } from "react-i18next";
import { tk } from "../../i18n";
import { formatRelativeTime } from "../../utils/api";
import {
	describeCheckReason,
	getSourceDisplayName,
	getUpdateStatusMeta,
} from "../../utils/docker";
import Tooltip from "../ui/Tooltip";

// Container states as the agent reports them. "created" and "restarting"
// already mean something else elsewhere: the container context picks the
// right French form ("created_container").
const CONTAINER_STATES = {
	running: tk("running"),
	exited: tk("exited"),
	created: tk("created"),
	restarting: tk("restarting"),
	paused: tk("paused"),
	dead: tk("dead"),
	removing: tk("removing"),
	unknown: tk("unknown"),
};

export const ContainerState = ({ state }) => {
	const { t } = useTranslation();
	if (!Object.hasOwn(CONTAINER_STATES, state)) return state ?? null;
	return t(CONTAINER_STATES[state], { context: "container" });
};

export const SourceName = ({ source }) => {
	const { t } = useTranslation();
	return t(getSourceDisplayName(source));
};

export const CheckReason = ({ reason }) => {
	const { t } = useTranslation();
	return t(describeCheckReason(reason));
};

export const RecreateNeededBadge = ({ count }) => {
	const { t } = useTranslation();
	return count > 0 ? (
		<span
			className="badge-secondary w-fit whitespace-nowrap"
			title={t(
				"Containers running an older image than the one now pulled for their tag",
			)}
		>
			{t("Recreate needed ({{count}})", { count })}
		</span>
	) : null;
};

const UpdateStatusBadge = ({ status, checkedAt, reason }) => {
	const { t } = useTranslation();
	const meta = getUpdateStatusMeta(status, {
		checkedAt,
		reason,
		formatTime: formatRelativeTime,
	});
	const label = t(meta.label);
	// The up to date tooltip embeds the check time: translate it whole.
	const tooltip =
		meta.status === "up_to_date" && checkedAt
			? t("Checked {{time}}", { time: formatRelativeTime(checkedAt) })
			: t(meta.tooltip);

	if (!tooltip) {
		return (
			<span className={`${meta.className} w-fit whitespace-nowrap`}>
				{label}
			</span>
		);
	}

	return (
		<Tooltip content={tooltip}>
			<button
				type="button"
				className={`${meta.className} w-fit whitespace-nowrap cursor-help focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-1`}
				aria-label={`${label}. ${tooltip}`}
				onClick={(e) => {
					e.preventDefault();
					e.stopPropagation();
				}}
			>
				{label}
			</button>
		</Tooltip>
	);
};

export default UpdateStatusBadge;
