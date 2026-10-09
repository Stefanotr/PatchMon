/**
 * pollDryRunUntilDone: a host that never picks the run up is offline after a
 * short wait, but a run the agent is working on (a Windows Update search can
 * take minutes) is waited for, and aborted runs stop the wait at once.
 */

import { beforeEach, describe, expect, it, vi } from "vitest";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../utils/api", () => ({ default: { get } }));

import { pollDryRunUntilDone } from "../../utils/patchingApi";

// Serves the given statuses in order, repeating the last one.
const serve = (...runs) => {
	let i = 0;
	get.mockImplementation(() => {
		const run = runs[Math.min(i, runs.length - 1)];
		i += 1;
		return Promise.resolve({ data: run });
	});
};

const fast = { intervalMs: 0, pickupTimeoutMs: 20, maxWaitMs: 60 };

describe("pollDryRunUntilDone", () => {
	beforeEach(() => {
		get.mockReset();
	});

	it("returns the validated packages", async () => {
		serve(
			{ status: "pending_validation" },
			{ status: "running" },
			{
				status: "validated",
				packages_affected: ["Mozilla Firefox (x64 en-US)"],
				shell_output: "[plan] Mozilla Firefox (x64 en-US)\n",
			},
		);
		const res = await pollDryRunUntilDone("r1", fast);
		expect(res.status).toBe("validated");
		expect(res.packages_affected).toEqual(["Mozilla Firefox (x64 en-US)"]);
	});

	it("reports an offline host when the run is never picked up", async () => {
		serve({ status: "pending_validation" });
		const res = await pollDryRunUntilDone("r1", fast);
		expect(res.status).toBe("timeout");
		expect(res.still_running).toBeFalsy();
		expect(res.error).toMatch(/offline/);
	});

	it("keeps waiting past the pickup timeout while the agent runs it", async () => {
		let calls = 0;
		get.mockImplementation(async () => {
			calls += 1;
			await new Promise((r) => setTimeout(r, 5));
			if (calls < 8) return { data: { status: "running" } };
			return {
				data: { status: "validated", packages_affected: ["KB5065432"] },
			};
		});
		const res = await pollDryRunUntilDone("r1", {
			intervalMs: 0,
			pickupTimeoutMs: 10,
			maxWaitMs: 5000,
		});
		expect(res.status).toBe("validated");
		expect(calls).toBe(8);
	});

	it("flags a run still going at the end of the wait as still running", async () => {
		serve({ status: "running", shell_output: "Searching Windows Update\n" });
		const res = await pollDryRunUntilDone("r1", fast);
		expect(res.status).toBe("timeout");
		expect(res.still_running).toBe(true);
		expect(res.error).toMatch(/still running/);
	});

	it.each([
		["cancelled", /cancelled/],
		["timed_out", /timed out/],
		["agent_disconnected", /disconnected/],
	])("stops at once on %s", async (status, message) => {
		serve({ status });
		const res = await pollDryRunUntilDone("r1", fast);
		expect(res.status).toBe("failed");
		expect(res.error).toMatch(message);
		expect(get).toHaveBeenCalledTimes(1);
	});

	it("passes the agent's failure message through", async () => {
		serve({ status: "failed", error_message: "1 item(s) failed: KB5065432" });
		const res = await pollDryRunUntilDone("r1", fast);
		expect(res).toMatchObject({
			status: "failed",
			error: "1 item(s) failed: KB5065432",
		});
	});
});
