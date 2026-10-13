import { afterEach, describe, expect, it } from "vitest";
import api, { adminHostsAPI } from "../../utils/api";
import { patchingAPI } from "../../utils/patchingApi";

// The routes that can order a restart refuse a body that is not declared as
// JSON (CSRF), so every request to them must carry one, an empty object at
// least.
const originalAdapter = api.defaults.adapter;
afterEach(() => {
	api.defaults.adapter = originalAdapter;
});

const capture = () => {
	const sent = [];
	api.defaults.adapter = async (config) => {
		sent.push(config);
		return { data: {}, status: 200, statusText: "OK", headers: {}, config };
	};
	return sent;
};

const isJSON = (config) =>
	/^application\/json/.test(config.headers.get("Content-Type")) &&
	typeof JSON.parse(config.data) === "object";

describe("restart commands", () => {
	it("sends the cancel command as an empty JSON object", async () => {
		const sent = capture();
		await adminHostsAPI.cancelHostReboot("h1");
		expect(sent[0].url).toBe("/hosts/h1/reboot/cancel");
		expect(sent[0].data).toBe("{}");
		expect(sent[0].headers.get("Content-Type")).toMatch(/^application\/json/);
	});

	it("sends the restart command as JSON, even without options", async () => {
		const sent = capture();
		await adminHostsAPI.rebootHost("h1", { mode: "always", delay_minutes: 5 });
		await adminHostsAPI.rebootHost("h1", null);
		expect(JSON.parse(sent[0].data)).toEqual({
			mode: "always",
			delay_minutes: 5,
		});
		expect(sent[1].data).toBe("{}");
		for (const config of sent) {
			expect(config.headers.get("Content-Type")).toMatch(/^application\/json/);
		}
	});

	it("waits longer than the server waits for the agent", async () => {
		const sent = capture();
		await adminHostsAPI.rebootHost("h1", {});
		await adminHostsAPI.cancelHostReboot("h1");
		for (const config of sent) {
			expect(config.timeout).toBeGreaterThan(30000);
		}
	});
});

describe("patch runs, which can restart the host", () => {
	it("trigger a run with a JSON body, with or without a restart", async () => {
		const sent = capture();
		await patchingAPI.trigger("h1", "patch_all");
		await patchingAPI.trigger("h1", "patch_package", null, ["curl"], {
			reboot: { mode: "always", delay_minutes: 5 },
		});
		expect(sent.map((c) => c.url)).toEqual([
			"/patching/trigger",
			"/patching/trigger",
		]);
		expect(sent.every(isJSON)).toBe(true);
	});

	it("approve a run with a JSON body, an empty object without options", async () => {
		const sent = capture();
		await patchingAPI.approveRun("run-1");
		await patchingAPI.approveRun("run-1", { reboot: { mode: "if_needed" } });
		expect(sent[0].url).toBe("/patching/runs/run-1/approve");
		expect(sent[0].data).toBe("{}");
		expect(sent.every(isJSON)).toBe(true);
	});
});
