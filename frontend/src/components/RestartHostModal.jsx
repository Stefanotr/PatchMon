import { useMutation, useQueryClient } from "@tanstack/react-query";
import { History, Info, Power, RotateCcw, X } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { useToast } from "../contexts/ToastContext";
import { adminHostsAPI } from "../utils/api";
import {
	buildRebootRequest,
	DEFAULT_RESTART_CHOICE,
	describeRestartAnswer,
	restartChoiceError,
} from "../utils/patchingApi";
import RestartOptions from "./RestartOptions";
import ModalPortal from "./ui/ModalPortal";

const errorText = (err) => err?.response?.data?.error || err?.message;

// Restart commands and the restarts patch runs schedule are recorded in the
// host's Agent Activity under these job names.
export const restartHistoryPath = (hostId) =>
	`/hosts/${encodeURIComponent(hostId)}?tab=activity&type=reboot,reboot_cancel&since=30d`;

/**
 * Restart a host now or later, or cancel a scheduled restart. The server
 * waits for the agent's answer: 200 when it did it, 409 when it refused
 * (a patch run in progress, a restart already scheduled), 202 when it did
 * not answer in time (an older agent ignores the command).
 *
 * needsReboot is the host's last reported state: true shows why a restart is
 * pending, false says a restart is optional, undefined shows neither.
 */
export default function RestartHostModal({
	hostId,
	hostName,
	isOpen,
	onClose,
	needsReboot,
	rebootReason,
}) {
	const { t } = useTranslation();
	const toast = useToast();
	const queryClient = useQueryClient();
	// Every outcome is recorded in the host's Agent Activity.
	const refreshHistory = () =>
		queryClient.invalidateQueries({ queryKey: ["host-activity", hostId] });
	const [choice, setChoice] = useState({
		...DEFAULT_RESTART_CHOICE,
		mode: "always",
	});

	const report = (res, fallback) => {
		const answer = describeRestartAnswer(res, fallback);
		toast[answer.level](answer.text);
		refreshHistory();
		if (answer.level === "success") onClose();
	};

	const restart = useMutation({
		mutationFn: () =>
			adminHostsAPI.rebootHost(
				hostId,
				buildRebootRequest(choice, { mode: "always" }),
			),
		onSuccess: (res) => report(res, t("Restart scheduled")),
		onError: (err) => {
			toast.error(errorText(err) || t("Restart failed"));
			refreshHistory();
		},
	});

	const cancel = useMutation({
		mutationFn: () => adminHostsAPI.cancelHostReboot(hostId),
		onSuccess: (res) => report(res, t("Scheduled restart cancelled")),
		onError: (err) => {
			toast.error(errorText(err) || t("Cancel failed"));
			refreshHistory();
		},
	});

	if (!isOpen || !hostId) return null;
	const busy = restart.isPending || cancel.isPending;
	const invalid = Boolean(restartChoiceError(choice));

	const modal = (
		<div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-[120]">
			<div className="bg-white dark:bg-secondary-800 rounded-lg p-6 w-full max-w-md">
				<div className="flex items-start gap-3 mb-4">
					<div className="w-10 h-10 bg-warning-100 dark:bg-warning-900 rounded-full flex items-center justify-center shrink-0">
						<Power className="h-5 w-5 text-warning-600 dark:text-warning-400" />
					</div>
					<div className="flex-1 min-w-0">
						<h3 className="text-lg font-semibold text-secondary-900 dark:text-white">
							{t("Restart host")}
						</h3>
						<p className="text-sm text-secondary-600 dark:text-secondary-300 break-words">
							{hostName}
						</p>
					</div>
					<button
						type="button"
						onClick={onClose}
						className="text-secondary-400 hover:text-secondary-600"
						aria-label={t("Close")}
					>
						<X className="h-5 w-5" />
					</button>
				</div>

				{needsReboot === true && (
					<div className="mb-4 flex items-start gap-2 rounded-md border border-warning-200 bg-warning-50 p-3 text-sm text-warning-800 dark:border-warning-800 dark:bg-warning-900/30 dark:text-warning-200">
						<RotateCcw className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
						<span>
							{rebootReason
								? t("A restart is pending on this host: {{reason}}", {
										reason: rebootReason,
									})
								: t("A restart is pending on this host.")}
						</span>
					</div>
				)}
				{needsReboot === false && (
					<div className="mb-4 flex items-start gap-2 rounded-md border border-secondary-200 bg-secondary-50 p-3 text-sm text-secondary-700 dark:border-secondary-600 dark:bg-secondary-700/50 dark:text-secondary-200">
						<Info className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
						<span>
							{t(
								"This host does not report a pending restart. Restarting it is optional.",
							)}
						</span>
					</div>
				)}

				<RestartOptions value={choice} onChange={setChoice} showMode={false} />

				<p className="mt-4 text-xs text-secondary-500 dark:text-secondary-400">
					{t(
						"Signed-in users are warned. Applications are closed when the countdown ends, unsaved work included. The agent refuses the restart while a patch run is in progress on this host, or when a restart is already scheduled: cancel that one first to pick another time.",
					)}
				</p>
				<Link
					to={restartHistoryPath(hostId)}
					onClick={onClose}
					className="mt-2 inline-flex items-center gap-1 text-xs text-primary-600 hover:text-primary-800 dark:text-primary-400 dark:hover:text-primary-300"
				>
					<History className="h-3 w-3" aria-hidden="true" />
					{t("Restart history")}
				</Link>

				<div className="mt-6 flex flex-wrap justify-between gap-3">
					<button
						type="button"
						onClick={() => cancel.mutate()}
						className="btn-outline text-sm"
						disabled={busy}
						title={t("Cancel a restart scheduled on this host")}
					>
						{cancel.isPending
							? t("Cancelling...")
							: t("Cancel scheduled restart")}
					</button>
					<button
						type="button"
						onClick={() => restart.mutate()}
						className="btn-danger text-sm"
						disabled={busy || invalid}
					>
						{restart.isPending ? t("Sending...") : t("Schedule restart")}
					</button>
				</div>
			</div>
		</div>
	);

	return <ModalPortal>{modal}</ModalPortal>;
}
