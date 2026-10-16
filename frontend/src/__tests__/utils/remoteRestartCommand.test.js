import { describe, expect, it } from "vitest";
import {
	remoteRestartCommand,
	remoteRestartPlatform,
} from "../../utils/remoteRestartCommand";

describe("remoteRestartPlatform", () => {
	it("picks the command a host needs", () => {
		expect(remoteRestartPlatform({ os_type: "Windows Server 2022" })).toBe(
			"windows",
		);
		expect(remoteRestartPlatform({ os_type: "FreeBSD" })).toBe("freebsd");
		expect(remoteRestartPlatform({ os_type: "Alpine Linux" })).toBe("alpine");
		expect(
			remoteRestartPlatform({ os_type: "x", package_manager: "apk" }),
		).toBe("alpine");
		expect(remoteRestartPlatform({ os_type: "Ubuntu" })).toBe("linux");
		expect(remoteRestartPlatform(null)).toBe("linux");
	});
});

describe("remoteRestartCommand", () => {
	it("sets the switch with the agent, then restarts its service", () => {
		expect(remoteRestartCommand("linux")).toBe(
			"sudo /usr/local/bin/patchmon-agent config remote-restart on && sudo systemctl restart patchmon-agent",
		);
		expect(remoteRestartCommand("alpine")).toBe(
			"/usr/local/bin/patchmon-agent config remote-restart on && rc-service patchmon-agent restart",
		);
		expect(remoteRestartCommand("freebsd")).toBe(
			"/usr/local/bin/patchmon-agent config remote-restart on && service patchmon_agent restart",
		);
		expect(remoteRestartCommand("windows")).toBe(
			'& "C:\\Program Files\\PatchMon\\patchmon-agent.exe" config remote-restart on; if ($LASTEXITCODE -eq 0) { Restart-Service PatchMonAgent }',
		);
	});

	it("forbids them again with off", () => {
		expect(remoteRestartCommand("linux", false)).toContain(
			"config remote-restart off",
		);
	});
});
