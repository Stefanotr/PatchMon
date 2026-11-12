import { currentLanguage, tk } from "../i18n";

// Days of the week as the server counts them: 0 is Sunday, 6 Saturday.
// The form lists them from Monday.
export const WEEK_ORDER = [1, 2, 3, 4, 5, 6, 0];

// 2023-01-01 was a Sunday.
const SUNDAY_MS = Date.UTC(2023, 0, 1);
const DAY_MS = 86400000;

// weekdayName names day (0 = Sunday) in the language of the UI.
export function weekdayName(day, style = "long", lang = currentLanguage()) {
	return new Intl.DateTimeFormat(lang, {
		weekday: style,
		timeZone: "UTC",
	}).format(new Date(SUNDAY_MS + day * DAY_MS));
}

function listOf(items, lang) {
	try {
		return new Intl.ListFormat(lang, {
			style: "long",
			type: "conjunction",
		}).format(items);
	} catch {
		return items.join(", ");
	}
}

// The week of the month, 1 to 4, or 5 for the last one.
export const MONTH_WEEKS = [
	{ value: 1, label: tk("First") },
	{ value: 2, label: tk("Second") },
	{ value: 3, label: tk("Third") },
	{ value: 4, label: tk("Fourth") },
	{ value: 5, label: tk("Last") },
];

const MONTHLY_PHRASES = {
	1: tk("First {{weekday}} of the month at {{time}}"),
	2: tk("Second {{weekday}} of the month at {{time}}"),
	3: tk("Third {{weekday}} of the month at {{time}}"),
	4: tk("Fourth {{weekday}} of the month at {{time}}"),
	5: tk("Last {{weekday}} of the month at {{time}}"),
};

// describeRecurrence says when schedule runs, in one line.
export function describeRecurrence(t, schedule, lang = currentLanguage()) {
	const time = schedule.time_of_day;
	if (schedule.frequency === "monthly") {
		const phrase = MONTHLY_PHRASES[schedule.month_week];
		if (!phrase) return time;
		return t(phrase, {
			weekday: weekdayName(schedule.month_weekday, "long", lang),
			time,
		});
	}
	const days = WEEK_ORDER.filter((d) => (schedule.weekdays || []).includes(d));
	if (days.length === 7) return t("Every day at {{time}}", { time });
	return t("Weekly on {{days}} at {{time}}", {
		days: listOf(
			days.map((d) => weekdayName(d, "long", lang)),
			lang,
		),
		time,
	});
}

// summarizeResult says what the last firing of a schedule did, or "" when
// it never fired.
export function summarizeResult(t, result) {
	if (!result) return "";
	if (result.missed) {
		return t("Missed: the server was down at the scheduled time");
	}
	const parts = [t("{{count}} dry run queued", { count: result.queued || 0 })];
	if (result.up_to_date) {
		parts.push(t("{{count}} host up to date", { count: result.up_to_date }));
	}
	if (result.busy) {
		parts.push(t("{{count}} host busy", { count: result.busy }));
	}
	if (result.unsupported) {
		parts.push(
			t("{{count}} host not supported", { count: result.unsupported }),
		);
	}
	if (result.failed) {
		parts.push(t("{{count}} host failed", { count: result.failed }));
	}
	return parts.join(", ");
}

// scheduleToForm and formToPayload convert between a schedule from the API
// and the form state.
export const DEFAULT_FORM = {
	name: "",
	host_group_id: "",
	scope: "security",
	frequency: "weekly",
	weekdays: [3],
	month_week: 2,
	month_weekday: 3,
	time_of_day: "03:00",
	enabled: true,
};

export function scheduleToForm(schedule) {
	return {
		name: schedule.name,
		host_group_id: schedule.host_group_id || "",
		scope: schedule.scope,
		frequency: schedule.frequency,
		weekdays: schedule.weekdays?.length
			? [...schedule.weekdays]
			: DEFAULT_FORM.weekdays,
		month_week: schedule.month_week || DEFAULT_FORM.month_week,
		month_weekday:
			schedule.frequency === "monthly"
				? schedule.month_weekday
				: DEFAULT_FORM.month_weekday,
		time_of_day: schedule.time_of_day,
		enabled: schedule.enabled,
	};
}

export function formToPayload(form) {
	const monthly = form.frequency === "monthly";
	return {
		name: form.name.trim(),
		host_group_id: form.host_group_id || null,
		scope: form.scope,
		frequency: form.frequency,
		weekdays: monthly ? [] : [...form.weekdays].sort((a, b) => a - b),
		month_week: monthly ? Number(form.month_week) : 0,
		month_weekday: monthly ? Number(form.month_weekday) : 0,
		time_of_day: form.time_of_day,
		enabled: form.enabled,
	};
}
