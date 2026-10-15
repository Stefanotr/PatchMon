import { describe, expect, it } from "vitest";
import {
	shellInstallArgs,
	windowsScriptCall,
	withScriptArgs,
} from "../../utils/installCommand";

describe("install command arguments", () => {
	it("passes nothing by default", () => {
		expect(shellInstallArgs()).toEqual([]);
		expect(withScriptArgs("sudo sh", {})).toBe("sudo sh");
		expect(windowsScriptCall()).toBe('& "$env:TEMP\\patchmon-install.ps1"');
	});

	it("adds the options the host's administrator chose", () => {
		expect(withScriptArgs("sudo sh", { force: true })).toBe(
			"sudo sh -s -- --force",
		);
		expect(withScriptArgs("sh", { allowRemoteRestart: true })).toBe(
			"sh -s -- --allow-remote-restart",
		);
		expect(
			withScriptArgs("sudo sh", { force: true, allowRemoteRestart: true }),
		).toBe("sudo sh -s -- --force --allow-remote-restart");
		expect(withScriptArgs("bash", { allowRemoteRestart: true })).toBe(
			"bash -s -- --allow-remote-restart",
		);
		expect(windowsScriptCall({ allowRemoteRestart: true })).toBe(
			'& "$env:TEMP\\patchmon-install.ps1" -AllowRemoteRestart',
		);
	});
});
