// Arguments of the agent install scripts that the command run on the host
// carries, so that the host's administrator sees and chooses them; the
// server cannot set them. --allow-remote-restart (-AllowRemoteRestart on
// Windows) writes remote-restart-enabled: true in the agent's config.yml.

// shellInstallArgs lists the arguments of the Linux and FreeBSD scripts
// (install and enrollment).
export const shellInstallArgs = ({
	force = false,
	allowRemoteRestart = false,
} = {}) => {
	const args = [];
	if (force) args.push("--force");
	if (allowRemoteRestart) args.push("--allow-remote-restart");
	return args;
};

// withScriptArgs appends the arguments to the shell a script is piped to:
// "sudo sh -s -- --force", or the shell alone without arguments.
export const withScriptArgs = (shell, options) => {
	const args = shellInstallArgs(options);
	return args.length ? `${shell} -s -- ${args.join(" ")}` : shell;
};

// windowsScriptCall runs the downloaded Windows install script.
export const windowsScriptCall = ({ allowRemoteRestart = false } = {}) =>
	`& "$env:TEMP\\patchmon-install.ps1"${allowRemoteRestart ? " -AllowRemoteRestart" : ""}`;
