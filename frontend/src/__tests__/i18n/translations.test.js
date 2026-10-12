import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { buildResources } from "../../i18n";

// Checks every string marked for translation against the locale files.

const srcDir = path.resolve(
	path.dirname(fileURLToPath(import.meta.url)),
	"../..",
);
const localesDir = path.join(srcDir, "i18n", "locales");

const readLocale = (lang) => {
	const dir = path.join(localesDir, lang);
	if (!fs.existsSync(dir)) return [];
	return fs
		.readdirSync(dir)
		.filter((f) => f.endsWith(".json"))
		.sort()
		.map((f) => ({
			file: `${lang}/${f}`,
			strings: JSON.parse(fs.readFileSync(path.join(dir, f), "utf8")),
		}));
};

const merge = (files) => Object.assign({}, ...files.map((f) => f.strings));

const sourceFiles = (dir) =>
	fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
		const p = path.join(dir, e.name);
		if (e.isDirectory()) {
			return e.name === "__tests__" || e.name === "i18n" ? [] : sourceFiles(p);
		}
		return /\.(jsx?|tsx?)$/.test(e.name) ? [p] : [];
	});

// Literal keys: t("..."), tk("..."), i18n.t("..."), i18nKey="...", in
// double or single quotes (biome switches to single quotes when the text
// holds a double quote).
const LITERAL = String.raw`(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)')`;
const KEY_PATTERNS = [
	new RegExp(String.raw`(?<![\w$.])(?:t|tk)\(\s*${LITERAL}`, "g"),
	new RegExp(String.raw`\bi18n\.t\(\s*${LITERAL}`, "g"),
	new RegExp(String.raw`\bi18nKey=\{?\s*${LITERAL}`, "g"),
];

// unquote turns the body of a JS string literal into its value.
const unquote = (double, single) =>
	double !== undefined
		? JSON.parse(`"${double}"`)
		: JSON.parse(`"${single.replace(/\\'/g, "'").replace(/"/g, '\\"')}"`);

const usedKeys = () => {
	const keys = new Map();
	for (const file of sourceFiles(srcDir)) {
		const text = fs.readFileSync(file, "utf8");
		for (const re of KEY_PATTERNS) {
			for (const m of text.matchAll(re)) {
				const key = unquote(m[1], m[2]);
				if (!keys.has(key)) keys.set(key, path.relative(srcDir, file));
			}
		}
	}
	return keys;
};

const PLURAL = /_(one|other)$/;
const placeholders = (s) =>
	[...s.matchAll(/\{\{\s*([\w.]+)[^}]*\}\}/g)].map((m) => m[1]).sort();
const tags = (s) =>
	[...s.matchAll(/<\/?([a-zA-Z0-9]+)\s*\/?>/g)].map((m) => m[0]).sort();

const fr = readLocale("fr");
const en = readLocale("en");
const frStrings = merge(fr);
const enStrings = merge(en);

describe("translations", () => {
	it("translates every string marked for translation into French", () => {
		const missing = [];
		for (const [key, file] of usedKeys()) {
			if (!(key in frStrings) && !(`${key}_other` in frStrings)) {
				missing.push(`${file}: ${key}`);
			}
		}
		expect(missing).toEqual([]);
	});

	it("gives both plural forms in English and in French", () => {
		const problems = [];
		const bases = new Set(
			Object.keys(frStrings)
				.filter((k) => PLURAL.test(k))
				.map((k) => k.replace(PLURAL, "")),
		);
		for (const base of bases) {
			for (const form of ["_one", "_other"]) {
				if (!(base + form in frStrings)) problems.push(`fr: ${base}${form}`);
				if (!(base + form in enStrings)) problems.push(`en: ${base}${form}`);
			}
		}
		expect(problems).toEqual([]);
	});

	it("keeps the placeholders and tags of the English text", () => {
		const problems = [];
		for (const [key, value] of Object.entries(frStrings)) {
			if (typeof value !== "string" || value.trim() === "") {
				problems.push(`${key}: empty translation`);
				continue;
			}
			const english = enStrings[key] ?? key.replace(PLURAL, "");
			const drop = key.endsWith("_one") ? ["count"] : [];
			const want = placeholders(english).filter((p) => !drop.includes(p));
			const got = placeholders(value).filter((p) => !drop.includes(p));
			if (want.join() !== got.join()) {
				problems.push(`${key}: placeholders ${got} instead of ${want}`);
			}
			if (tags(english).join() !== tags(value).join()) {
				problems.push(
					`${key}: tags ${tags(value)} instead of ${tags(english)}`,
				);
			}
		}
		expect(problems).toEqual([]);
	});

	it("never translates one string two ways", () => {
		const conflicts = [];
		for (const files of [fr, en]) {
			const seen = new Map();
			for (const { file, strings } of files) {
				for (const [key, value] of Object.entries(strings)) {
					const prev = seen.get(key);
					if (prev && prev.value !== value) {
						conflicts.push(`${key}: ${prev.file} vs ${file}`);
					}
					seen.set(key, { file, value });
				}
			}
		}
		expect(conflicts).toEqual([]);
	});

	it("leaves the escaping of <Trans> values alone", () => {
		// index.js escapes <Trans> values; a local tOptions or shouldUnescape
		// would replace that default and let a value inject markup. A file
		// that renders <Trans> may not use either name at all.
		const offenders = sourceFiles(srcDir)
			.filter((file) => {
				const text = fs.readFileSync(file, "utf8");
				return (
					/<Trans\b/.test(text) && /\b(tOptions|shouldUnescape)\b/.test(text)
				);
			})
			.map((file) => path.relative(srcDir, file));
		expect(offenders).toEqual([]);
	});

	it("merges the area files of each language", () => {
		const res = buildResources({
			"./locales/fr/a.json": { One: "Un" },
			"./locales/fr/b.json": { Two: "Deux" },
			"./locales/en/a.json": { "{{count}} x_one": "{{count}} x" },
			"./locales/../evil.json": { One: "pwned" },
		});
		expect(res.fr.translation).toEqual({ One: "Un", Two: "Deux" });
		expect(Object.keys(res)).toEqual(["fr", "en"]);
		expect(res.en.translation["{{count}} x_many"]).toBeUndefined();
	});

	it("gives round millions the plural form in French", () => {
		const res = buildResources({
			"./locales/fr/a.json": {
				"{{count}} host_one": "{{count}} hôte",
				"{{count}} host_other": "{{count}} hôtes",
			},
		});
		expect(res.fr.translation["{{count}} host_many"]).toBe("{{count}} hôtes");
	});
});
