import { Languages } from "lucide-react";
import { useTranslation } from "react-i18next";
import { currentLanguage, LANGUAGES, setLanguage } from "../i18n";

/**
 * Pick the UI language. The choice is kept in this browser, and changing it
 * reloads the page so that every screen switches. Without a choice, the
 * browser's language applies.
 *
 * compact renders a bare select for the login page's dark background; the
 * default renders the Profile section.
 */
export default function LanguageSelect({ compact = false }) {
	const { t } = useTranslation();
	const options = LANGUAGES.map((l) => (
		<option key={l.code} value={l.code} className="text-secondary-900">
			{l.label}
		</option>
	));
	const onChange = (e) => setLanguage(e.target.value);

	if (compact) {
		return (
			<label className="flex items-center gap-1.5 text-white/80">
				<Languages className="h-4 w-4" aria-hidden="true" />
				<select
					value={currentLanguage()}
					onChange={onChange}
					aria-label={t("Language")}
					className="rounded border border-white/20 bg-white/10 py-1 pl-2 pr-7 text-sm text-white focus:outline-none focus:ring-2 focus:ring-primary-400"
				>
					{options}
				</select>
			</label>
		);
	}

	return (
		<div className="border-t border-secondary-200 dark:border-secondary-600 pt-4 md:pt-6">
			<h4 className="text-sm font-medium text-secondary-700 dark:text-secondary-200 mb-3">
				{t("Language")}
			</h4>
			<div className="max-w-md">
				<label className="flex items-center gap-3">
					<Languages
						className="h-5 w-5 flex-shrink-0 text-secondary-600 dark:text-white"
						aria-hidden="true"
					/>
					<select
						value={currentLanguage()}
						onChange={onChange}
						aria-label={t("Language")}
						className="w-full rounded border border-secondary-300 dark:border-secondary-600 bg-white dark:bg-secondary-800 py-1.5 px-2 text-sm text-secondary-900 dark:text-white"
					>
						{options}
					</select>
				</label>
				<p className="mt-2 text-xs text-secondary-500 dark:text-secondary-400">
					{t(
						"Kept in this browser. Without a choice, the browser's language applies. The page reloads to switch.",
					)}
				</p>
			</div>
		</div>
	);
}
