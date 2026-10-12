import {
	Container,
	Cpu,
	Globe,
	Network,
	Package,
	Server,
	Shield,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import i18n, { tk } from "../../i18n";

// Icon and label mapping for each section name an agent can report on.
// New section keys: extend SECTION_META below.
const SECTION_META = {
	packages: { icon: Package, label: tk("Packages") },
	metrics: { icon: Cpu, label: tk("Metrics") },
	hostname: { icon: Server, label: tk("Hostname") },
	interfaces: { icon: Network, label: tk("Interfaces") },
	repos: { icon: Globe, label: tk("Repos") },
	repositories: { icon: Globe, label: tk("Repos") },
	compliance: { icon: Shield, label: tk("Compliance") },
	docker: { icon: Container, label: tk("Docker") },
};

const FALLBACK = { icon: Package, label: null };

const formatLabel = (key) => {
	if (!key) return i18n.t("Section");
	return key
		.toString()
		.replace(/[_-]+/g, " ")
		.replace(/\s+/g, " ")
		.trim()
		.split(" ")
		.map((word) =>
			word.length === 0 ? word : word[0].toUpperCase() + word.slice(1),
		)
		.join(" ");
};

const Chip = ({ name, variant }) => {
	const { t } = useTranslation();
	const meta = SECTION_META[name?.toLowerCase()] || FALLBACK;
	const Icon = meta.icon;
	const label = meta.label ? t(meta.label) : formatLabel(name);

	const tooltip =
		variant === "updated"
			? t("{{label}} updated: agent sent fresh data this cycle", { label })
			: t(
					"{{label}} skipped: hash matched server's stored value, no payload sent",
					{ label },
				);

	const baseClasses =
		"inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium whitespace-nowrap";
	// Neutral palette across both states; the line-through + opacity convey
	// "skipped" without needing a different colour.
	const updatedClasses =
		"bg-secondary-100 text-secondary-700 dark:bg-secondary-700 dark:text-secondary-100";
	const skippedClasses =
		"bg-secondary-100 text-secondary-500 dark:bg-secondary-700 dark:text-secondary-300 line-through opacity-80";

	return (
		<span
			className={`${baseClasses} ${variant === "updated" ? updatedClasses : skippedClasses}`}
			title={tooltip}
		>
			<Icon className="h-3 w-3 flex-shrink-0" />
			<span>{label}</span>
		</span>
	);
};

const SectionChips = ({ sectionsSent = [], sectionsUnchanged = [] }) => {
	const sent = Array.isArray(sectionsSent) ? sectionsSent : [];
	const skipped = Array.isArray(sectionsUnchanged) ? sectionsUnchanged : [];

	if (sent.length === 0 && skipped.length === 0) {
		return (
			<span className="text-xs text-secondary-500 dark:text-secondary-300">
				-
			</span>
		);
	}

	return (
		<div className="flex flex-wrap items-center gap-1">
			{sent.map((name) => (
				<Chip key={`sent-${name}`} name={name} variant="updated" />
			))}
			{skipped.map((name) => (
				<Chip key={`skipped-${name}`} name={name} variant="skipped" />
			))}
		</div>
	);
};

export default SectionChips;
