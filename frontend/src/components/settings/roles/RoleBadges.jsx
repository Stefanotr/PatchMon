import { KeyRound, Lock, Server } from "lucide-react";
import { useTranslation } from "react-i18next";

const BADGE =
	"inline-flex items-center px-2 py-0.5 rounded-md text-[11px] font-medium";

// RoleMarks flags a role in one line: icons with a tooltip, the editor header
// spells them out.
export const RoleMarks = ({ rules, limited }) => {
	const { t } = useTranslation();
	const marks = [
		rules.oidcSynced && {
			Icon: KeyRound,
			label: t("OIDC Synced"),
			className: "text-blue-500 dark:text-blue-400",
		},
		!rules.oidcSynced &&
			rules.builtIn && {
				Icon: Lock,
				label: t("Built-in Role"),
				className: "text-secondary-400",
			},
		limited && {
			Icon: Server,
			label: t("Some host groups only"),
			className: "text-warning-600 dark:text-warning-400",
		},
	].filter(Boolean);
	return marks.map(({ Icon, label, className }) => (
		<span key={label} title={label} className="inline-flex shrink-0">
			<Icon className={`h-3.5 w-3.5 ${className}`} aria-hidden="true" />
			<span className="sr-only">{label}</span>
		</span>
	));
};

// RoleBadges tells at a glance what is special about a role.
const RoleBadges = ({ rules, limited }) => {
	const { t } = useTranslation();
	return (
		<>
			{rules.oidcSynced && (
				<span
					className={`${BADGE} bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200`}
				>
					{t("OIDC Synced")}
				</span>
			)}
			{!rules.oidcSynced && rules.builtIn && (
				<span
					className={`${BADGE} bg-primary-100 text-primary-800 dark:bg-primary-900 dark:text-primary-200`}
				>
					{t("Built-in Role")}
				</span>
			)}
			{limited && (
				<span
					className={`${BADGE} bg-warning-100 text-warning-800 dark:bg-warning-900/40 dark:text-warning-200`}
				>
					{t("Some host groups only")}
				</span>
			)}
		</>
	);
};

export default RoleBadges;
