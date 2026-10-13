import i18n, { tk } from "../i18n";

/**
 * Display label overrides for alert types. The internal alert_type IDs (the
 * keys here) MUST stay unchanged across the codebase — they are used by the
 * DB, the API, and external integrations. This map renames the user-visible
 * label only.
 */
export const ALERT_LABEL_OVERRIDES = {
	host_down: tk("Host Agent Down"),
	host_recovered: tk("Host Agent Recovered"),
	// Add future overrides here.
};

// English labels of the other alert types the server raises, the Title Case
// form of their identifier, so that they can be translated.
const ALERT_TYPE_LABELS = {
	host_enrolled: tk("Host Enrolled"),
	host_deleted: tk("Host Deleted"),
	host_security_updates_exceeded: tk("Host Security Updates Exceeded"),
	host_pending_updates_exceeded: tk("Host Pending Updates Exceeded"),
	host_security_updates_resolved: tk("Host Security Updates Resolved"),
	host_pending_updates_resolved: tk("Host Pending Updates Resolved"),
	patch_run_started: tk("Patch Run Started"),
	patch_run_completed: tk("Patch Run Completed"),
	patch_run_failed: tk("Patch Run Failed"),
	patch_run_approved: tk("Patch Run Approved"),
	patch_run_cancelled: tk("Patch Run Cancelled"),
	patch_reboot_required: tk("Patch Reboot Required"),
	container_stopped: tk("Container Stopped"),
	container_started: tk("Container Started"),
	container_image_update_available: tk("Container Image Update Available"),
	compliance_scan_completed: tk("Compliance Scan Completed"),
	compliance_scan_failed: tk("Compliance Scan Failed"),
	user_login: tk("User Login"),
	user_login_failed: tk("User Login Failed"),
	account_locked: tk("Account Locked"),
	user_created: tk("User Created"),
	user_role_changed: tk("User Role Changed"),
	user_tfa_disabled: tk("User Tfa Disabled"),
	ssh_session_started: tk("Ssh Session Started"),
	rdp_session_started: tk("Rdp Session Started"),
	server_update: tk("Server Update"),
	agent_update: tk("Agent Update"),
};

/**
 * Resolve a human-readable label for an alert type, in the UI language. Falls
 * back to a Title Case conversion of the snake_case identifier, untranslated,
 * for a type this map does not know.
 */
export const formatAlertType = (type) => {
	if (!type) return "";
	const label = ALERT_LABEL_OVERRIDES[type] ?? ALERT_TYPE_LABELS[type];
	if (label) return i18n.t(label);
	return type.replace(/_/g, " ").replace(/\b\w/g, (l) => l.toUpperCase());
};
