import { useQueryClient } from "@tanstack/react-query";
import { Check, Copy, Power, X } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
	REMOTE_RESTART_PLATFORMS,
	remoteRestartCommand,
	remoteRestartPlatform,
} from "../utils/remoteRestartCommand";
import ModalPortal from "./ui/ModalPortal";

// RemoteRestartHelpModal shows the command that allows remote restarts on a
// host. PatchMon cannot turn them on itself: the host's administrator runs
// it. host is optional; it picks the platform shown first.
export default function RemoteRestartHelpModal({ isOpen, onClose, host }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [platform, setPlatform] = useState(() => remoteRestartPlatform(host));
	const [copied, setCopied] = useState(false);

	if (!isOpen) return null;

	const command = remoteRestartCommand(platform, true);
	const current = REMOTE_RESTART_PLATFORMS.find((p) => p.id === platform);
	const hostName = host?.friendly_name || host?.hostname;

	const close = () => {
		// The agent announces the new setting when it reconnects.
		queryClient.invalidateQueries({ queryKey: ["remote-restart"] });
		onClose();
	};

	const copy = async () => {
		try {
			await navigator.clipboard.writeText(command);
			setCopied(true);
			setTimeout(() => setCopied(false), 2000);
		} catch {
			setCopied(false);
		}
	};

	const modal = (
		<div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-[130] p-4">
			<div
				className="bg-white dark:bg-secondary-800 rounded-lg p-6 w-full max-w-xl max-h-[90vh] overflow-y-auto"
				role="dialog"
				aria-modal="true"
				aria-labelledby="remote-restart-help-title"
			>
				<div className="flex items-start gap-3 mb-4">
					<div className="w-10 h-10 bg-primary-100 dark:bg-primary-900 rounded-full flex items-center justify-center shrink-0">
						<Power className="h-5 w-5 text-primary-600 dark:text-primary-400" />
					</div>
					<div className="flex-1 min-w-0">
						<h3
							id="remote-restart-help-title"
							className="text-lg font-semibold text-secondary-900 dark:text-white"
						>
							{t("Allow remote restarts")}
						</h3>
						{hostName && (
							<p className="text-sm text-secondary-600 dark:text-secondary-300 break-words">
								{hostName}
							</p>
						)}
					</div>
					<button
						type="button"
						onClick={close}
						className="text-secondary-400 hover:text-secondary-600"
						aria-label={t("Close")}
					>
						<X className="h-5 w-5" />
					</button>
				</div>

				<p className="text-sm text-secondary-700 dark:text-secondary-200">
					{t(
						"PatchMon cannot turn remote restarts on by itself: whoever administers the host decides. Run this command on the host:",
					)}
				</p>

				<div
					className="mt-3 flex flex-wrap gap-1"
					role="tablist"
					aria-label={t("Operating system")}
				>
					{REMOTE_RESTART_PLATFORMS.map((p) => (
						<button
							key={p.id}
							type="button"
							role="tab"
							aria-selected={platform === p.id}
							onClick={() => {
								setPlatform(p.id);
								setCopied(false);
							}}
							className={`px-3 py-1 rounded-md text-xs font-medium border ${
								platform === p.id
									? "bg-primary-600 border-primary-600 text-white"
									: "border-secondary-300 dark:border-secondary-600 text-secondary-700 dark:text-secondary-200 hover:bg-secondary-100 dark:hover:bg-secondary-700"
							}`}
						>
							{t(p.label)}
						</button>
					))}
				</div>

				<p className="mt-3 text-xs text-secondary-500 dark:text-secondary-400">
					{t(current.runAs)}
				</p>
				<div className="mt-1 flex items-stretch gap-2">
					<pre className="flex-1 min-w-0 overflow-x-auto rounded-md bg-secondary-900 text-secondary-100 text-xs p-3 font-mono whitespace-pre-wrap [overflow-wrap:anywhere]">
						{command}
					</pre>
					<button
						type="button"
						onClick={copy}
						className="btn-outline text-sm flex items-center gap-1 shrink-0 self-start"
						title={t("Copy the command")}
					>
						{copied ? (
							<Check className="h-4 w-4 text-success-600" />
						) : (
							<Copy className="h-4 w-4" />
						)}
						<span className="hidden sm:inline">
							{copied ? t("Copied") : t("Copy")}
						</span>
					</button>
				</div>

				<ul className="mt-4 space-y-1.5 text-xs text-secondary-600 dark:text-secondary-300 list-disc pl-4">
					<li>
						{t(
							"It sets remote-restart-enabled: true in the agent's config.yml, then restarts the agent service so that PatchMon sees the change.",
						)}
					</li>
					<li>
						{t(
							"Do not run it while a patch run is in progress on this host: restarting the agent service would cut the run off.",
						)}
					</li>
					<li>
						{t(
							"To forbid remote restarts again, run the same command with off instead of on.",
						)}
					</li>
					<li>
						{t(
							"If the command only prints its help, the agent is too old: update it from PatchMon first.",
						)}
					</li>
				</ul>

				<div className="mt-6 flex justify-end">
					<button type="button" onClick={close} className="btn-primary text-sm">
						{t("Close")}
					</button>
				</div>
			</div>
		</div>
	);

	return <ModalPortal>{modal}</ModalPortal>;
}
