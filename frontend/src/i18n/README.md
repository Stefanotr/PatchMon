# Translating the UI

The UI is written in English and translated with
[react-i18next](https://react.i18next.com/). Translations are keyed by their
English text, so a string nobody translated yet still shows, in English.

## Files

- `index.js` sets i18next up: languages, detection, the merge of the files below.
- `locales/fr/<area>.json`: the French strings of one area of the UI
  (`hosts.json`, `patch-wizard.json`, ...), `"English text": "French text"`.
  One file per area keeps changes to different screens from colliding.
- `locales/en/<area>.json`: only the English plural forms of that area (see
  Plurals). Most areas have none.
- `src/__tests__/i18n/translations.test.js` fails when a string marked for
  translation has no French translation, when a translation loses a
  placeholder or a tag, or when two files translate a string differently.

The language is the one picked in **Profile**, kept in the browser
(`localStorage` key `patchmon-language`), or else the browser's language.

## Marking strings

```jsx
import { useTranslation } from "react-i18next";

function RestartButton({ host }) {
	const { t } = useTranslation();
	return (
		<button type="button" title={t("Restart {{name}}", { name: host.name })}>
			{t("Restart host")}
		</button>
	);
}
```

- **Translate what a person reads**: JSX text, `title`, `placeholder`,
  `aria-label`, `alt`, toast and confirm messages, option and column labels,
  empty states, errors written by the frontend.
- **Never translate what code reads**: values sent to the API or compared in
  code (`status === "pending"`), option `value`s, query keys, URLs, CSS
  classes, `data-testid`, `console` and logger messages, enum keys. Translate
  the label, not the value: `{ value: "never", label: t("Never") }`.
- **Text the server sends** (error messages, package names, host names,
  reasons reported by agents) stays as it comes: never pass it to `t()`. A
  fixed message the server always sends may be translated, by listing it in a
  `Set` of `tk()` strings and translating only its members (see
  `SERVER_RESTART_MESSAGES` in `utils/patchingApi.js`).
- **Whole sentences, never pieces**: word order differs between languages.
  Use placeholders, `t("{{count}} of {{total}} selected", { count, total })`,
  never `count + " of " + total + t(" selected")`.
- **Module-level constants** cannot call `t()` (no hook outside a component):
  mark the string with `tk()` from `../i18n` where it is defined and render it
  with `t(label)`. `tk()` returns its argument; it only lets the test find the
  string.
- **Plain helper functions** that return display text and run during render
  may call `i18n.t()` (`import i18n from "../i18n"`). Changing the language
  reloads the page, so text computed this way follows the choice too.
- **One English word, two meanings**: give the second one a context,
  `t("Reporting", { context: "status" })`, and translate
  `"Reporting_status"`. English shows the plain key.
- **Markup inside a sentence**: use `<Trans>` with named tags:

  ```jsx
  <Trans
  	i18nKey="Select the hosts to deploy <strong>{{count}}</strong> packages to."
  	values={{ count }}
  	components={{ strong: <strong /> }}
  />
  ```

  `index.js` escapes `<Trans>` values before the tags are parsed, so a host
  name holding markup still renders as text. Never pass `tOptions` or
  `shouldUnescape` to a `<Trans>`: that would undo it (the test checks).

- **Plurals**: pass `count` and give both forms in English and in French:

  ```js
  t("{{count}} host", { count })
  // locales/en/<area>.json: "{{count}} host_one": "{{count}} host", "{{count}} host_other": "{{count}} hosts"
  // locales/fr/<area>.json: "{{count}} host_one": "{{count}} hôte", "{{count}} host_other": "{{count}} hôtes"
  ```

  French uses `_one` for 0 and 1 (round millions use `_other`, added by
  `index.js`). A string that only needs `count` as a number
  ("Selected: {{count}}") is not a plural.
- **Dates**: `formatRelativeTime()` from `utils/api` follows the language;
  with date-fns, pass `locale: dateFnsLocale()` from `../i18n`.
- Keep the English text exactly as it was, so English users see no change and
  existing tests keep finding their text.

## Writing French

- Address the user as « vous »; buttons use the infinitive (« Redémarrer »,
  « Annuler »).
- Non-breaking space (` ` in JSON) before `:`, `;`, `!`, `?`, `»` and
  after `«`.
- Keep placeholders (`{{name}}`), tags (`<strong>`) and product names
  (PatchMon, Docker, WinGet, Windows Update) unchanged.
- Sentence case, like the English UI: « Mises à jour de sécurité », not
  « Mises À Jour De Sécurité ».

### Glossary

| English | Français |
|---|---|
| Host / Hosts | Hôte / Hôtes |
| Host group | Groupe d'hôtes |
| Package | Paquet |
| Update, updates available | Mise à jour, mises à jour disponibles |
| Security update | Mise à jour de sécurité |
| Outdated | Obsolète |
| Up to date | À jour |
| Patch (noun) / patch run | Patch / exécution de patch |
| Patch (verb) / Patch all | Patcher / Tout patcher |
| Patching (page) | Patching |
| Dry run / Validate | Dry run (simulation) / Valider |
| Approve / Approval | Approuver / Approbation |
| Schedule / Scheduled | Planifier / Planifié |
| Policy / Patch policy | Politique / Politique de patch |
| Maintenance window | Fenêtre de maintenance |
| Restart / Reboot | Redémarrage / Redémarrer |
| Reboot required | Redémarrage requis |
| Agent | Agent |
| Report / Last report | Rapport / Dernier rapport |
| Repository | Dépôt |
| Dashboard | Tableau de bord |
| Settings | Paramètres |
| Compliance | Conformité |
| Alerts | Alertes |
| Reporting | Rapports |
| Users / Roles | Utilisateurs / Rôles |
| Sign in / Sign out | Se connecter / Se déconnecter |
| Uptime | Uptime |
| Container | Conteneur |
| Pending | En attente |
| Running / Completed / Failed | En cours / Terminé / Échec |
| Online / Offline | En ligne / Hors ligne |
| Stale (agent silent) / Overdue | Silencieux / En retard |
| Refresh | Actualiser |
| Search | Rechercher |
| Filter / Filters | Filtrer / Filtres |
| Delete / Remove | Supprimer / Retirer |
| Save / Cancel / Close | Enregistrer / Annuler / Fermer |
