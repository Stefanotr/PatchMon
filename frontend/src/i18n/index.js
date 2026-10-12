import { fr as frDateLocale } from "date-fns/locale/fr";
import i18n from "i18next";
import LanguageDetector from "i18next-browser-languagedetector";
import { initReactI18next } from "react-i18next";

// Translations are keyed by their English text: t("Restart host"). A string
// with no translation shows in English, so English needs no file of its own
// except for plural forms ("{{count}} host_one" / "_other"). Each area of the
// UI keeps its strings in its own file, locales/<lang>/<area>.json, so that
// changes to different screens do not collide. See README.md.

export const LANGUAGES = [
	{ code: "en", label: "English" },
	{ code: "fr", label: "Français" },
];

// Where the language a user picked is kept, per browser. Without a choice,
// the browser's language applies.
export const LANGUAGE_STORAGE_KEY = "patchmon-language";

const fragments = import.meta.glob("./locales/*/*.json", {
	eager: true,
	import: "default",
});

// buildResources merges the per-area files of each language into one
// "translation" namespace. French puts round millions (1 000 000 hôtes) in a
// "many" plural form: it falls back to the "_other" form, so the files only
// give "_one" and "_other".
export function buildResources(files) {
	const resources = {};
	for (const [path, strings] of Object.entries(files)) {
		const m = path.match(/\/locales\/([a-z]{2})\/[\w-]+\.json$/);
		if (!m) continue;
		const lang = m[1];
		if (!resources[lang]) resources[lang] = { translation: {} };
		Object.assign(resources[lang].translation, strings);
	}
	for (const { translation } of Object.values(resources)) {
		for (const [key, value] of Object.entries(translation)) {
			const base = key.endsWith("_other") ? key.slice(0, -6) : null;
			if (base !== null && !(`${base}_many` in translation)) {
				translation[`${base}_many`] = value;
			}
		}
	}
	return resources;
}

const supported = LANGUAGES.map((l) => l.code);

i18n
	.use(LanguageDetector)
	.use(initReactI18next)
	.init({
		resources: buildResources(fragments),
		fallbackLng: "en",
		supportedLngs: supported,
		load: "languageOnly",
		// Keys are English sentences: "." and ":" are text, not separators.
		keySeparator: false,
		nsSeparator: false,
		returnEmptyString: false,
		// React escapes what it renders. Never pass a translation to
		// dangerouslySetInnerHTML.
		interpolation: { escapeValue: false },
		detection: {
			order: ["localStorage", "navigator"],
			lookupLocalStorage: LANGUAGE_STORAGE_KEY,
			// Only an explicit choice is stored (setLanguage), so a user who
			// never picked one follows the browser's language.
			caches: [],
		},
		react: {
			useSuspense: false,
			// <Trans> interpolates values before it parses the sentence's tags:
			// unescaped, a value holding markup (a host name such as
			// "<b style=...>") would be parsed as tags and its attributes passed
			// as props. Values are escaped before the parse and the text nodes
			// unescaped after it, so a value always renders as plain text. Do not
			// override tOptions or shouldUnescape on a <Trans> (the translations
			// test checks it).
			transDefaultProps: {
				tOptions: { interpolation: { escapeValue: true } },
				shouldUnescape: true,
			},
		},
	});

const setDocumentLanguage = (lng) => {
	if (typeof document !== "undefined") {
		document.documentElement.lang = supported.includes(lng) ? lng : "en";
	}
};
setDocumentLanguage(i18n.resolvedLanguage);
i18n.on("languageChanged", () => setDocumentLanguage(i18n.resolvedLanguage));

// currentLanguage is the language the UI is shown in ("en" or "fr").
export const currentLanguage = () =>
	supported.includes(i18n.resolvedLanguage) ? i18n.resolvedLanguage : "en";

// dateFnsLocale is the date-fns locale of the UI language, for
// formatDistanceToNow and the like (undefined means English).
export const dateFnsLocale = () =>
	currentLanguage() === "fr" ? frDateLocale : undefined;

// setLanguage stores the user's choice in this browser and reloads the page,
// so that every screen, including text computed outside React, switches.
export function setLanguage(code) {
	if (!supported.includes(code)) return;
	try {
		window.localStorage.setItem(LANGUAGE_STORAGE_KEY, code);
	} catch {
		// Storage unavailable (private mode): the choice lasts this page only.
		i18n.changeLanguage(code);
		return;
	}
	window.location.reload();
}

// tk marks a string for translation where t() cannot be called, such as a
// label in a module-level constant: define it with tk("Label") and render it
// with t(label). It returns its argument unchanged; the translations test
// finds the strings it marks.
export const tk = (s) => s;

export default i18n;
