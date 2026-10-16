import { tk } from "../i18n";
import { hostIsFreeBSD, hostIsWindows } from "./hostPlatform";

// The command an administrator runs on a host to allow, or forbid again, the
// restarts PatchMon asks for. The server cannot change this switch: it only
// shows the command. Each one sets remote-restart-enabled in the agent's
// config.yml, then restarts the agent service, which announces the new
// setting when it reconnects. The agent applies the switch at once anyway.

// Absolute paths: sudo's secure_path leaves /usr/local/bin out on RHEL.
const AGENT = "/usr/local/bin/patchmon-agent";
const AGENT_WINDOWS = "C:\\Program Files\\PatchMon\\patchmon-agent.exe";

export const REMOTE_RESTART_PLATFORMS = [
	{
		id: "linux",
		label: tk("Linux (systemd)"),
		runAs: tk("In a terminal, with sudo."),
	},
	{ id: "alpine", label: tk("Alpine (OpenRC)"), runAs: tk("As root.") },
	{ id: "freebsd", label: "FreeBSD", runAs: tk("As root.") },
	{
		id: "windows",
		label: "Windows",
		runAs: tk("In PowerShell run as administrator."),
	},
];

// remoteRestartPlatform picks the command a host needs from what it reports.
export const remoteRestartPlatform = (host) => {
	if (!host) return "linux";
	if (hostIsWindows(host)) return "windows";
	if (hostIsFreeBSD(host)) return "freebsd";
	const os = (host.os_type || "").toLowerCase();
	if (
		(host.package_manager || "").toLowerCase() === "apk" ||
		os.includes("alpine")
	) {
		return "alpine";
	}
	return "linux";
};

// remoteRestartCommand returns the command for a platform; on=false forbids
// remote restarts again.
export const remoteRestartCommand = (platform, on = true) => {
	const state = on ? "on" : "off";
	switch (platform) {
		case "windows":
			return `& "${AGENT_WINDOWS}" config remote-restart ${state}; if ($LASTEXITCODE -eq 0) { Restart-Service PatchMonAgent }`;
		case "freebsd":
			return `${AGENT} config remote-restart ${state} && service patchmon_agent restart`;
		case "alpine":
			return `${AGENT} config remote-restart ${state} && rc-service patchmon-agent restart`;
		default:
			return `sudo ${AGENT} config remote-restart ${state} && sudo systemctl restart patchmon-agent`;
	}
};
