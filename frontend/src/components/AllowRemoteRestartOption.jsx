import { useTranslation } from "react-i18next";

// AllowRemoteRestartOption adds --allow-remote-restart (-AllowRemoteRestart
// on Windows) to an install command. Off by default: the host's
// administrator, who runs the command, decides.
const AllowRemoteRestartOption = ({ checked, onChange, windows = false }) => {
	const { t } = useTranslation();
	return (
		<div className="mb-3">
			<label className="flex items-center gap-2 text-xs md:text-sm cursor-pointer">
				<input
					type="checkbox"
					checked={checked}
					onChange={(e) => onChange(e.target.checked)}
					className="rounded border-secondary-300 dark:border-secondary-600 text-primary-600 focus:ring-primary-500 dark:focus:ring-primary-400 dark:bg-secondary-700"
				/>
				<span className="text-secondary-800 dark:text-secondary-200">
					{t("Allow remote restarts")}
				</span>
			</label>
			<p className="text-xs text-secondary-600 dark:text-secondary-300 mt-1">
				{t(
					"Adds {{option}} to the command: the agent's config.yml gets remote-restart-enabled: true, so PatchMon can restart the host (Restart button, patch runs). The host keeps the decision: the server cannot change it afterwards.",
					{
						option: windows ? "-AllowRemoteRestart" : "--allow-remote-restart",
					},
				)}
			</p>
		</div>
	);
};

export default AllowRemoteRestartOption;
