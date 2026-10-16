import { tk } from "../i18n";

// Display labels of the rule severities the API sends; the values themselves
// stay as sent.
export const SEVERITY_LABELS = {
	critical: tk("Critical"),
	high: tk("High"),
	medium: tk("Medium"),
	low: tk("Low"),
	unknown: tk("Unknown"),
};

// Mirrors classifyComplianceScan in server-source-code/internal/store/compliance_dashboard.go.
export const hostComplianceStatus = (row) => {
	if (row?.compliance_status) return row.compliance_status;
	const score = Number(row?.score);
	if (score >= 80) return "compliant";
	if (score >= 60 || !(Number(row?.failed) > 0)) return "warning";
	return "critical";
};
