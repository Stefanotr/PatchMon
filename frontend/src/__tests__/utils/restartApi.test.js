import { afterEach, describe, expect, it } from "vitest";
import api, { adminHostsAPI } from "../../utils/api";

// The restart routes refuse a body that is not declared as JSON (CSRF), so
// every restart request must carry one, an empty object at least.
describe("restart commands", () => {
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
