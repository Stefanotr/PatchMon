import { describe, expect, it } from "vitest";
import {
	buildRebootRequest,
	DEFAULT_RESTART_CHOICE,
	describeReboot,
	describeRestartAnswer,
	nextOccurrence,
	restartChoiceError,
} from "../../utils/patchingApi";

describe("restart choice", () => {
	const now = new Date(2026, 9, 9, 16, 0, 0);

	it("sends nothing when the run must not restart", () => {
		expect(buildRebootRequest(DEFAULT_RESTART_CHOICE)).toBeNull();
		expect(buildRebootRequest(null)).toBeNull();
	});

	it("sends a delay, with the message trimmed", () => {
		expect(
			buildRebootRequest({
				...DEFAULT_RESTART_CHOICE,
				mode: "if_needed",
				delayMinutes: "15",
				message: "  window ",
			}),
		).toEqual({ mode: "if_needed", delay_minutes: 15, message: "window" });
	});

	it("turns a time of day into the next occurrence", () => {
		const later = buildRebootRequest(
			{
				...DEFAULT_RESTART_CHOICE,
				mode: "always",
				when: "at",
				atTime: "18:30",
			},
			{ now },
		);
		expect(new Date(later.at)).toEqual(new Date(2026, 9, 9, 18, 30, 0));
		expect(later.delay_minutes).toBeUndefined();

		// Already past today: tomorrow. Still ahead, however close: today
		// (the agent waits its minimum delay).
		expect(nextOccurrence("03:00", now)).toEqual(
			new Date(2026, 9, 10, 3, 0, 0),
		);
		expect(nextOccurrence("16:00", now)).toEqual(
			new Date(2026, 9, 10, 16, 0, 0),
		);
		expect(nextOccurrence("16:01", now)).toEqual(
			new Date(2026, 9, 9, 16, 1, 0),
		);
	});

	it("lets the restart command force the mode", () => {
		expect(
			buildRebootRequest(DEFAULT_RESTART_CHOICE, { mode: "always" }),
		).toEqual({ mode: "always", delay_minutes: 5 });
	});

	it("validates the delay and the time", () => {
		const base = { ...DEFAULT_RESTART_CHOICE, mode: "always" };
		expect(restartChoiceError(base)).toBeNull();
		expect(restartChoiceError({ ...base, delayMinutes: 1 })).toMatch(/between/);
		expect(restartChoiceError({ ...base, delayMinutes: "2.5" })).toMatch(
			/between/,
		);
		expect(restartChoiceError({ ...base, when: "at", atTime: "" })).toMatch(
			/time/,
		);
		expect(
			restartChoiceError({ ...base, when: "at", atTime: "23:59" }),
		).toBeNull();
		expect(restartChoiceError(DEFAULT_RESTART_CHOICE)).toBeNull();
	});

	it("describes the option recorded on a run", () => {
		expect(describeReboot(undefined)).toBeNull();
		expect(describeReboot({ mode: "never" })).toBeNull();
		expect(describeReboot({ mode: "if_needed", delay_minutes: 15 })).toBe(
			"Restart after the run if one is needed, 15 min after it ends",
		);
		expect(
			describeReboot({ mode: "always", at: "2026-10-10T01:00:00Z" }),
		).toMatch(/^Restart after the run, at /);
	});

	it("counts the message in bytes, like the agent", () => {
		const choice = { ...DEFAULT_RESTART_CHOICE, mode: "always" };
		expect(restartChoiceError({ ...choice, message: "a".repeat(200) })).toBe(
			null,
		);
		// 101 two-byte characters: 101 characters, 202 bytes.
		expect(
			restartChoiceError({ ...choice, message: "\u00e9".repeat(101) }),
		).toMatch(/200 bytes/);
	});
});

describe("restart command answer", () => {
	it("confirms what the agent did, with its time", () => {
		const answer = describeRestartAnswer(
			{
				status: 200,
				data: {
					message: "Restart scheduled",
					reboot_at: "2026-10-10T01:00:00Z",
				},
			},
			"fallback",
		);
		expect(answer.level).toBe("success");
		expect(answer.text).toMatch(/^Restart scheduled, for /);
	});

	it("falls back to the given text", () => {
		expect(describeRestartAnswer({ status: 200, data: {} }, "Done")).toEqual({
			level: "success",
			text: "Done",
		});
	});

	it("warns when the agent did not answer", () => {
		const answer = describeRestartAnswer(
			{
				status: 202,
				data: { message: "Sent to the agent, which did not answer." },
			},
			"Restart scheduled",
		);
		expect(answer).toEqual({
			level: "warning",
			text: "Sent to the agent, which did not answer.",
		});
	});
});
