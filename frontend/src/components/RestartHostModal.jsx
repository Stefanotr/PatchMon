import { useMutation } from "@tanstack/react-query";
import { Power, X } from "lucide-react";
import { useState } from "react";
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

/**
 * Restart a host now or later, or cancel a scheduled restart. The server
 * waits for the agent's answer: 200 when it did it, 409 when it refused
 * (a patch run in progress, a restart already scheduled), 202 when it did
 * not answer in time (an older agent ignores the command).
 */
export default function RestartHostModal({
	hostId,
	hostName,
	isOpen,
	onClose,
}) {
	const toast = useToast();
	const [choice, setChoice] = useState({
		...DEFAULT_RESTART_CHOICE,
		mode: "always",
	});

	const report = (res, fallback) => {
		const answer = describeRestartAnswer(res, fallback);
		toast[answer.level](answer.text);
		if (answer.level === "success") onClose();
	};

	const restart = useMutation({
		mutationFn: () =>
			adminHostsAPI.rebootHost(
				hostId,
				buildRebootRequest(choice, { mode: "always" }),
			),
		onSuccess: (res) => report(res, "Restart scheduled"),
		onError: (err) => toast.error(errorText(err) || "Restart failed"),
	});

	const cancel = useMutation({
		mutationFn: () => adminHostsAPI.cancelHostReboot(hostId),
		onSuccess: (res) => report(res, "Scheduled restart cancelled"),
		onError: (err) => toast.error(errorText(err) || "Cancel failed"),
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
							Restart host
						</h3>
						<p className="text-sm text-secondary-600 dark:text-secondary-300 break-words">
							{hostName}
						</p>
					</div>
					<button
						type="button"
						onClick={onClose}
						className="text-secondary-400 hover:text-secondary-600"
						aria-label="Close"
					>
						<X className="h-5 w-5" />
					</button>
				</div>

				<RestartOptions value={choice} onChange={setChoice} showMode={false} />

				<p className="mt-4 text-xs text-secondary-500 dark:text-secondary-400">
					Signed-in users are warned. Applications are closed when the countdown
					ends, unsaved work included. The agent refuses the restart while a
					patch run is in progress on this host, or when a restart is already
					scheduled: cancel that one first to pick another time.
				</p>

				<div className="mt-6 flex flex-wrap justify-between gap-3">
					<button
						type="button"
						onClick={() => cancel.mutate()}
						className="btn-outline text-sm"
						disabled={busy}
						title="Cancel a restart scheduled on this host"
					>
						{cancel.isPending ? "Cancelling..." : "Cancel scheduled restart"}
					</button>
					<button
						type="button"
						onClick={() => restart.mutate()}
						className="btn-danger text-sm"
						disabled={busy || invalid}
					>
						{restart.isPending ? "Sending..." : "Schedule restart"}
					</button>
				</div>
			</div>
		</div>
	);

	return <ModalPortal>{modal}</ModalPortal>;
}
