import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { formatDistanceToNow } from "date-fns";
import {
	AlertTriangle,
	BarChart3,
	CheckCircle,
	Clock,
	Container,
	History,
	ListChecks,
	PieChart as PieChartIcon,
	Play,
	RefreshCw,
	Server,
	Settings,
	ShieldAlert,
	ShieldCheck,
	ShieldOff,
	StopCircle,
	Users,
	Wifi,
	WifiOff,
	X,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Doughnut } from "react-chartjs-2";
import { useTranslation } from "react-i18next";
import { Link, useLocation, useSearchParams } from "react-router-dom";
import {
	Bar,
	BarChart,
	Cell,
	ResponsiveContainer,
	Tooltip,
	XAxis,
	YAxis,
} from "recharts";
import ComplianceSettings from "../components/compliance/ComplianceSettings";
import ScanHistoryTab from "../components/compliance/ScanHistoryTab";
import ScanResultsTab from "../components/compliance/ScanResultsTab";
import {
	ComplianceProfilesPie,
	FailuresBySeverityDoughnut,
	HostComplianceStatusBar,
	LastScanAgeBar,
	OpenSCAPDistributionDoughnut,
} from "../components/compliance/widgets";
import { getDoughnutOptions } from "../components/compliance/widgets/chartOptions";
import ModalPortal from "../components/ui/ModalPortal";
import { useTheme } from "../contexts/ThemeContext";
import { useToast } from "../contexts/ToastContext";
import { useTabParam } from "../hooks/useTabParam";
import { dateFnsLocale, tk } from "../i18n";
import { adminHostsAPI, settingsAPI } from "../utils/api";
import { complianceAPI } from "../utils/complianceApi";
import {
	hostComplianceStatus,
	SEVERITY_LABELS,
} from "../utils/complianceStatus";
import {
	deriveReportingStateByTime,
	hasNeverReported,
} from "../utils/hostStatus";

// Custom tooltip component for consistent styling across all charts
const CustomTooltip = ({ active, payload, label, type }) => {
	const { t } = useTranslation();
	if (!active || !payload || payload.length === 0) return null;

	const getTitle = () => {
		if (type === "hostStatus") return t("{{label}} Hosts", { label });
		if (type === "severity") return t("{{label}} Severity", { label });
		if (type === "scoreRange") return t("Score: {{label}}", { label });
		if (type === "profile") return label;
		if (type === "scanAge") return `${label}`;
		return label;
	};

	return (
		<div className="bg-gray-900 border border-gray-700 rounded-lg px-3 py-2 shadow-lg">
			<p className="text-white font-medium text-sm mb-1">{getTitle()}</p>
			<div className="space-y-1">
				{payload.map((entry) => {
					let name = entry.name;
					let color = entry.color;

					// Format the name for better readability
					if (name === "openscap") {
						name = "OpenSCAP";
						color = "#22c55e";
					} else if (name === "dockerBench") {
						name = "Docker Bench";
						color = "#3b82f6";
					} else if (name === "count") {
						name = t("Scans");
					} else if (name === "host_count") {
						name = t("Hosts");
					}

					return (
						<div
							key={`entry-${entry.name || entry.dataKey}`}
							className="flex items-center justify-between gap-4 text-sm"
						>
							<div className="flex items-center gap-2">
								<div
									className="w-2.5 h-2.5 rounded-sm"
									style={{ backgroundColor: color }}
								/>
								<span className="text-gray-300">{name}</span>
							</div>
							<span className="text-white font-medium">
								{entry.value?.toLocaleString()}
							</span>
						</div>
					);
				})}
			</div>
		</div>
	);
};

const COMPLIANCE_TABS = [
	{ id: "overview", label: tk("Overview"), icon: BarChart3 },
	{ id: "hosts", label: tk("Hosts"), icon: Users },
	{ id: "scan-results", label: tk("Scan Results"), icon: ListChecks },
	{ id: "history", label: tk("History"), icon: History },
	{ id: "settings", label: tk("Settings"), icon: Settings },
];

const Compliance = () => {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const { isDark } = useTheme();
	const toast = useToast();
	const location = useLocation();
	const [searchParams, setSearchParams] = useSearchParams();
	const [activeTab, setActiveTab] = useState(() => {
		const urlTab = searchParams.get("tab");
		if (urlTab && COMPLIANCE_TABS.some((t) => t.id === urlTab)) return urlTab;
		const requested = location.state?.complianceTab;
		if (requested && COMPLIANCE_TABS.some((t) => t.id === requested))
			return requested;
		return "overview";
	});

	const [scanResultsFilters, setScanResultsFilters] = useState(null);
	useTabParam(activeTab);

	const urlTab = searchParams.get("tab");
	useEffect(() => {
		if (urlTab && COMPLIANCE_TABS.some((t) => t.id === urlTab)) {
			setActiveTab(urlTab);
		}
	}, [urlTab]);

	const urlProfileType = searchParams.get("profile_type");
	useEffect(() => {
		if (
			urlProfileType &&
			["openscap", "docker-bench"].includes(urlProfileType)
		) {
			setProfileTypeFilter(urlProfileType);
		}
	}, [urlProfileType]);

	// Handle tab navigation and filters from external links (e.g. host compliance cards)
	const requestedTab = location.state?.complianceTab;
	const requestedFilters = location.state?.scanResultsFilters;
	useEffect(() => {
		if (requestedTab && COMPLIANCE_TABS.some((t) => t.id === requestedTab)) {
			setActiveTab(requestedTab);
			// Must also write the URL, or useTabParam's write of the previous tab wins and the two loop.
			setSearchParams(
				(prev) => {
					const next = new URLSearchParams(prev);
					next.set("tab", requestedTab);
					return next;
				},
				{ replace: true },
			);
		}
		if (requestedFilters) {
			setScanResultsFilters(requestedFilters);
		}
	}, [requestedTab, requestedFilters, setSearchParams]);
	const [showBulkScanModal, setShowBulkScanModal] = useState(false);
	const [selectedHosts, setSelectedHosts] = useState([]);
	const [bulkScanOptions, setBulkScanOptions] = useState({
		profileType: "all",
		enableRemediation: false,
	});
	const [bulkScanResult, setBulkScanResult] = useState(null);
	const [pendingScans, setPendingScans] = useState([]); // Hosts where scan was triggered but not yet in database
	const prevActiveScanIds = useRef(new Map());
	const cancelledHostIds = useRef(new Set());
	const [profileTypeFilter, setProfileTypeFilter] = useState(() => {
		const urlProfileType = searchParams.get("profile_type");
		if (urlProfileType && ["openscap", "docker-bench"].includes(urlProfileType))
			return urlProfileType;
		return "all";
	}); // "all", "openscap", "docker-bench"
	const [tableFilter, setTableFilter] = useState(null); // null = compliance-enabled only, 'never-scanned' = only never-scanned hosts

	// Fetch active/running scans first so we can use it for dashboard refetch rate
	const { data: activeScansData } = useQuery({
		queryKey: ["compliance-active-scans"],
		queryFn: () => complianceAPI.getActiveScans().then((res) => res.data),
		staleTime: 30 * 1000,
		refetchInterval: (query) => {
			const active = query.state?.data?.activeScans?.length > 0;
			return active ? 30000 : 120000; // 30s when scans running, 2 min when idle
		},
	});

	const hasActiveScans = (activeScansData?.activeScans?.length ?? 0) > 0;

	const {
		data: dashboard,
		isLoading,
		error,
	} = useQuery({
		queryKey: ["compliance-dashboard"],
		queryFn: () => complianceAPI.getDashboard().then((res) => res.data),
		staleTime: 60 * 1000, // 60s - avoid refetch on tab switch
		refetchInterval: hasActiveScans ? 30000 : 120000, // 30s when scans running, 2 min when idle
	});

	// Fetch all hosts for bulk scan selection
	const { data: hostsData } = useQuery({
		queryKey: ["hosts-list"],
		queryFn: () => adminHostsAPI.list().then((res) => res.data),
		staleTime: 60000,
		select: (data) => ({ hosts: data.data || [] }),
	});

	// update_interval drives the host picker's reporting dot, the same ×2
	// boundary the Hosts table and HostStatusPills use.
	const { data: complianceSettings } = useQuery({
		queryKey: ["settings", "public"],
		queryFn: () => settingsAPI.getPublic().then((res) => res.data),
		staleTime: 5 * 60 * 1000,
	});
	const updateIntervalMinutes = (() => {
		const parsed = Number.parseInt(complianceSettings?.update_interval, 10);
		return Number.isFinite(parsed) && parsed > 0 ? parsed : 60;
	})();

	// Track active scans and notify when they complete
	useEffect(() => {
		const activeScans = activeScansData?.activeScans || [];
		const current = new Map(activeScans.map((s) => [s.id, s.hostId]));
		const activeHostIds = new Set(activeScans.map((s) => s.hostId));

		let finished = false;
		let completed = false;
		for (const [prevId, prevHostId] of prevActiveScanIds.current) {
			if (current.has(prevId)) continue;
			finished = true;
			if (!cancelledHostIds.current.has(prevHostId)) completed = true;
		}
		if (finished) {
			queryClient.invalidateQueries({ queryKey: ["compliance-dashboard"] });
			if (completed) toast.success(t("Compliance scan completed"));
		}
		for (const hostId of cancelledHostIds.current) {
			if (!activeHostIds.has(hostId)) cancelledHostIds.current.delete(hostId);
		}

		// Remove pending scans that now appear as real active scans
		setPendingScans((prev) => prev.filter((p) => !activeHostIds.has(p.hostId)));

		prevActiveScanIds.current = current;
	}, [activeScansData, queryClient, toast, t]);

	// Check if pending scans have completed (scan records appear in dashboard)
	useEffect(() => {
		if (pendingScans.length === 0 || !dashboard?.recent_scans) return;

		const recentScans = dashboard.recent_scans || [];
		const completedPending = [];

		for (const pending of pendingScans) {
			const pendingStart = new Date(pending.startedAt).getTime();
			const hasCompletedScan = recentScans.some((scan) => {
				const scanComplete = new Date(scan.completed_at).getTime();
				return scan.host_id === pending.hostId && scanComplete > pendingStart;
			});
			if (hasCompletedScan) {
				completedPending.push(pending.hostId);
			}
		}

		if (completedPending.length > 0) {
			setPendingScans((prev) =>
				prev.filter((p) => !completedPending.includes(p.hostId)),
			);
			if (completedPending.length === 1) {
				const completed = pendingScans.find(
					(p) => p.hostId === completedPending[0],
				);
				toast.success(
					completed?.hostName
						? t("Scan completed for {{name}}", { name: completed.hostName })
						: t("Scan completed for host"),
				);
			} else {
				// Always 2 or more here, so no plural forms
				toast.success(
					t("{{count}} scans completed", { count: completedPending.length }),
				);
			}
		}
	}, [dashboard?.recent_scans, pendingScans, toast, t]);

	// Clear stale pending scans after 60 seconds
	useEffect(() => {
		if (pendingScans.length === 0) return;

		const interval = setInterval(() => {
			const now = Date.now();
			setPendingScans((prev) =>
				prev.filter((p) => {
					const age = now - new Date(p.startedAt).getTime();
					return age < 60000; // Remove after 60 seconds
				}),
			);
		}, 5000); // Check every 5 seconds

		return () => clearInterval(interval);
	}, [pendingScans.length]);

	// Bulk scan mutation
	const bulkScanMutation = useMutation({
		mutationFn: (data) =>
			complianceAPI.triggerBulkScan(data.hostIds, data.options),
		onSuccess: (response) => {
			setBulkScanResult(response.data);
			queryClient.invalidateQueries({ queryKey: ["compliance-active-scans"] });
			const { success, failed } = response.data.summary || {};

			// Track triggered hosts as pending scans for immediate UI feedback
			for (const t of response.data.triggered || []) {
				cancelledHostIds.current.delete(t.hostId);
			}
			if (response.data.triggered?.length > 0) {
				const newPending = response.data.triggered.map((t) => ({
					id: `pending-${t.hostId}`,
					hostId: t.hostId,
					hostName: t.hostName,
					profileType: response.data.profile_type,
					startedAt: new Date().toISOString(),
					isPending: true,
					connected: true,
				}));
				setPendingScans((prev) => [...prev, ...newPending]);
			}

			if (failed === 0) {
				toast.success(
					t("Started {{count}} compliance scan(s)", { count: success }),
				);
				// Auto-close modal after 3 seconds if all succeeded
				setTimeout(() => {
					setShowBulkScanModal(false);
					setBulkScanResult(null);
					setSelectedHosts([]);
				}, 3000);
			} else {
				toast.warning(
					t("Started {{count}} scan(s), {{failed}} failed", {
						count: success,
						failed,
					}),
				);
			}
		},
		onError: (error) => {
			const errorMsg = error.response?.data?.error || error.message;
			toast.error(t("Bulk scan failed: {{error}}", { error: errorMsg }));
		},
	});

	// Single-host scan trigger (for RUN column in hosts table)
	const triggerSingleScanMutation = useMutation({
		mutationFn: ({ hostId }) =>
			complianceAPI.triggerScan(hostId, { profile_type: "all" }),
		onSuccess: (_, { hostId, hostName }) => {
			cancelledHostIds.current.delete(hostId);
			queryClient.invalidateQueries({ queryKey: ["compliance-active-scans"] });
			setPendingScans((prev) => [
				...prev,
				{
					id: `pending-${hostId}`,
					hostId,
					hostName: hostName || t("Host"),
					profileType: "all",
					startedAt: new Date().toISOString(),
					isPending: true,
					connected: true,
				},
			]);
			toast.success(
				hostName
					? t("Scan started for {{name}}", { name: hostName })
					: t("Scan started for host"),
			);
		},
		onError: (error, { hostName }) => {
			const errorMsg = error.response?.data?.error || error.message;
			toast.error(
				hostName
					? t("Scan failed for {{name}}: {{error}}", {
							name: hostName,
							error: errorMsg,
						})
					: t("Scan failed for host: {{error}}", { error: errorMsg }),
			);
		},
	});

	const cancelScanMutation = useMutation({
		mutationFn: ({ hostId }) => complianceAPI.cancelScan(hostId),
		onSuccess: (response, { hostId, hostName }) => {
			if (response?.data?.agentNotified === false) {
				toast.warning(
					hostName
						? t(
								"Scan cancelled for {{name}}, but the agent could not be reached and may still finish it",
								{ name: hostName },
							)
						: t(
								"Scan cancelled for host, but the agent could not be reached and may still finish it",
							),
				);
			} else {
				toast.success(
					hostName
						? t("Scan cancelled for {{name}}", { name: hostName })
						: t("Scan cancelled for host"),
				);
			}
			const shownActive = [...prevActiveScanIds.current.values()].includes(
				hostId,
			);
			if (response?.data?.scansCancelled > 0 || shownActive) {
				cancelledHostIds.current.add(hostId);
			}
			setPendingScans((prev) => prev.filter((p) => p.hostId !== hostId));
			queryClient.invalidateQueries({ queryKey: ["compliance-active-scans"] });
			queryClient.invalidateQueries({ queryKey: ["compliance-dashboard"] });
			queryClient.invalidateQueries({ queryKey: ["compliance-scan-history"] });
		},
		onError: (error, { hostName }) => {
			const errorMsg = error.response?.data?.error || error.message;
			toast.error(
				hostName
					? t("Failed to cancel scan for {{name}}: {{error}}", {
							name: hostName,
							error: errorMsg,
						})
					: t("Failed to cancel scan for host: {{error}}", {
							error: errorMsg,
						}),
			);
		},
	});

	if (isLoading) {
		return (
			<div className="flex items-center justify-center h-64">
				<div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary-500"></div>
			</div>
		);
	}

	if (error) {
		return (
			<div className="p-4 bg-red-900/50 border border-red-700 rounded-lg">
				<p className="text-red-200">
					{t("Failed to load compliance dashboard")}
				</p>
			</div>
		);
	}

	const {
		summary,
		recent_scans,
		hosts_with_latest_scan,
		worst_hosts,
		top_failing_rules,
		top_warning_rules,
		_profile_distribution,
		_severity_breakdown,
		severity_by_profile_type,
		docker_bench_by_section,
		scan_age_distribution,
		profile_type_stats,
	} = dashboard || {};

	const allHostsTableRows = hosts_with_latest_scan || [];
	// Rows expose which scanners a host has enabled, not the profile type of its
	// latest scan, so the filter matches on scanner enablement. Note
	// docker_enabled is the Docker integration and is not the same thing as
	// Docker Bench scanning.
	const matchesProfileType = (row) => {
		if (profileTypeFilter === "openscap")
			return row.compliance_openscap_enabled;
		if (profileTypeFilter === "docker-bench")
			return row.compliance_docker_bench_enabled;
		return true;
	};
	const hostsTableRows = (
		tableFilter === "never-scanned"
			? allHostsTableRows.filter((row) => row.last_scan_date == null)
			: allHostsTableRows.filter(
					(row) =>
						row.compliance_enabled || row.compliance_docker_bench_enabled,
				)
	).filter(matchesProfileType);

	// Combine real active scans with pending scans for display
	const realActiveScans = activeScansData?.activeScans || [];
	const activeScans = [...pendingScans, ...realActiveScans];

	// Get stats for the selected profile type
	const openscapStats = profile_type_stats?.find((p) => p.type === "openscap");
	const dockerBenchStats = profile_type_stats?.find(
		(p) => p.type === "docker-bench",
	);

	// Calculate filtered summary based on selected tab
	const getFilteredSummary = () => {
		if (profileTypeFilter === "all") {
			return summary;
		}
		const stats =
			profileTypeFilter === "openscap" ? openscapStats : dockerBenchStats;
		if (!stats) return null;

		// Calculate pass rate
		const passRate =
			stats.total_rules > 0
				? (stats.total_passed / stats.total_rules) * 100
				: 0;

		return {
			total_hosts: stats.hosts_scanned || 0,
			average_score: stats.average_score || 0,
			total_rules: stats.total_rules || 0,
			total_passed_rules: stats.total_passed || 0,
			total_failed_rules: stats.total_failed || 0,
			total_warnings: stats.total_warnings || 0,
			pass_rate: passRate,
		};
	};
	const filteredSummary = getFilteredSummary();

	// Filter data by profile type
	const filteredScans =
		recent_scans?.filter((scan) => {
			if (profileTypeFilter === "all") return true;
			return scan.profile?.type === profileTypeFilter;
		}) || [];

	const _filteredWorstHosts =
		worst_hosts?.filter((host) => {
			if (profileTypeFilter === "all") return true;
			return host.compliance_profiles?.type === profileTypeFilter;
		}) || [];

	const _filteredTopFailingRules =
		top_failing_rules?.filter((rule) => {
			if (profileTypeFilter === "all") return true;
			return rule.profile_type === profileTypeFilter;
		}) || [];

	const _filteredTopWarningRules =
		top_warning_rules?.filter((rule) => {
			if (profileTypeFilter === "all") return true;
			return rule.profile_type === profileTypeFilter;
		}) || [];

	// Get display name for current filter
	const getFilterDisplayName = () => {
		if (profileTypeFilter === "openscap") return "OpenSCAP";
		if (profileTypeFilter === "docker-bench") return "Docker Bench";
		return t("All Scans");
	};

	// scanner_status is an English value from the API; show a label for it
	const scannerStatusLabel = (status) => {
		if (status === "Scanned") return t("Scanned");
		if (status === "Enabled") return t("Enabled");
		if (status === "Never scanned")
			return t("Never scanned", { context: "host" });
		return status;
	};

	const clearProfileTypeFilter = () => {
		setProfileTypeFilter("all");
		const params = new URLSearchParams(searchParams);
		params.delete("profile_type");
		setSearchParams(params, { replace: true });
	};

	const openHostScanResults = (row, status) => {
		const bothScanners =
			row.compliance_openscap_enabled && row.compliance_docker_bench_enabled;
		setScanResultsFilters({
			status,
			host_id: row.host_id,
			profile_type: bothScanners ? row.score_scan_type || "" : "",
		});
		setActiveTab("scan-results");
	};

	const allHosts = hostsData?.hosts || [];

	const handleToggleHost = (hostId) => {
		setSelectedHosts((prev) =>
			prev.includes(hostId)
				? prev.filter((id) => id !== hostId)
				: [...prev, hostId],
		);
	};

	const handleSelectAll = () => {
		if (selectedHosts.length === allHosts.length) {
			setSelectedHosts([]);
		} else {
			setSelectedHosts(allHosts.map((h) => h.id));
		}
	};

	const handleBulkScan = () => {
		if (selectedHosts.length === 0) return;
		bulkScanMutation.mutate({
			hostIds: selectedHosts,
			options: bulkScanOptions,
		});
	};

	const _openBulkScanModal = () => {
		setBulkScanResult(null);
		setShowBulkScanModal(true);
	};

	return (
		<div className="space-y-6">
			<div>
				<h1 className="text-2xl font-semibold text-secondary-900 dark:text-white">
					{t("Security Compliance")}
				</h1>
				<p className="text-sm text-secondary-600 dark:text-white mt-1">
					{t("Monitor and manage compliance across your hosts")}
				</p>
			</div>

			<div className="grid grid-cols-2 lg:grid-cols-5 gap-4">
				<div className="card p-4 cursor-default text-left w-full">
					<div className="flex items-center">
						<Server className="h-5 w-5 text-primary-600 mr-2" />
						<div>
							<p className="text-sm text-secondary-500 dark:text-white">
								{t("Total hosts")}
							</p>
							<p className="text-xl font-semibold text-secondary-900 dark:text-white">
								{(summary?.total_hosts ?? 0) + (summary?.unscanned ?? 0) || 0}
							</p>
						</div>
					</div>
				</div>
				<div className="card p-4 cursor-default text-left w-full">
					<div className="flex items-center">
						<ShieldCheck className="h-5 w-5 text-green-600 mr-2" />
						<div>
							<p className="text-sm text-secondary-500 dark:text-white">
								{t("Compliant")}
							</p>
							<p className="text-xl font-semibold text-secondary-900 dark:text-white">
								{summary?.hosts_compliant ?? 0}
							</p>
						</div>
					</div>
				</div>
				<div className="card p-4 cursor-default text-left w-full">
					<div className="flex items-center">
						<AlertTriangle className="h-5 w-5 text-yellow-600 mr-2" />
						<div>
							<p className="text-sm text-secondary-500 dark:text-white">
								{t("Warning")}
							</p>
							<p className="text-xl font-semibold text-secondary-900 dark:text-white">
								{summary?.hosts_warning ?? 0}
							</p>
						</div>
					</div>
				</div>
				<div className="card p-4 cursor-default text-left w-full">
					<div className="flex items-center">
						<ShieldAlert className="h-5 w-5 text-red-600 mr-2" />
						<div>
							<p className="text-sm text-secondary-500 dark:text-white">
								{t("Critical")}
							</p>
							<p className="text-xl font-semibold text-secondary-900 dark:text-white">
								{summary?.hosts_critical ?? 0}
							</p>
						</div>
					</div>
				</div>
				<button
					type="button"
					onClick={() => {
						setTableFilter((prev) =>
							prev === "never-scanned" ? null : "never-scanned",
						);
						setActiveTab("hosts");
					}}
					className={`card p-4 text-left w-full transition-colors ${
						tableFilter === "never-scanned"
							? "ring-2 ring-primary-500 dark:ring-primary-400 bg-primary-50 dark:bg-primary-900/20"
							: "cursor-pointer hover:bg-secondary-50 dark:hover:bg-secondary-700/50"
					}`}
				>
					<div className="flex items-center">
						<ShieldOff className="h-5 w-5 text-secondary-600 mr-2" />
						<div>
							<p className="text-sm text-secondary-500 dark:text-white">
								{t("Never scanned")}
							</p>
							<p className="text-xl font-semibold text-secondary-900 dark:text-white">
								{summary?.unscanned ?? 0}
							</p>
							{tableFilter === "never-scanned" && (
								<p className="text-xs text-primary-600 dark:text-primary-400 mt-1">
									{t("Filtered in Hosts tab")}
								</p>
							)}
						</div>
					</div>
				</button>
			</div>

			{/* Tab Navigation - same pattern as Docker page */}
			<div className="border-b border-secondary-200 dark:border-secondary-600 overflow-x-auto scrollbar-hide">
				<nav
					className="-mb-px flex space-x-4 sm:space-x-8 px-4"
					aria-label={t("Tabs")}
				>
					{COMPLIANCE_TABS.map((tab) => {
						const Icon = tab.icon;
						return (
							<button
								key={tab.id}
								type="button"
								onClick={() => setActiveTab(tab.id)}
								className={`${
									activeTab === tab.id
										? "border-primary-500 text-primary-600 dark:text-primary-400"
										: "border-transparent text-secondary-500 hover:text-secondary-700 hover:border-secondary-300 dark:text-white dark:hover:text-primary-400"
								} whitespace-nowrap py-4 px-1 border-b-2 font-medium text-sm flex items-center`}
							>
								<Icon className="h-4 w-4 mr-2" />
								{t(tab.label)}
							</button>
						);
					})}
				</nav>
			</div>

			{/* ==================== HOSTS TAB ==================== */}
			{activeTab === "hosts" && (
				<>
					{/* Hosts table - full width; last activity per row */}
					<div className="card p-4 md:p-6">
						{tableFilter === "never-scanned" && (
							<p className="text-sm text-primary-600 dark:text-primary-400 mb-4">
								{t(
									"Showing never-scanned hosts only. Click the Never scanned card again to clear.",
								)}
							</p>
						)}
						{profileTypeFilter !== "all" && (
							<div className="flex flex-wrap items-center gap-2 mb-4">
								<p className="text-sm text-primary-600 dark:text-primary-400">
									{t("Showing hosts with {{name}} scanning enabled.", {
										name: getFilterDisplayName(),
									})}
								</p>
								<button
									type="button"
									onClick={clearProfileTypeFilter}
									className="inline-flex items-center gap-1 min-h-[44px] px-2 text-sm font-medium text-primary-600 dark:text-primary-400 hover:text-primary-700 dark:hover:text-primary-300"
								>
									<X className="h-3.5 w-3.5" />
									{t("Clear filter")}
								</button>
							</div>
						)}
						<div className="overflow-x-auto">
							<table className="min-w-full divide-y divide-secondary-200 dark:divide-secondary-600">
								<thead className="bg-secondary-50 dark:bg-secondary-700">
									<tr>
										<th className="px-4 py-2 text-left text-xs font-medium text-secondary-500 dark:text-white whitespace-nowrap w-16">
											{t("Run")}
										</th>
										<th className="px-4 py-2 text-left text-xs font-medium text-secondary-500 dark:text-white">
											{t("Host name")}
										</th>
										<th
											className="px-4 py-2 text-left text-xs font-medium text-secondary-500 dark:text-white w-12"
											title={t("Compliance status")}
										>
											{t("Status")}
										</th>
										<th className="px-4 py-2 text-left text-xs font-medium text-secondary-500 dark:text-white">
											{t("Last activity")}
										</th>
										<th className="px-4 py-2 text-right text-xs font-medium text-secondary-500 dark:text-white">
											{t("Passed")}
										</th>
										<th className="px-4 py-2 text-right text-xs font-medium text-secondary-500 dark:text-white">
											{t("Failed")}
										</th>
										<th className="px-4 py-2 text-right text-xs font-medium text-secondary-500 dark:text-white">
											{t("Skipped")}
										</th>
										<th className="px-4 py-2 text-left text-xs font-medium text-secondary-500 dark:text-white">
											{t("Scanner status")}
										</th>
										<th className="px-4 py-2 text-left text-xs font-medium text-secondary-500 dark:text-white">
											{t("Mode")}
										</th>
										<th className="px-4 py-2 text-left text-xs font-medium text-secondary-500 dark:text-white">
											{t("Scanners")}
										</th>
									</tr>
								</thead>
								<tbody className="bg-white dark:bg-secondary-800 divide-y divide-secondary-200 dark:divide-secondary-600 text-sm">
									{hostsTableRows.length === 0 ? (
										<tr>
											<td
												colSpan={10}
												className="px-4 py-8 text-center text-secondary-500 dark:text-white"
											>
												{t("No hosts")}
											</td>
										</tr>
									) : (
										hostsTableRows.map((row) => {
											const active_scan = activeScans.find(
												(s) => s.hostId === row.host_id,
											);
											const is_scanning =
												!!active_scan ||
												(triggerSingleScanMutation.isPending &&
													triggerSingleScanMutation.variables?.hostId ===
														row.host_id);
											const is_cancelling =
												cancelScanMutation.isPending &&
												cancelScanMutation.variables?.hostId === row.host_id;
											return (
												<tr
													key={row.host_id}
													className={
														is_scanning
															? "bg-blue-950/30 dark:bg-blue-950/30 hover:bg-blue-950/40 dark:hover:bg-blue-950/40"
															: "hover:bg-secondary-50 dark:hover:bg-secondary-700"
													}
												>
													<td className="px-4 py-2 whitespace-nowrap">
														{is_scanning ? (
															<button
																type="button"
																onClick={() =>
																	cancelScanMutation.mutate({
																		hostId: row.host_id,
																		hostName:
																			row.friendly_name ||
																			row.hostname ||
																			t("Host"),
																	})
																}
																disabled={is_cancelling}
																className="inline-flex items-center justify-center w-6 h-6 border border-transparent rounded text-white bg-red-600 hover:bg-red-700 focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-red-500 transition-colors duration-200 disabled:opacity-50 disabled:cursor-not-allowed"
																title={
																	is_cancelling
																		? t("Cancelling...")
																		: t("Cancel running scan")
																}
															>
																{is_cancelling ? (
																	<RefreshCw className="h-3 w-3 animate-spin" />
																) : (
																	<StopCircle className="h-3.5 w-3.5" />
																)}
															</button>
														) : (
															<button
																type="button"
																onClick={() =>
																	triggerSingleScanMutation.mutate({
																		hostId: row.host_id,
																		hostName:
																			row.friendly_name ||
																			row.hostname ||
																			t("Host"),
																	})
																}
																disabled={triggerSingleScanMutation.isPending}
																className="inline-flex items-center justify-center w-6 h-6 border border-transparent rounded text-white bg-green-600 hover:bg-green-700 focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-green-500 transition-colors duration-200 disabled:opacity-50 disabled:cursor-not-allowed"
																title={t("Run compliance scan")}
															>
																<Play className="h-3 w-3" />
															</button>
														)}
													</td>
													<td className="px-4 py-2 whitespace-nowrap">
														<Link
															to={`/compliance/hosts/${row.host_id}`}
															className="text-secondary-900 dark:text-white hover:text-primary-600 dark:hover:text-primary-400 hover:underline font-medium"
														>
															{row.friendly_name || row.hostname || " -"}
														</Link>
													</td>
													<td
														className="px-4 py-2 whitespace-nowrap"
														title={
															row.score != null
																? `${{ compliant: t("Compliant", { context: "host" }), warning: t("Warning"), critical: t("Critical") }[hostComplianceStatus(row)]}${row.score_profile_name ? ` · ${row.score_profile_name}` : ""} (${Math.round(row.score)}%)`
																: undefined
														}
													>
														{row.score != null ? (
															hostComplianceStatus(row) === "compliant" ? (
																<ShieldCheck
																	className="h-5 w-5 text-green-600 dark:text-green-400"
																	title={t("Compliant", { context: "host" })}
																/>
															) : hostComplianceStatus(row) === "warning" ? (
																<AlertTriangle
																	className="h-5 w-5 text-yellow-600 dark:text-yellow-400"
																	title={t("Warning")}
																/>
															) : (
																<ShieldAlert
																	className="h-5 w-5 text-red-600 dark:text-red-400"
																	title={t("Critical")}
																/>
															)
														) : (
															<ShieldOff
																className="h-5 w-5 text-secondary-400 dark:text-white"
																title={t("Not scanned")}
															/>
														)}
													</td>
													<td
														className="px-4 py-2 whitespace-nowrap text-secondary-700 dark:text-white"
														title={
															is_scanning
																? active_scan?.startedAt
																	? t("Scan running · started {{time}}", {
																			time: formatDistanceToNow(
																				new Date(active_scan.startedAt),
																				{
																					addSuffix: true,
																					locale: dateFnsLocale(),
																				},
																			),
																		})
																	: t("Scan running")
																: row.last_scan_date
																	? `${row.last_activity_title || t("Scan")} · ${formatDistanceToNow(new Date(row.last_scan_date), { addSuffix: true, locale: dateFnsLocale() })}`
																	: undefined
														}
													>
														{is_scanning ? (
															<span className="inline-flex items-center gap-1.5 text-blue-400">
																<RefreshCw className="h-3 w-3 animate-spin" />
																<span className="text-xs font-medium">
																	{active_scan?.isPending
																		? t("Starting...")
																		: active_scan?.profileType ===
																				"docker-bench"
																			? "Docker Bench"
																			: active_scan?.profileType === "openscap"
																				? "OpenSCAP"
																				: t("Scanning...")}
																</span>
															</span>
														) : (
															row.last_activity_title ||
															(row.last_scan_date ? t("Scan") : " -")
														)}
													</td>
													<td className="px-4 py-2 text-right whitespace-nowrap">
														{row.passed != null ? (
															<button
																type="button"
																onClick={() => openHostScanResults(row, "pass")}
																className="text-green-600 dark:text-green-400 hover:underline font-medium tabular-nums"
																title={t("View passing rules for this host")}
															>
																{row.passed}
															</button>
														) : (
															" -"
														)}
													</td>
													<td className="px-4 py-2 text-right whitespace-nowrap">
														{row.failed != null ? (
															<button
																type="button"
																onClick={() => openHostScanResults(row, "fail")}
																className="text-red-600 dark:text-red-400 hover:underline font-medium tabular-nums"
																title={t("View failing rules for this host")}
															>
																{row.failed}
															</button>
														) : (
															" -"
														)}
													</td>
													<td className="px-4 py-2 text-right whitespace-nowrap">
														{row.skipped != null ? (
															<button
																type="button"
																onClick={() =>
																	openHostScanResults(row, "skipped")
																}
																className="text-secondary-600 dark:text-white hover:underline font-medium tabular-nums"
																title={t(
																	"View skipped/N/A rules for this host",
																)}
															>
																{row.skipped}
															</button>
														) : (
															" -"
														)}
													</td>
													<td className="px-4 py-2 whitespace-nowrap">
														<span
															className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${
																row.scanner_status === "Scanned"
																	? "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400"
																	: row.scanner_status === "Enabled"
																		? "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400"
																		: "bg-secondary-100 text-secondary-700 dark:bg-secondary-700 dark:text-white"
															}`}
														>
															{scannerStatusLabel(row.scanner_status)}
														</span>
													</td>
													<td className="px-4 py-2 whitespace-nowrap">
														{row.compliance_mode === "disabled" ? (
															<span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-secondary-100 text-secondary-600 dark:bg-secondary-700 dark:text-white">
																{t("Disabled")}
															</span>
														) : (
															<span
																className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${
																	row.compliance_mode === "on-demand"
																		? "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-400"
																		: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400"
																}`}
															>
																{row.compliance_mode === "on-demand"
																	? t("On-demand")
																	: t("Scheduled")}
															</span>
														)}
													</td>
													<td className="px-4 py-2 whitespace-nowrap text-secondary-700 dark:text-white">
														{row.compliance_enabled && row.docker_enabled
															? "OpenSCAP, Docker"
															: row.compliance_enabled
																? "OpenSCAP"
																: row.docker_enabled
																	? "Docker"
																	: " -"}
													</td>
												</tr>
											);
										})
									)}
								</tbody>
							</table>
						</div>
					</div>
				</>
			)}

			{/* Bulk Scan Modal */}
			{showBulkScanModal && (
				<ModalPortal>
					<div className="fixed inset-0 bg-black/50 flex items-center justify-center z-[120]">
						<div className="bg-secondary-800 rounded-lg border border-secondary-700 w-full max-w-2xl max-h-[80vh] overflow-hidden">
							{/* Modal Header */}
							<div className="flex items-center justify-between p-4 border-b border-secondary-700">
								<h2 className="text-lg font-semibold text-white">
									{t("Bulk Compliance Scan")}
								</h2>
								<button
									onClick={() => {
										setShowBulkScanModal(false);
										setBulkScanResult(null);
									}}
									className="text-white hover:text-white"
								>
									<X className="h-5 w-5" />
								</button>
							</div>

							{/* Modal Body */}
							<div className="p-4 space-y-4 max-h-[60vh] overflow-y-auto">
								{/* Scan Options */}
								<div className="space-y-3">
									<h3 className="text-sm font-medium text-white">
										{t("Scan Options")}
									</h3>
									<div className="flex flex-wrap gap-4">
										<div className="flex-1 min-w-[200px]">
											<label className="block text-xs text-white mb-1">
												{t("Profile Type")}
											</label>
											<select
												value={bulkScanOptions.profileType}
												onChange={(e) =>
													setBulkScanOptions((prev) => ({
														...prev,
														profileType: e.target.value,
													}))
												}
												className="w-full px-3 py-2 bg-secondary-700 border border-secondary-600 rounded-lg text-white text-sm"
											>
												<option value="all">{t("All Profiles")}</option>
												<option value="openscap">{t("OpenSCAP Only")}</option>
												<option value="docker-bench">
													{t("Docker Bench Only")}
												</option>
											</select>
										</div>
										<div className="flex items-center gap-2">
											<input
												type="checkbox"
												id="enableRemediation"
												checked={bulkScanOptions.enableRemediation}
												onChange={(e) =>
													setBulkScanOptions((prev) => ({
														...prev,
														enableRemediation: e.target.checked,
													}))
												}
												className="w-4 h-4 rounded bg-secondary-700 border-secondary-600"
											/>
											<label
												htmlFor="enableRemediation"
												className="text-sm text-white"
											>
												{t("Enable Remediation")}
											</label>
										</div>
									</div>
								</div>

								{/* Host Selection */}
								<div className="space-y-3">
									<div className="flex items-center justify-between">
										<h3 className="text-sm font-medium text-white">
											{t("Select Hosts ({{selected}} of {{total}})", {
												selected: selectedHosts.length,
												total: allHosts.length,
											})}
										</h3>
										<button
											onClick={handleSelectAll}
											className="text-xs text-primary-400 hover:text-primary-300"
										>
											{selectedHosts.length === allHosts.length
												? t("Deselect All")
												: t("Select All")}
										</button>
									</div>
									<div className="grid grid-cols-1 md:grid-cols-2 gap-2 max-h-[200px] overflow-y-auto">
										{allHosts.map((host) => {
											const reportingState = hasNeverReported(host)
												? "awaiting"
												: deriveReportingStateByTime(
														host.last_update,
														updateIntervalMinutes,
													);
											const dotVariant =
												reportingState === "awaiting"
													? {
															className: "bg-secondary-400",
															label: t("Awaiting first report"),
														}
													: reportingState === "reporting"
														? {
																className: "bg-success-500",
																label: t("Reporting", { context: "status" }),
															}
														: reportingState === "overdue"
															? {
																	className: "bg-warning-500",
																	label: t("Overdue"),
																}
															: {
																	className: "bg-danger-500",
																	label: t("Not reporting"),
																};
											return (
												<label
													key={host.id}
													className={`flex items-center gap-3 p-2 rounded-lg cursor-pointer transition-colors ${
														selectedHosts.includes(host.id)
															? "bg-primary-900/30 border border-primary-700"
															: "bg-secondary-700/50 border border-secondary-600 hover:border-secondary-500"
													}`}
												>
													<input
														type="checkbox"
														checked={selectedHosts.includes(host.id)}
														onChange={() => handleToggleHost(host.id)}
														className="w-4 h-4 rounded bg-secondary-700 border-secondary-600"
													/>
													<div className="flex-1 min-w-0">
														<p className="text-sm font-medium text-white truncate">
															{host.friendly_name || host.hostname}
														</p>
														<p className="text-xs text-white truncate">
															{host.hostname}
														</p>
													</div>
													<div
														className={`w-2 h-2 rounded-full flex-shrink-0 ${dotVariant.className}`}
														title={dotVariant.label}
													/>
													<span className="sr-only">{dotVariant.label}</span>
												</label>
											);
										})}
									</div>
								</div>

								{/* Results */}
								{bulkScanResult && (
									<div
										className={`p-3 rounded-lg ${bulkScanResult.summary?.failed > 0 ? "bg-yellow-900/30 border border-yellow-700" : "bg-green-900/30 border border-green-700"}`}
									>
										<p
											className={`text-sm font-medium ${bulkScanResult.summary?.failed > 0 ? "text-yellow-300" : "text-green-300"}`}
										>
											{bulkScanResult.message}
										</p>
										{bulkScanResult.failed?.length > 0 && (
											<div className="mt-2 text-xs text-yellow-400">
												<p>{t("Failed hosts:")}</p>
												<ul className="list-disc list-inside">
													{bulkScanResult.failed.map((f) => (
														<li key={`failed-${f.hostName}`}>
															{f.hostName}: {f.error}
														</li>
													))}
												</ul>
											</div>
										)}
									</div>
								)}
							</div>

							{/* Modal Footer */}
							<div className="flex items-center justify-end gap-3 p-4 border-t border-secondary-700">
								<button
									onClick={() => {
										setShowBulkScanModal(false);
										setBulkScanResult(null);
									}}
									className="px-4 py-2 text-white hover:text-white transition-colors"
								>
									{t("Cancel")}
								</button>
								<button
									onClick={handleBulkScan}
									disabled={
										selectedHosts.length === 0 || bulkScanMutation.isPending
									}
									className="flex items-center gap-2 px-4 py-2 bg-primary-600 hover:bg-primary-700 disabled:bg-secondary-600 disabled:cursor-not-allowed text-white rounded-lg font-medium transition-colors"
								>
									{bulkScanMutation.isPending ? (
										<>
											<RefreshCw className="h-4 w-4 animate-spin" />
											{t("Triggering...")}
										</>
									) : (
										<>
											<Play className="h-4 w-4" />
											{t("Scan {{count}} Host", {
												count: selectedHosts.length,
											})}
										</>
									)}
								</button>
							</div>
						</div>
					</div>
				</ModalPortal>
			)}

			{/* ==================== OVERVIEW TAB ==================== */}
			{activeTab === "overview" && (
				<>
					{/* Compliance dashboard widgets, mirroring the main Dashboard */}
					<div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 sm:gap-6 mb-6">
						<FailuresBySeverityDoughnut
							data={dashboard}
							onTabChange={(tab, filters) => {
								setActiveTab(tab);
								setScanResultsFilters(filters ?? null);
							}}
						/>
						<OpenSCAPDistributionDoughnut data={dashboard} />
						<ComplianceProfilesPie
							data={dashboard}
							onTabChange={(tab) => {
								setActiveTab(tab);
								setScanResultsFilters(null);
							}}
						/>
						<LastScanAgeBar
							data={dashboard}
							onTabChange={(tab) => setActiveTab(tab)}
						/>
						<HostComplianceStatusBar
							data={dashboard}
							onTabChange={(tab) => setActiveTab(tab)}
						/>
					</div>

					{/* Active Scans Section - Only show if there are running scans */}
					{activeScans.length > 0 && (
						<div className="card p-4 border-blue-700/50 bg-blue-900/20">
							<div className="flex items-center gap-2 mb-3">
								<RefreshCw className="h-5 w-5 text-blue-400 animate-spin" />
								<h2 className="text-lg font-semibold text-blue-300">
									{t("Scans in Progress ({{count}})", {
										count: activeScans.length,
									})}
								</h2>
							</div>
							<div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
								{activeScans.map((scan) => (
									<Link
										key={scan.id}
										to={`/compliance/hosts/${scan.hostId}`}
										className={`rounded-lg p-3 border transition-colors ${
											scan.isPending
												? "bg-yellow-900/30 border-yellow-700/50 hover:border-yellow-500"
												: scan.profileType === "docker-bench"
													? "bg-blue-900/20 border-blue-700/50 hover:border-blue-500"
													: "bg-green-900/20 border-green-700/50 hover:border-green-500"
										}`}
									>
										<div className="flex items-center justify-between mb-2">
											<span className="font-medium text-white truncate">
												{scan.hostName}
											</span>
											{scan.isPending ? (
												<Clock
													className="h-4 w-4 text-yellow-400 animate-pulse"
													title={t("Triggering...")}
												/>
											) : scan.connected ? (
												<Wifi
													className="h-4 w-4 text-green-400"
													title={t("Connected")}
												/>
											) : (
												<WifiOff
													className="h-4 w-4 text-red-400"
													title={t("Disconnected")}
												/>
											)}
										</div>
										<div className="flex items-center gap-2 text-sm text-white">
											<span
												className={`px-2 py-0.5 rounded text-xs ${
													scan.isPending
														? "bg-yellow-900/50 text-yellow-300"
														: scan.profileType === "docker-bench"
															? "bg-blue-900/50 text-blue-300"
															: "bg-green-900/50 text-green-300"
												}`}
											>
												{scan.isPending
													? t("Triggering...")
													: scan.profileType === "docker-bench"
														? "Docker Bench"
														: scan.profileType === "openscap"
															? "OpenSCAP"
															: scan.profileType || t("Scanning...")}
											</span>
											<span className="text-xs">
												{t("Started {{time}}", {
													time: formatDistanceToNow(new Date(scan.startedAt), {
														addSuffix: true,
														locale: dateFnsLocale(),
													}),
												})}
											</span>
										</div>
									</Link>
								))}
							</div>
						</div>
					)}

					{/* ==================== SPECIFIC SCAN TYPE STATS (OpenSCAP or Docker Bench tabs) ==================== */}
					{profileTypeFilter !== "all" && filteredSummary && (
						<>
							<div className="flex items-center gap-3 pt-2">
								<div className="flex items-center gap-2">
									{profileTypeFilter === "openscap" ? (
										<Server className="h-5 w-5 text-green-400" />
									) : (
										<Container className="h-5 w-5 text-blue-400" />
									)}
									<h2 className="text-lg font-semibold text-white">
										{t("{{name}} Statistics", { name: getFilterDisplayName() })}
									</h2>
								</div>
								<div className="flex-1 h-px bg-secondary-700" />
							</div>

							{/* Compact Stats Card for specific scan type */}
							<div
								className={`card p-4 sm:p-5 border-2 ${
									profileTypeFilter === "openscap"
										? "border-green-700/50"
										: "border-blue-700/50"
								}`}
							>
								<div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-6 gap-6">
									<div className="text-center">
										<p className="text-xs text-white uppercase tracking-wide mb-1 min-h-[2.5em] flex items-end justify-center">
											{t("Hosts")}
										</p>
										<p className="text-3xl font-bold text-white">
											{filteredSummary.total_hosts || 0}
										</p>
									</div>
									<div className="text-center">
										<p className="text-xs text-white uppercase tracking-wide mb-1 min-h-[2.5em] flex items-end justify-center">
											{t("Avg Score")}
										</p>
										<p
											className={`text-3xl font-bold ${
												filteredSummary.average_score >= 80
													? "text-green-400"
													: filteredSummary.average_score >= 60
														? "text-yellow-400"
														: "text-red-400"
											}`}
										>
											{filteredSummary.average_score?.toFixed(1) || 0}%
										</p>
									</div>
									<div className="text-center">
										<p className="text-xs text-green-400 uppercase tracking-wide mb-1">
											{t("Passed")}
										</p>
										<p className="text-3xl font-bold text-green-400">
											{filteredSummary.total_passed_rules?.toLocaleString() ||
												0}
										</p>
									</div>
									{profileTypeFilter === "docker-bench" ? (
										<div className="text-center">
											<p className="text-xs text-yellow-400 uppercase tracking-wide mb-1">
												{t("Warnings")}
											</p>
											<p className="text-3xl font-bold text-yellow-400">
												{filteredSummary.total_warnings?.toLocaleString() || 0}
											</p>
										</div>
									) : (
										<div className="text-center">
											<p className="text-xs text-red-400 uppercase tracking-wide mb-1">
												{t("Failed")}
											</p>
											<p className="text-3xl font-bold text-red-400">
												{filteredSummary.total_failed_rules?.toLocaleString() ||
													0}
											</p>
										</div>
									)}
									<div className="text-center">
										<p className="text-xs text-white uppercase tracking-wide mb-1 min-h-[2.5em] flex items-end justify-center">
											{t("Total Rules")}
										</p>
										<p className="text-3xl font-bold text-white">
											{filteredSummary.total_rules?.toLocaleString() || 0}
										</p>
									</div>
									<div className="text-center">
										<p className="text-xs text-white uppercase tracking-wide mb-1 min-h-[2.5em] flex items-end justify-center">
											{t("Pass Rate")}
										</p>
										<p className="text-3xl font-bold text-white">
											{filteredSummary.total_rules > 0
												? (
														(filteredSummary.total_passed_rules /
															filteredSummary.total_rules) *
														100
													).toFixed(1)
												: 0}
											%
										</p>
									</div>
								</div>
								{profileTypeFilter === "docker-bench" &&
									filteredSummary.total_rules > 0 && (
										<div className="mt-4 pt-4 border-t border-secondary-700 text-center">
											<span className="text-sm text-white">
												{t("Warning Rate:")}{" "}
											</span>
											<span className="text-sm font-bold text-yellow-400">
												{(
													(filteredSummary.total_warnings /
														filteredSummary.total_rules) *
													100
												).toFixed(1)}
												%
											</span>
										</div>
									)}
							</div>
						</>
					)}

					{/* No data message */}
					{!filteredSummary && (
						<div className="card p-8 text-center">
							<p className="text-white">
								{profileTypeFilter === "all"
									? t("No scan data available")
									: t("No {{name}} scan data available", {
											name: getFilterDisplayName(),
										})}
							</p>
						</div>
					)}

					{/* ==================== OpenSCAP Tab Analysis ==================== */}
					{profileTypeFilter === "openscap" && filteredSummary && (
						<>
							<div className="flex items-center gap-3 pt-4">
								<div className="flex items-center gap-2">
									<PieChartIcon className="h-5 w-5 text-green-400" />
									<h2 className="text-lg font-semibold text-white">
										{t("OpenSCAP Analysis")}
									</h2>
								</div>
								<div className="flex-1 h-px bg-green-700/50" />
							</div>

							<div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
								{/* Rule Pass/Fail Breakdown */}
								{openscapStats && (
									<div className="card p-4 border border-green-700/50">
										<h3 className="text-white font-medium mb-1 flex items-center gap-2">
											<CheckCircle className="h-4 w-4 text-green-400" />
											{t("Rule Results")}
										</h3>
										<p className="text-xs text-secondary-500 dark:text-white mb-3">
											{(() => {
												const evaluated =
													openscapStats.total_passed +
													openscapStats.total_failed;
												return t("{{value}} rules evaluated", {
													count: evaluated,
													value: evaluated.toLocaleString(),
												});
											})()}
										</p>
										<div className="h-40">
											<Doughnut
												data={{
													labels: [t("Passed"), t("Failed")],
													datasets: [
														{
															data: [
																openscapStats.total_passed || 0,
																openscapStats.total_failed || 0,
															],
															backgroundColor: ["#22c55e", "#ef4444"],
															borderWidth: 0,
														},
													],
												}}
												options={getDoughnutOptions(isDark)}
											/>
										</div>
										<div className="flex justify-center gap-6 mt-2 text-sm">
											<div className="flex items-center gap-2">
												<div className="w-3 h-3 rounded-full bg-green-500" />
												<span className="text-green-400">
													{t("Passed: {{value}}", {
														value: openscapStats.total_passed?.toLocaleString(),
													})}
												</span>
											</div>
											<div className="flex items-center gap-2">
												<div className="w-3 h-3 rounded-full bg-red-500" />
												<span className="text-red-400">
													{t("Failed: {{value}}", {
														value: openscapStats.total_failed?.toLocaleString(),
													})}
												</span>
											</div>
										</div>
									</div>
								)}

								{/* Failures by Severity */}
								{severity_by_profile_type &&
									severity_by_profile_type.filter(
										(s) => s.profile_type === "openscap",
									).length > 0 &&
									(() => {
										const openscapSeverity = severity_by_profile_type.filter(
											(s) => s.profile_type === "openscap",
										);
										const totalFailures = openscapSeverity.reduce(
											(sum, s) => sum + s.count,
											0,
										);
										const severityOrder = ["critical", "high", "medium", "low"];
										const severityColors = {
											critical: "#ef4444",
											high: "#f97316",
											medium: "#eab308",
											low: "#22c55e",
										};
										const chartData = severityOrder
											.map((sev) => {
												const count =
													openscapSeverity.find((s) => s.severity === sev)
														?.count || 0;
												if (count === 0) return null;
												return {
													name: t(SEVERITY_LABELS[sev]),
													count,
													color: severityColors[sev],
												};
											})
											.filter(Boolean);

										return (
											<div className="card p-4 border border-green-700/50">
												<h3 className="text-white font-medium mb-1 flex items-center gap-2">
													<AlertTriangle className="h-4 w-4 text-red-400" />
													{t("Failures by Severity")}
												</h3>
												<p className="text-xs text-secondary-500 dark:text-white mb-3">
													{t("{{value}} total failures", {
														count: totalFailures,
														value: totalFailures.toLocaleString(),
													})}
												</p>
												<div className="h-40">
													<ResponsiveContainer width="100%" height="100%">
														<BarChart data={chartData} layout="vertical">
															<XAxis
																type="number"
																stroke="#6b7280"
																fontSize={12}
															/>
															<YAxis
																type="category"
																dataKey="name"
																stroke="#6b7280"
																fontSize={12}
																width={70}
															/>
															<Tooltip
																content={<CustomTooltip type="severity" />}
															/>
															<Bar dataKey="count" radius={[0, 4, 4, 0]}>
																{chartData.map((entry) => (
																	<Cell
																		key={`cell-${entry.name || entry.color || entry.dataKey}`}
																		fill={entry.color}
																	/>
																))}
															</Bar>
														</BarChart>
													</ResponsiveContainer>
												</div>
											</div>
										);
									})()}

								{/* Score Distribution */}
								{filteredScans &&
									filteredScans.length > 0 &&
									(() => {
										const scoreRanges = [
											{
												range: "90-100%",
												count: filteredScans.filter((s) => s.score >= 90)
													.length,
												color: "#22c55e",
											},
											{
												range: "80-89%",
												count: filteredScans.filter(
													(s) => s.score >= 80 && s.score < 90,
												).length,
												color: "#84cc16",
											},
											{
												range: "70-79%",
												count: filteredScans.filter(
													(s) => s.score >= 70 && s.score < 80,
												).length,
												color: "#eab308",
											},
											{
												range: "60-69%",
												count: filteredScans.filter(
													(s) => s.score >= 60 && s.score < 70,
												).length,
												color: "#f97316",
											},
											{
												range: "<60%",
												count: filteredScans.filter((s) => s.score < 60).length,
												color: "#ef4444",
											},
										];
										return (
											<div className="card p-4 border border-green-700/50">
												<h3 className="text-white font-medium mb-1 flex items-center gap-2">
													<BarChart3 className="h-4 w-4 text-green-400" />
													{t("Score Distribution")}
												</h3>
												<p className="text-xs text-secondary-500 dark:text-white mb-3">
													{t("{{count}} scans", {
														count: filteredScans.length,
													})}
												</p>
												<div className="h-40">
													<ResponsiveContainer width="100%" height="100%">
														<BarChart data={scoreRanges} layout="vertical">
															<XAxis
																type="number"
																stroke="#6b7280"
																fontSize={12}
															/>
															<YAxis
																type="category"
																dataKey="range"
																stroke="#6b7280"
																fontSize={12}
																width={60}
															/>
															<Tooltip
																content={<CustomTooltip type="scoreRange" />}
															/>
															<Bar dataKey="count" radius={[0, 4, 4, 0]}>
																{scoreRanges.map((entry) => (
																	<Cell
																		key={`cell-${entry.name || entry.color || entry.dataKey}`}
																		fill={entry.color}
																	/>
																))}
															</Bar>
														</BarChart>
													</ResponsiveContainer>
												</div>
											</div>
										);
									})()}

								{/* Last Scan Age - OpenSCAP only */}
								{scan_age_distribution &&
									(() => {
										const chartData = [
											{
												name: t("Today"),
												count: scan_age_distribution.today?.openscap || 0,
												color: "#22c55e",
											},
											{
												name: t("This Week"),
												count: scan_age_distribution.this_week?.openscap || 0,
												color: "#84cc16",
											},
											{
												name: t("This Month"),
												count: scan_age_distribution.this_month?.openscap || 0,
												color: "#eab308",
											},
											{
												name: t("Older"),
												count: scan_age_distribution.older?.openscap || 0,
												color: "#ef4444",
											},
										].filter((d) => d.count > 0);
										const totalScans = chartData.reduce(
											(sum, d) => sum + d.count,
											0,
										);

										if (totalScans === 0) return null;

										return (
											<div className="card p-4 border border-green-700/50">
												<h3 className="text-white font-medium mb-1 flex items-center gap-2">
													<Clock className="h-4 w-4 text-green-400" />
													{t("Scan Freshness")}
												</h3>
												<p className="text-xs text-secondary-500 dark:text-white mb-3">
													{t("{{count}} OpenSCAP scans", { count: totalScans })}
												</p>
												<div className="h-40">
													<ResponsiveContainer width="100%" height="100%">
														<BarChart data={chartData} layout="vertical">
															<XAxis
																type="number"
																stroke="#6b7280"
																fontSize={12}
															/>
															<YAxis
																type="category"
																dataKey="name"
																stroke="#6b7280"
																fontSize={11}
																width={96}
															/>
															<Tooltip
																content={<CustomTooltip type="scanAge" />}
															/>
															<Bar dataKey="count" radius={[0, 4, 4, 0]}>
																{chartData.map((entry) => (
																	<Cell
																		key={`cell-${entry.name || entry.color || entry.dataKey}`}
																		fill={entry.color}
																	/>
																))}
															</Bar>
														</BarChart>
													</ResponsiveContainer>
												</div>
											</div>
										);
									})()}
							</div>
						</>
					)}

					{/* ==================== Docker Bench Tab Analysis ==================== */}
					{profileTypeFilter === "docker-bench" && filteredSummary && (
						<>
							<div className="flex items-center gap-3 pt-4">
								<div className="flex items-center gap-2">
									<PieChartIcon className="h-5 w-5 text-blue-400" />
									<h2 className="text-lg font-semibold text-white">
										{t("Docker Bench Analysis")}
									</h2>
								</div>
								<div className="flex-1 h-px bg-blue-700/50" />
							</div>

							<div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
								{/* Rule Pass/Warn Breakdown */}
								{dockerBenchStats && (
									<div className="card p-4 border border-blue-700/50">
										<h3 className="text-white font-medium mb-1 flex items-center gap-2">
											<CheckCircle className="h-4 w-4 text-blue-400" />
											{t("Rule Results")}
										</h3>
										<p className="text-xs text-secondary-500 dark:text-white mb-3">
											{(() => {
												const evaluated =
													dockerBenchStats.total_passed +
													dockerBenchStats.total_warnings;
												return t("{{value}} rules evaluated", {
													count: evaluated,
													value: evaluated.toLocaleString(),
												});
											})()}
										</p>
										<div className="h-40">
											<Doughnut
												data={{
													labels: [t("Passed"), t("Warnings")],
													datasets: [
														{
															data: [
																dockerBenchStats.total_passed || 0,
																dockerBenchStats.total_warnings || 0,
															],
															backgroundColor: ["#22c55e", "#eab308"],
															borderWidth: 0,
														},
													],
												}}
												options={getDoughnutOptions(isDark)}
											/>
										</div>
										<div className="flex justify-center gap-6 mt-2 text-sm">
											<div className="flex items-center gap-2">
												<div className="w-3 h-3 rounded-full bg-green-500" />
												<span className="text-green-400">
													{t("Passed: {{value}}", {
														value:
															dockerBenchStats.total_passed?.toLocaleString(),
													})}
												</span>
											</div>
											<div className="flex items-center gap-2">
												<div className="w-3 h-3 rounded-full bg-yellow-500" />
												<span className="text-yellow-400">
													{t("Warnings: {{value}}", {
														value:
															dockerBenchStats.total_warnings?.toLocaleString(),
													})}
												</span>
											</div>
										</div>
									</div>
								)}

								{/* Warnings by Section */}
								{docker_bench_by_section &&
									docker_bench_by_section.length > 0 &&
									(() => {
										const totalWarnings = docker_bench_by_section.reduce(
											(sum, s) => sum + s.count,
											0,
										);
										const sectionColors = {
											"Host Configuration": "#ef4444",
											"Docker Daemon Configuration": "#f97316",
											"Docker Daemon Configuration Files": "#eab308",
											"Container Images and Build File": "#84cc16",
											"Container Runtime": "#22c55e",
											"Docker Security Operations": "#3b82f6",
											"Docker Swarm Configuration": "#8b5cf6",
										};
										const chartData = docker_bench_by_section.map((s) => ({
											name: s.section,
											shortName:
												s.section.length > 20
													? `${s.section.slice(0, 17)}...`
													: s.section,
											count: s.count,
											color: sectionColors[s.section] || "#6b7280",
										}));

										return (
											<div className="card p-4 border border-blue-700/50">
												<h3 className="text-white font-medium mb-1 flex items-center gap-2">
													<Container className="h-4 w-4 text-yellow-400" />
													{t("Warnings by Section")}
												</h3>
												<p className="text-xs text-secondary-500 dark:text-white mb-3">
													{t("{{value}} total warnings", {
														count: totalWarnings,
														value: totalWarnings.toLocaleString(),
													})}
												</p>
												<div className="h-40">
													<ResponsiveContainer width="100%" height="100%">
														<BarChart data={chartData} layout="vertical">
															<XAxis
																type="number"
																stroke="#6b7280"
																fontSize={12}
															/>
															<YAxis
																type="category"
																dataKey="shortName"
																stroke="#6b7280"
																fontSize={10}
																width={85}
															/>
															<Tooltip
																content={({ active, payload }) => {
																	if (
																		!active ||
																		!payload ||
																		payload.length === 0
																	)
																		return null;
																	const data = payload[0].payload;
																	return (
																		<div className="bg-gray-900 border border-gray-700 rounded-lg px-3 py-2 shadow-lg max-w-xs">
																			<p className="text-white font-medium text-sm mb-1">
																				{data.name}
																			</p>
																			<div className="flex items-center gap-2 text-sm">
																				<div
																					className="w-2.5 h-2.5 rounded"
																					style={{
																						backgroundColor: data.color,
																					}}
																				/>
																				<span className="text-gray-300">
																					{t("Warnings:")}
																				</span>
																				<span className="text-white font-medium">
																					{data.count.toLocaleString()}
																				</span>
																			</div>
																		</div>
																	);
																}}
															/>
															<Bar dataKey="count" radius={[0, 4, 4, 0]}>
																{chartData.map((entry) => (
																	<Cell
																		key={`cell-${entry.name || entry.color || entry.dataKey}`}
																		fill={entry.color}
																	/>
																))}
															</Bar>
														</BarChart>
													</ResponsiveContainer>
												</div>
											</div>
										);
									})()}

								{/* Score Distribution */}
								{filteredScans &&
									filteredScans.length > 0 &&
									(() => {
										const scoreRanges = [
											{
												range: "90-100%",
												count: filteredScans.filter((s) => s.score >= 90)
													.length,
												color: "#22c55e",
											},
											{
												range: "80-89%",
												count: filteredScans.filter(
													(s) => s.score >= 80 && s.score < 90,
												).length,
												color: "#84cc16",
											},
											{
												range: "70-79%",
												count: filteredScans.filter(
													(s) => s.score >= 70 && s.score < 80,
												).length,
												color: "#eab308",
											},
											{
												range: "60-69%",
												count: filteredScans.filter(
													(s) => s.score >= 60 && s.score < 70,
												).length,
												color: "#f97316",
											},
											{
												range: "<60%",
												count: filteredScans.filter((s) => s.score < 60).length,
												color: "#ef4444",
											},
										];
										return (
											<div className="card p-4 border border-blue-700/50">
												<h3 className="text-white font-medium mb-1 flex items-center gap-2">
													<BarChart3 className="h-4 w-4 text-blue-400" />
													{t("Score Distribution")}
												</h3>
												<p className="text-xs text-secondary-500 dark:text-white mb-3">
													{t("{{count}} scans", {
														count: filteredScans.length,
													})}
												</p>
												<div className="h-40">
													<ResponsiveContainer width="100%" height="100%">
														<BarChart data={scoreRanges} layout="vertical">
															<XAxis
																type="number"
																stroke="#6b7280"
																fontSize={12}
															/>
															<YAxis
																type="category"
																dataKey="range"
																stroke="#6b7280"
																fontSize={12}
																width={60}
															/>
															<Tooltip
																content={<CustomTooltip type="scoreRange" />}
															/>
															<Bar dataKey="count" radius={[0, 4, 4, 0]}>
																{scoreRanges.map((entry) => (
																	<Cell
																		key={`cell-${entry.name || entry.color || entry.dataKey}`}
																		fill={entry.color}
																	/>
																))}
															</Bar>
														</BarChart>
													</ResponsiveContainer>
												</div>
											</div>
										);
									})()}

								{/* Last Scan Age - Docker Bench only */}
								{scan_age_distribution &&
									(() => {
										const chartData = [
											{
												name: t("Today"),
												count:
													scan_age_distribution.today?.["docker-bench"] || 0,
												color: "#22c55e",
											},
											{
												name: t("This Week"),
												count:
													scan_age_distribution.this_week?.["docker-bench"] ||
													0,
												color: "#84cc16",
											},
											{
												name: t("This Month"),
												count:
													scan_age_distribution.this_month?.["docker-bench"] ||
													0,
												color: "#eab308",
											},
											{
												name: t("Older"),
												count:
													scan_age_distribution.older?.["docker-bench"] || 0,
												color: "#ef4444",
											},
										].filter((d) => d.count > 0);
										const totalScans = chartData.reduce(
											(sum, d) => sum + d.count,
											0,
										);

										if (totalScans === 0) return null;

										return (
											<div className="card p-4 border border-blue-700/50">
												<h3 className="text-white font-medium mb-1 flex items-center gap-2">
													<Clock className="h-4 w-4 text-blue-400" />
													{t("Scan Freshness")}
												</h3>
												<p className="text-xs text-secondary-500 dark:text-white mb-3">
													{t("{{count}} Docker Bench scans", {
														count: totalScans,
													})}
												</p>
												<div className="h-40">
													<ResponsiveContainer width="100%" height="100%">
														<BarChart data={chartData} layout="vertical">
															<XAxis
																type="number"
																stroke="#6b7280"
																fontSize={12}
															/>
															<YAxis
																type="category"
																dataKey="name"
																stroke="#6b7280"
																fontSize={11}
																width={96}
															/>
															<Tooltip
																content={<CustomTooltip type="scanAge" />}
															/>
															<Bar dataKey="count" radius={[0, 4, 4, 0]}>
																{chartData.map((entry) => (
																	<Cell
																		key={`cell-${entry.name || entry.color || entry.dataKey}`}
																		fill={entry.color}
																	/>
																))}
															</Bar>
														</BarChart>
													</ResponsiveContainer>
												</div>
											</div>
										);
									})()}
							</div>
						</>
					)}
				</>
			)}

			{/* ==================== SCAN RESULTS TAB ==================== */}
			{activeTab === "scan-results" && (
				<ScanResultsTab
					profileTypeFilter={profileTypeFilter}
					scannedHosts={hosts_with_latest_scan}
					initialFilters={scanResultsFilters}
				/>
			)}

			{/* ==================== HISTORY TAB ==================== */}
			{activeTab === "history" && (
				<ScanHistoryTab scanned_hosts={hosts_with_latest_scan} />
			)}

			{/* ==================== SETTINGS TAB ==================== */}
			{activeTab === "settings" && <ComplianceSettings />}
		</div>
	);
};

export default Compliance;
