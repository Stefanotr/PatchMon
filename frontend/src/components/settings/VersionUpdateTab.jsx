import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
	AlertCircle,
	CheckCircle,
	Clock,
	Code,
	Download,
	ExternalLink,
} from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { formatDate, settingsAPI, versionAPI } from "../../utils/api";
import {
	commitUrl,
	isForkRepo,
	repoUrl,
	shortCommit,
	sourceRepo,
	UPSTREAM_REPO,
} from "../../utils/sourceRepo";

const VersionUpdateTab = () => {
	const { t } = useTranslation();
	const queryClient = useQueryClient();

	// Fetch current settings
	const { data: settings, isLoading: settingsLoading } = useQuery({
		queryKey: ["settings"],
		queryFn: () => settingsAPI.get().then((res) => res.data),
	});

	// Where this build comes from (shared with the layout's version query).
	const { data: buildInfo } = useQuery({
		queryKey: ["versionInfo"],
		queryFn: () => versionAPI.getCurrent().then((res) => res.data),
	});
	const repo = sourceRepo(buildInfo);
	const isFork = isForkRepo(repo);
	const commit = shortCommit(buildInfo);
	// Links of the fork build line; Trans fills each one with its value.
	const forkLink = (href) => (
		// biome-ignore lint/a11y/useAnchorContent: Trans fills the link text
		<a
			href={href}
			target="_blank"
			rel="noopener noreferrer"
			className="font-mono text-blue-600 dark:text-blue-400 hover:text-blue-800 dark:hover:text-blue-300"
		/>
	);
	const forkLinks = {
		repo: forkLink(repoUrl(repo)),
		commit: forkLink(commitUrl(buildInfo)),
		upstream: forkLink(repoUrl(UPSTREAM_REPO)),
	};

	// Update settings mutation
	const updateSettingsMutation = useMutation({
		mutationFn: (data) => {
			return settingsAPI.update(data).then((res) => res.data);
		},
		onSuccess: () => {
			queryClient.invalidateQueries({ queryKey: ["settings"] });
		},
		onError: (error) => {
			console.error("Failed to update settings:", error);
		},
	});

	// Version checking state
	const [versionInfo, setVersionInfo] = useState({
		currentVersion: null,
		latestVersion: null,
		isUpdateAvailable: false,
		checking: false,
		error: null,
		github: null,
	});

	// Version checking functions
	const checkForUpdates = useCallback(async () => {
		setVersionInfo((prev) => ({ ...prev, checking: true, error: null }));

		try {
			const response = await versionAPI.checkUpdates();
			const data = response.data;

			setVersionInfo({
				currentVersion: data.currentVersion,
				latestVersion: data.latestVersion,
				isUpdateAvailable: data.isUpdateAvailable,
				last_update_check: data.lastUpdateCheck || data.last_update_check,
				latestRelease: data.latestRelease,
				checking: false,
				error: null,
			});
		} catch (error) {
			console.error("Version check error:", error);
			setVersionInfo((prev) => ({
				...prev,
				checking: false,
				error: error.response?.data?.error || t("Failed to check for updates"),
			}));
		}
	}, [t]);

	// Load current version and automatically check for updates on component mount
	useEffect(() => {
		const loadAndCheckUpdates = async () => {
			try {
				// First, get current version info
				const response = await versionAPI.getCurrent();
				const data = response.data;
				setVersionInfo({
					currentVersion: data.version,
					latestVersion: data.latest_version || null,
					isUpdateAvailable: data.is_update_available || false,
					last_update_check: data.last_update_check || null,
					latestRelease: null,
					checking: false,
					error: null,
				});

				// Then automatically trigger a fresh update check
				await checkForUpdates();
			} catch (error) {
				console.error("Error loading version info:", error);
				setVersionInfo((prev) => ({
					...prev,
					error: t("Failed to load version information"),
				}));
			}
		};

		loadAndCheckUpdates();
	}, [checkForUpdates, t]); // Run when component mounts

	return (
		<div className="space-y-6">
			<div className="flex items-center mb-6">
				<Code className="h-6 w-6 text-primary-600 mr-3" />
				<h2 className="text-xl font-semibold text-secondary-900 dark:text-white">
					{t("Server Version Information")}
				</h2>
			</div>

			<div className="bg-secondary-50 dark:bg-secondary-700 rounded-lg p-6">
				<h3 className="text-lg font-medium text-secondary-900 dark:text-white mb-4">
					{t("Version Information")}
				</h3>
				<p className="text-sm text-secondary-600 dark:text-white mb-6">
					{t(
						"Current server version and latest updates from GitHub repository.",
					)}
					{versionInfo.checking && (
						<span className="ml-2 text-blue-600 dark:text-blue-400">
							{t("🔄 Checking for updates...")}
						</span>
					)}
				</p>

				{/* Toggle for showing GitHub version on login */}
				<div className="bg-white dark:bg-secondary-800 rounded-lg p-4 border border-secondary-200 dark:border-secondary-600 mb-6">
					<div className="flex items-center justify-between">
						<div className="flex-1">
							<label
								htmlFor="show-github-version-toggle"
								className="text-sm font-medium text-secondary-900 dark:text-white cursor-pointer"
							>
								{t("Show GitHub Version / Release Notes on Login Screen")}
							</label>
							<p className="text-xs text-secondary-500 dark:text-white mt-1">
								{t(
									"When enabled, the login screen will display the latest GitHub release version and release notes information.",
								)}
							</p>
						</div>
						<button
							type="button"
							id="show-github-version-toggle"
							onClick={() => {
								const newValue = !settings?.show_github_version_on_login;
								updateSettingsMutation.mutate({
									showGithubVersionOnLogin: newValue,
								});
							}}
							disabled={settingsLoading || updateSettingsMutation.isPending}
							className={`relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-md border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 ${
								settings?.show_github_version_on_login !== false
									? "bg-primary-600"
									: "bg-secondary-300 dark:bg-secondary-600"
							} ${settingsLoading || updateSettingsMutation.isPending ? "opacity-50 cursor-not-allowed" : ""}`}
							role="switch"
							aria-checked={settings?.show_github_version_on_login !== false}
						>
							<span
								className={`pointer-events-none inline-block h-5 w-5 transform rounded-md bg-white shadow ring-0 transition duration-200 ease-in-out ${
									settings?.show_github_version_on_login !== false
										? "translate-x-5"
										: "translate-x-0"
								}`}
							/>
						</button>
					</div>
				</div>

				<div className="grid grid-cols-1 md:grid-cols-2 gap-4">
					{/* My Version */}
					<div className="bg-white dark:bg-secondary-800 rounded-lg p-4 border border-secondary-200 dark:border-secondary-600">
						<div className="flex items-center gap-2 mb-2">
							<CheckCircle className="h-4 w-4 text-green-600 dark:text-green-400" />
							<span className="text-sm font-medium text-secondary-700 dark:text-white">
								{t("My Version")}
							</span>
						</div>
						<span className="text-lg font-mono text-secondary-900 dark:text-white">
							{versionInfo.currentVersion}
						</span>
						{isFork && (
							<p className="mt-1 text-xs text-secondary-500 dark:text-secondary-300">
								{commit ? (
									<Trans
										i18nKey="Fork build: <repo>{{repo}}</repo> @ <commit>{{commit}}</commit>, based on <upstream>{{upstream}}</upstream>. Updates follow the fork's releases."
										values={{ repo, commit, upstream: UPSTREAM_REPO }}
										components={forkLinks}
									/>
								) : (
									<Trans
										i18nKey="Fork build: <repo>{{repo}}</repo>, based on <upstream>{{upstream}}</upstream>. Updates follow the fork's releases."
										values={{ repo, upstream: UPSTREAM_REPO }}
										components={forkLinks}
									/>
								)}
							</p>
						)}
					</div>

					{/* Latest Release */}
					{versionInfo.latestRelease && (
						<div className="bg-white dark:bg-secondary-800 rounded-lg p-4 border border-secondary-200 dark:border-secondary-600">
							<div className="flex items-center gap-2 mb-2">
								<Download className="h-4 w-4 text-blue-600 dark:text-blue-400" />
								<span className="text-sm font-medium text-secondary-700 dark:text-white">
									{t("Latest Release")}
								</span>
							</div>
							<div className="space-y-1">
								<span className="text-lg font-mono text-secondary-900 dark:text-white">
									{versionInfo.latestRelease.tagName}
								</span>
								{versionInfo.latestRelease.htmlUrl && (
									<div className="text-xs text-secondary-500 dark:text-white">
										<a
											href={versionInfo.latestRelease.htmlUrl}
											target="_blank"
											rel="noopener noreferrer"
											className="text-blue-600 dark:text-blue-400 hover:text-blue-800 dark:hover:text-blue-300"
										>
											{t("View on GitHub")}{" "}
											<ExternalLink className="h-3 w-3 inline ml-1" />
										</a>
									</div>
								)}
							</div>
						</div>
					)}
				</div>

				{/* Release Information */}
				{versionInfo.latestRelease && (
					<div className="bg-white dark:bg-secondary-800 rounded-lg p-4 border border-secondary-200 dark:border-secondary-600 mt-4">
						<div className="flex items-center gap-2 mb-4">
							<Code className="h-4 w-4 text-purple-600 dark:text-purple-400" />
							<span className="text-sm font-medium text-secondary-700 dark:text-white">
								{t("Release Information")}
							</span>
						</div>

						<div className="grid grid-cols-1 md:grid-cols-2 gap-4">
							{/* Repository Link */}
							<div className="space-y-2">
								<span className="text-xs font-medium text-secondary-600 dark:text-white uppercase tracking-wide">
									{t("Repository")}
								</span>
								<div className="flex items-center gap-2">
									<a
										href={repoUrl(repo)}
										target="_blank"
										rel="noopener noreferrer"
										className="text-blue-600 dark:text-blue-400 hover:text-blue-800 dark:hover:text-blue-300 text-sm font-mono"
									>
										{repo} <ExternalLink className="h-3 w-3 inline ml-1" />
									</a>
								</div>
							</div>

							{/* Latest Release Info */}
							{versionInfo.latestRelease.htmlUrl && (
								<div className="space-y-2">
									<span className="text-xs font-medium text-secondary-600 dark:text-white uppercase tracking-wide">
										{t("Release Link")}
									</span>
									<div className="flex items-center gap-2">
										<a
											href={versionInfo.latestRelease.htmlUrl}
											target="_blank"
											rel="noopener noreferrer"
											className="text-blue-600 dark:text-blue-400 hover:text-blue-800 dark:hover:text-blue-300 text-sm"
										>
											{t("View Release")}{" "}
											<ExternalLink className="h-3 w-3 inline ml-1" />
										</a>
									</div>
								</div>
							)}
						</div>
					</div>
				)}

				{/* Last Checked Time */}
				{versionInfo.last_update_check && (
					<div className="bg-white dark:bg-secondary-800 rounded-lg p-4 border border-secondary-200 dark:border-secondary-600 mt-4">
						<div className="flex items-center gap-2 mb-2">
							<Clock className="h-4 w-4 text-blue-600 dark:text-blue-400" />
							<span className="text-sm font-medium text-secondary-700 dark:text-white">
								{t("Last Checked")}
							</span>
						</div>
						<span className="text-sm text-secondary-600 dark:text-white">
							{formatDate(versionInfo.last_update_check)}
						</span>
						<p className="text-xs text-secondary-500 dark:text-white mt-1">
							{t("Updates are checked automatically every 24 hours")}
						</p>
					</div>
				)}

				<div className="flex items-center justify-start mt-6">
					<button
						type="button"
						onClick={checkForUpdates}
						disabled={versionInfo.checking}
						className="btn-primary flex items-center gap-2"
					>
						<Download className="h-4 w-4" />
						{versionInfo.checking ? t("Checking...") : t("Check for Updates")}
					</button>
				</div>

				{versionInfo.error && (
					<div className="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-700 rounded-lg p-4 mt-4">
						<div className="flex">
							<AlertCircle className="h-5 w-5 text-red-400 dark:text-red-300" />
							<div className="ml-3">
								<h3 className="text-sm font-medium text-red-800 dark:text-red-200">
									{t("Version Check Failed")}
								</h3>
								<p className="mt-1 text-sm text-red-700 dark:text-red-300">
									{versionInfo.error}
								</p>
							</div>
						</div>
					</div>
				)}
			</div>
		</div>
	);
};

export default VersionUpdateTab;
