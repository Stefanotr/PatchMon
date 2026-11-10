import { useTranslation } from "react-i18next";
import { TIERS } from "../constants/tiers";

const TierBadge = ({ tier, className = "" }) => {
	const { t } = useTranslation();
	const tierDef = TIERS[tier];
	if (!tierDef) return null;

	return (
		<span
			className={`inline-flex items-center px-1.5 py-px rounded text-[10px] font-semibold uppercase tracking-wide leading-tight ${tierDef.badgeClass} ${className}`}
			title={t("Requires {{tier}} plan", { tier: tierDef.name })}
		>
			{tierDef.name}
		</span>
	);
};

export default TierBadge;
