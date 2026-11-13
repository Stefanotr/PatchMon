import { describe, expect, it } from "vitest";
import {
	DEFAULT_FORM,
	describeRecurrence,
	formToPayload,
	scheduleToForm,
	summarizeResult,
	weekdayName,
} from "../../utils/patchSchedules";

// A t() that shows the key and its values, to check what is asked for.
const t = (key, values = {}) =>
	key.replace(/\{\{(\w+)\}\}/g, (_, name) => String(values[name]));

describe("weekdayName", () => {
	it("counts from Sunday, like the server", () => {
		expect(weekdayName(0, "long", "en")).toBe("Sunday");
		expect(weekdayName(3, "long", "en")).toBe("Wednesday");
		expect(weekdayName(6, "long", "fr")).toBe("samedi");
	});
});

describe("describeRecurrence", () => {
	it("lists weekly days from Monday", () => {
		const s = {
			frequency: "weekly",
			weekdays: [4, 0, 2],
			time_of_day: "03:00",
		};
		expect(describeRecurrence(t, s, "en")).toBe(
			"Weekly on Tuesday, Thursday, and Sunday at 03:00",
		);
	});

	it("says every day for all seven", () => {
		const s = {
			frequency: "weekly",
			weekdays: [0, 1, 2, 3, 4, 5, 6],
			time_of_day: "22:30",
		};
		expect(describeRecurrence(t, s, "en")).toBe("Every day at 22:30");
	});

	it("names the week of the month", () => {
		const s = {
			frequency: "monthly",
			month_week: 2,
			month_weekday: 3,
			time_of_day: "22:00",
		};
		expect(describeRecurrence(t, s, "en")).toBe(
			"Second Wednesday of the month at 22:00",
		);
		expect(describeRecurrence(t, { ...s, month_week: 5 }, "en")).toBe(
			"Last Wednesday of the month at 22:00",
		);
	});
});

describe("summarizeResult", () => {
	it("is empty before the first firing", () => {
		expect(summarizeResult(t, null)).toBe("");
	});

	it("says a slot was missed", () => {
		expect(summarizeResult(t, { missed: true, queued: 0 })).toMatch(/^Missed/);
	});

	it("lists only what happened", () => {
		expect(
			summarizeResult(t, { queued: 3, up_to_date: 2, busy: 0, failed: 1 }),
		).toBe("3 dry run queued, 2 host up to date, 1 host failed");
	});
});

describe("form conversion", () => {
	it("sends only the fields of the frequency", () => {
		const weekly = formToPayload({
			...DEFAULT_FORM,
			name: "  nightly ",
			weekdays: [5, 1],
		});
		expect(weekly).toMatchObject({
			name: "nightly",
			host_group_id: null,
			weekdays: [1, 5],
			month_week: 0,
			month_weekday: 0,
		});
		const monthly = formToPayload({
			...DEFAULT_FORM,
			name: "m",
			frequency: "monthly",
			host_group_id: "g1",
		});
		expect(monthly).toMatchObject({
			host_group_id: "g1",
			weekdays: [],
			month_week: 2,
			month_weekday: 3,
		});
	});

	it("round-trips a schedule", () => {
		const s = {
			name: "m",
			host_group_id: null,
			scope: "all",
			frequency: "monthly",
			weekdays: [],
			month_week: 5,
			month_weekday: 0,
			time_of_day: "06:15",
			enabled: false,
		};
		expect(formToPayload(scheduleToForm(s))).toEqual(s);
	});
});
