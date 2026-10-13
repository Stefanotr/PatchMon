import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
	AlertTriangle,
	ArrowLeft,
	Calendar,
	Database,
	Globe,
	Lock,
	Package,
	RefreshCw,
	RotateCcw,
	Search,
	Server,
	Shield,
	ShieldOff,
	Trash2,
	Unlock,
	X,
} from "lucide-react";

import {
	useCallback,
	useEffect,
	useId,
	useMemo,
	useRef,
	useState,
} from "react";

import { Trans, useTranslation } from "react-i18next";
import { Link, useNavigate, useParams } from "react-router-dom";
import ModalPortal from "../components/ui/ModalPortal";
import {
	formatDateOnly,
	formatRelativeTime,
	packagesAPI,
	repositoryAPI,
} from "../utils/api";

const RepositoryDetail = () => {
	const { t } = useTranslation();
	const isActiveId = useId();
	const repositoryNameId = useId();
	const priorityId = useId();
	const descriptionId = useId();
	const { repositoryId } = useParams();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const [editMode, setEditMode] = useState(false);
	const [formData, setFormData] = useState({});
	const [searchTerm, setSearchTerm] = useState("");
	const [currentPage, setCurrentPage] = useState(1);
	const [pageSize, setPageSize] = useState(25);
	const [showDeleteModal, setShowDeleteModal] = useState(false);
	const [packagesSearch, setPackagesSearch] = useState("");
	const [packagesPage, setPackagesPage] = useState(1);
	const [packagesPageSize, setPackagesPageSize] = useState(25);
	const packagesSearchTimerRef = useRef(null);
	const [packagesSearchInput, setPackagesSearchInput] = useState("");

	// Fetch repository details
	const {
		data: repository,
		isLoading,
		error,
	} = useQuery({
		queryKey: ["repository", repositoryId],
		queryFn: () => repositoryAPI.getById(repositoryId).then((res) => res.data),
		enabled: !!repositoryId,
	});

	// Fetch packages from this repository
	const { data: packagesResponse, isLoading: packagesLoading } = useQuery({
		queryKey: [
			"repository-packages",
			repositoryId,
			packagesSearch,
			packagesPage,
			packagesPageSize,
		],
		queryFn: () =>
			packagesAPI.getAll({
				repository: repositoryId,
				search: packagesSearch,
				page: packagesPage,
				limit: packagesPageSize,
			}),
		enabled: !!repositoryId,
	});

	const packages = packagesResponse?.data?.packages || [];
	const packagesPagination = packagesResponse?.data?.pagination || {};

	// Debounced packages search
	const handlePackagesSearchChange = useCallback((value) => {
		setPackagesSearchInput(value);
		if (packagesSearchTimerRef.current) {
			clearTimeout(packagesSearchTimerRef.current);
		}
		packagesSearchTimerRef.current = setTimeout(() => {
			setPackagesSearch(value);
			setPackagesPage(1);
		}, 400);
	}, []);

	useEffect(() => {
		return () => {
			if (packagesSearchTimerRef.current) {
				clearTimeout(packagesSearchTimerRef.current);
			}
		};
	}, []);

	const handlePackageClick = (packageId) => {
		navigate(`/packages/${packageId}`);
	};

	const getPackageStatusBadge = (stats) => {
		if ((stats?.securityUpdates || 0) > 0) {
			return (
				<span className="badge-danger flex items-center gap-1 w-fit">
					<Shield className="h-3 w-3" />
					{t("Security Update")}
				</span>
			);
		}
		if ((stats?.updatesNeeded || 0) > 0) {
			return <span className="badge-warning">{t("Update Available")}</span>;
		}
		return <span className="badge-success">{t("Up to Date")}</span>;
	};

	const getRebootBadge = (host) => {
		if (!host.needs_reboot) return null;
		return (
			<span
				className="inline-flex items-center gap-1 px-2 py-1 rounded-md text-xs font-medium bg-warning-100 text-warning-800 dark:bg-warning-900 dark:text-warning-200"
				title={t("Reboot required")}
			>
				<RotateCcw className="h-3 w-3" />
				{t("Required")}
			</span>
		);
	};

	const hosts = repository?.host_repositories || [];

	// Filter and paginate hosts
	const filteredAndPaginatedHosts = useMemo(() => {
		let filtered = hosts;

		if (searchTerm) {
			filtered = hosts.filter(
				(hostRepo) =>
					hostRepo.hosts.friendly_name
						?.toLowerCase()
						.includes(searchTerm.toLowerCase()) ||
					hostRepo.hosts.hostname
						?.toLowerCase()
						.includes(searchTerm.toLowerCase()) ||
					hostRepo.hosts.ip?.toLowerCase().includes(searchTerm.toLowerCase()),
			);
		}

		const startIndex = (currentPage - 1) * pageSize;
		const endIndex = startIndex + pageSize;
		return filtered.slice(startIndex, endIndex);
	}, [hosts, searchTerm, currentPage, pageSize]);

	const totalPages = Math.ceil(
		(searchTerm
			? hosts.filter(
					(hostRepo) =>
						hostRepo.hosts.friendly_name
							?.toLowerCase()
							.includes(searchTerm.toLowerCase()) ||
						hostRepo.hosts.hostname
							?.toLowerCase()
							.includes(searchTerm.toLowerCase()) ||
						hostRepo.hosts.ip?.toLowerCase().includes(searchTerm.toLowerCase()),
				).length
			: hosts.length) / pageSize,
	);

	const handleHostClick = (hostId) => {
		navigate(`/hosts/${hostId}`);
	};

	// Update repository mutation
	const updateRepositoryMutation = useMutation({
		mutationFn: (data) => repositoryAPI.update(repositoryId, data),
		onSuccess: () => {
			queryClient.invalidateQueries({ queryKey: ["repository", repositoryId] });
			queryClient.invalidateQueries({ queryKey: ["repositories"] });
			setEditMode(false);
		},
	});

	// Delete repository mutation
	const deleteRepositoryMutation = useMutation({
		mutationFn: () => repositoryAPI.delete(repositoryId),
		onSuccess: () => {
			queryClient.invalidateQueries({ queryKey: ["repositories"] });
			navigate("/repositories");
		},
	});

	const handleEdit = () => {
		setFormData({
			name: repository.name,
			description: repository.description || "",
			is_active: repository.is_active,
			priority: repository.priority ?? "",
		});
		setEditMode(true);
	};

	const handleSave = () => {
		updateRepositoryMutation.mutate(formData);
	};

	const handleCancel = () => {
		setEditMode(false);
		setFormData({});
	};

	const handleDelete = () => {
		setShowDeleteModal(true);
	};

	const confirmDelete = () => {
		deleteRepositoryMutation.mutate();
		setShowDeleteModal(false);
	};

	const cancelDelete = () => {
		setShowDeleteModal(false);
	};

	if (isLoading) {
		return (
			<div className="flex items-center justify-center h-64">
				<RefreshCw className="h-8 w-8 animate-spin text-primary-600" />
			</div>
		);
	}

	if (error) {
		return (
			<div className="space-y-4 sm:space-y-6">
				<div className="flex items-center gap-3">
					<Link
						to="/repositories"
						className="inline-flex items-center justify-center -ml-2 min-h-[44px] min-w-[44px] md:min-h-0 md:min-w-0 md:ml-0 text-secondary-500 hover:text-secondary-700 dark:text-white dark:hover:text-secondary-200"
						aria-label={t("Back to repositories")}
						title={t("Back to Repositories")}
					>
						<ArrowLeft className="h-5 w-5" />
					</Link>
					<h1 className="text-2xl font-semibold text-secondary-900 dark:text-white">
						{t("Repository")}
					</h1>
				</div>
				<div className="bg-danger-50 dark:bg-danger-900/20 border border-danger-200 dark:border-danger-800 rounded-lg p-4">
					<div className="flex items-center">
						<AlertTriangle className="h-5 w-5 text-danger-400 mr-2 flex-shrink-0" />
						<span className="text-danger-700 dark:text-danger-300">
							{t("Failed to load repository: {{message}}", {
								message: error.message,
							})}
						</span>
					</div>
				</div>
			</div>
		);
	}

	if (!repository) {
		return (
			<div className="space-y-4 sm:space-y-6">
				<div className="flex items-center gap-3">
					<Link
						to="/repositories"
						className="inline-flex items-center justify-center -ml-2 min-h-[44px] min-w-[44px] md:min-h-0 md:min-w-0 md:ml-0 text-secondary-500 hover:text-secondary-700 dark:text-white dark:hover:text-secondary-200"
						aria-label={t("Back to repositories")}
						title={t("Back to Repositories")}
					>
						<ArrowLeft className="h-5 w-5" />
					</Link>
					<h1 className="text-2xl font-semibold text-secondary-900 dark:text-white">
						{t("Repository")}
					</h1>
				</div>
				<div className="text-center py-12">
					<Database className="mx-auto h-12 w-12 text-secondary-400" />
					<h3 className="mt-2 text-sm font-medium text-secondary-900 dark:text-white">
						{t("Repository not found")}
					</h3>
					<p className="mt-1 text-sm text-secondary-500 dark:text-white">
						{t("The repository you're looking for doesn't exist.")}
					</p>
				</div>
			</div>
		);
	}

	return (
		<div className="space-y-4 sm:space-y-6">
			{/* Delete Confirmation Modal */}
			{showDeleteModal && (
				<ModalPortal>
					<div className="fixed inset-0 bg-black/50 flex items-center justify-center z-[120]">
						<button
							type="button"
							onClick={cancelDelete}
							className="fixed inset-0 cursor-default"
							aria-label={t("Close modal")}
							disabled={deleteRepositoryMutation.isPending}
						/>
						<div className="bg-white dark:bg-secondary-800 rounded-lg shadow-xl max-w-md w-full mx-4 relative z-10">
							<div className="px-6 py-4 border-b border-secondary-200 dark:border-secondary-600">
								<div className="flex items-center justify-between gap-3">
									<div className="flex items-center gap-3 min-w-0">
										<div className="w-10 h-10 bg-danger-100 dark:bg-danger-900 rounded-full flex items-center justify-center flex-shrink-0">
											<AlertTriangle className="h-5 w-5 text-danger-600 dark:text-danger-400" />
										</div>
										<div className="min-w-0">
											<h3 className="text-lg font-medium text-secondary-900 dark:text-white">
												{t("Delete Repository")}
											</h3>
											<p className="text-sm text-secondary-600 dark:text-white">
												{t("This action cannot be undone")}
											</p>
										</div>
									</div>
									<button
										type="button"
										onClick={cancelDelete}
										className="p-1 rounded hover:bg-secondary-100 dark:hover:bg-secondary-700 text-secondary-400 hover:text-secondary-600 disabled:opacity-50 disabled:cursor-not-allowed flex-shrink-0"
										aria-label={t("Close")}
										disabled={deleteRepositoryMutation.isPending}
									>
										<X className="h-5 w-5" />
									</button>
								</div>
							</div>
							<div className="px-6 py-4">
								<p className="text-secondary-700 dark:text-white">
									<Trans
										i18nKey='Are you sure you want to delete "<name/>"?'
										components={{
											name: (
												<span className="font-semibold">
													{repository?.name}
												</span>
											),
										}}
									/>
								</p>
								{repository?.host_repositories?.length > 0 && (
									<div className="mt-3 p-3 bg-danger-50 dark:bg-danger-900 border border-danger-200 dark:border-danger-700 rounded-md">
										<p className="text-sm text-danger-800 dark:text-danger-200">
											<Trans
												i18nKey="<strong>Warning:</strong> This repository is currently assigned to {{count}} host."
												count={repository.host_repositories.length}
												values={{ count: repository.host_repositories.length }}
												components={{ strong: <strong /> }}
											/>
										</p>
									</div>
								)}
							</div>
							<div className="px-6 py-4 border-t border-secondary-200 dark:border-secondary-600 flex justify-end gap-3">
								<button
									type="button"
									onClick={cancelDelete}
									className="btn-outline"
									disabled={deleteRepositoryMutation.isPending}
								>
									{t("Cancel")}
								</button>
								<button
									type="button"
									onClick={confirmDelete}
									className="btn-danger disabled:opacity-50 disabled:cursor-not-allowed"
									disabled={deleteRepositoryMutation.isPending}
								>
									{deleteRepositoryMutation.isPending
										? t("Deleting...")
										: t("Delete Repository")}
								</button>
							</div>
						</div>
					</div>
				</ModalPortal>
			)}

			{/* Header */}
			<div className="flex flex-col md:flex-row md:items-start md:justify-between gap-4 pb-4 border-b border-secondary-200 dark:border-secondary-600">
				<div className="flex items-start gap-3 min-w-0">
					<Link
						to="/repositories"
						className="inline-flex items-center justify-center -ml-2 min-h-[44px] min-w-[44px] md:min-h-0 md:min-w-0 md:ml-0 md:mt-1 flex-shrink-0 text-secondary-500 hover:text-secondary-700 dark:text-white dark:hover:text-secondary-200"
						aria-label={t("Back to repositories")}
						title={t("Back to Repositories")}
					>
						<ArrowLeft className="h-5 w-5" />
					</Link>
					<div className="flex items-center gap-3 flex-wrap min-w-0">
						{repository.isSecure ? (
							<Lock className="h-6 w-6 text-success-600 flex-shrink-0" />
						) : (
							<Unlock className="h-6 w-6 text-warning-600 flex-shrink-0" />
						)}
						<h1 className="text-2xl font-semibold text-secondary-900 dark:text-white truncate">
							{repository.name}
						</h1>
						<span
							className={
								repository.is_active ? "badge-success" : "badge-danger"
							}
						>
							{repository.is_active ? t("Active") : t("Inactive")}
						</span>
					</div>
				</div>
				<div className="flex items-center gap-2 flex-wrap w-full md:w-auto md:flex-shrink-0">
					{editMode ? (
						<>
							<button
								type="button"
								onClick={handleCancel}
								className="btn-outline min-h-[44px] md:min-h-0 whitespace-nowrap"
								disabled={updateRepositoryMutation.isPending}
							>
								{t("Cancel")}
							</button>
							<button
								type="button"
								onClick={handleSave}
								className="btn-primary min-h-[44px] md:min-h-0 whitespace-nowrap"
								disabled={updateRepositoryMutation.isPending}
							>
								{updateRepositoryMutation.isPending
									? t("Saving...")
									: t("Save Changes")}
							</button>
						</>
					) : (
						<>
							<button
								type="button"
								onClick={handleDelete}
								className="btn-outline border-danger-200 text-danger-600 hover:bg-danger-50 hover:border-danger-300 dark:border-danger-800 dark:text-danger-400 dark:hover:bg-danger-900/20 dark:hover:border-danger-700 flex items-center gap-2 min-h-[44px] md:min-h-0 whitespace-nowrap"
								disabled={deleteRepositoryMutation.isPending}
							>
								<Trash2 className="h-4 w-4" />
								{deleteRepositoryMutation.isPending
									? t("Deleting...")
									: t("Delete")}
							</button>
							<button
								type="button"
								onClick={handleEdit}
								className="btn-primary min-h-[44px] md:min-h-0 whitespace-nowrap"
							>
								<span className="hidden sm:inline">{t("Edit Repository")}</span>
								<span className="sm:hidden">{t("Edit")}</span>
							</button>
						</>
					)}
				</div>
			</div>

			{/* Repository Information */}
			<div className="card">
				<div className="px-6 py-4 border-b border-secondary-200 dark:border-secondary-700">
					<h2 className="text-lg font-semibold text-secondary-900 dark:text-white">
						{t("Repository Information")}
					</h2>
				</div>
				<div className="px-6 py-4 space-y-4">
					{editMode ? (
						<div className="grid grid-cols-1 md:grid-cols-2 gap-6">
							<div>
								<label
									htmlFor={repositoryNameId}
									className="block text-sm font-medium text-secondary-700 dark:text-white mb-1"
								>
									{t("Repository Name")}
								</label>
								<input
									type="text"
									id={repositoryNameId}
									value={formData.name}
									onChange={(e) =>
										setFormData({ ...formData, name: e.target.value })
									}
									className="w-full px-3 py-2 border border-secondary-300 dark:border-secondary-600 rounded-md focus:outline-none focus:ring-2 focus:ring-primary-500 dark:bg-secondary-700 dark:text-white"
								/>
							</div>
							<div>
								<label
									htmlFor={priorityId}
									className="block text-sm font-medium text-secondary-700 dark:text-white mb-1"
								>
									{t("Priority")}
								</label>
								<input
									type="number"
									id={priorityId}
									value={formData.priority}
									onChange={(e) =>
										setFormData({ ...formData, priority: e.target.value })
									}
									className="w-full px-3 py-2 border border-secondary-300 dark:border-secondary-600 rounded-md focus:outline-none focus:ring-2 focus:ring-primary-500 dark:bg-secondary-700 dark:text-white"
									placeholder={t("Optional priority")}
								/>
							</div>
							<div className="md:col-span-2">
								<label
									htmlFor={descriptionId}
									className="block text-sm font-medium text-secondary-700 dark:text-white mb-1"
								>
									{t("Description")}
								</label>
								<textarea
									id={descriptionId}
									value={formData.description}
									onChange={(e) =>
										setFormData({ ...formData, description: e.target.value })
									}
									rows="3"
									className="w-full px-3 py-2 border border-secondary-300 dark:border-secondary-600 rounded-md focus:outline-none focus:ring-2 focus:ring-primary-500 dark:bg-secondary-700 dark:text-white"
									placeholder={t("Optional description")}
								/>
							</div>
							<div className="flex items-center">
								<input
									type="checkbox"
									id={isActiveId}
									checked={formData.is_active}
									onChange={(e) =>
										setFormData({ ...formData, is_active: e.target.checked })
									}
									className="h-4 w-4 text-primary-600 focus:ring-primary-500 border-secondary-300 rounded"
								/>
								<label
									htmlFor={isActiveId}
									className="ml-2 block text-sm text-secondary-900 dark:text-white"
								>
									{t("Repository is active")}
								</label>
							</div>
						</div>
					) : (
						<div className="grid grid-cols-1 md:grid-cols-2 gap-6">
							<div className="space-y-4">
								<div>
									<span className="text-sm font-medium text-secondary-500 dark:text-white">
										{t("URL")}
									</span>
									<div className="flex items-center mt-1">
										<Globe className="h-4 w-4 text-secondary-400 mr-2" />
										<span className="text-secondary-900 dark:text-white">
											{repository.url}
										</span>
									</div>
								</div>
								<div>
									<span className="text-sm font-medium text-secondary-500 dark:text-white">
										{t("Distribution")}
									</span>
									<p className="text-secondary-900 dark:text-white mt-1">
										{repository.distribution}
									</p>
								</div>
								<div>
									<span className="text-sm font-medium text-secondary-500 dark:text-white">
										{t("Components")}
									</span>
									<p className="text-secondary-900 dark:text-white mt-1">
										{repository.components}
									</p>
								</div>
								<div>
									<span className="text-sm font-medium text-secondary-500 dark:text-white">
										{t("Repository Type")}
									</span>
									<p className="text-secondary-900 dark:text-white mt-1">
										{repository.repo_type}
									</p>
								</div>
							</div>
							<div className="space-y-4">
								<div>
									<span className="text-sm font-medium text-secondary-500 dark:text-white">
										{t("Security")}
									</span>
									<div className="flex items-center mt-1">
										{repository.isSecure ? (
											<>
												<Shield className="h-4 w-4 text-success-700 dark:text-success-400 mr-2" />
												<span className="text-success-700 dark:text-success-400">
													{t("Secure (HTTPS)")}
												</span>
											</>
										) : (
											<>
												<ShieldOff className="h-4 w-4 text-warning-700 dark:text-warning-400 mr-2" />
												<span className="text-warning-700 dark:text-warning-400">
													{t("Insecure (HTTP)")}
												</span>
											</>
										)}
									</div>
								</div>
								{repository.priority != null && (
									<div>
										<span className="text-sm font-medium text-secondary-500 dark:text-white">
											{t("Priority")}
										</span>
										<p className="text-secondary-900 dark:text-white mt-1">
											{repository.priority}
										</p>
									</div>
								)}
								{repository.description && (
									<div>
										<span className="text-sm font-medium text-secondary-500 dark:text-white">
											{t("Description")}
										</span>
										<p className="text-secondary-900 dark:text-white mt-1">
											{repository.description}
										</p>
									</div>
								)}
								<div>
									<span className="text-sm font-medium text-secondary-500 dark:text-white">
										{t("Created")}
									</span>
									<div className="flex items-center mt-1">
										<Calendar className="h-4 w-4 text-secondary-400 mr-2" />
										<span className="text-secondary-900 dark:text-white">
											{formatDateOnly(repository.created_at)}
										</span>
									</div>
								</div>
							</div>
						</div>
					)}
				</div>
			</div>

			{/* Hosts Using This Repository */}
			<div className="card">
				<div className="px-6 py-4 border-b border-secondary-200 dark:border-secondary-600">
					<div className="flex items-center justify-between mb-4">
						<div className="flex items-center gap-3">
							<Server className="h-5 w-5 text-primary-600" />
							<h3 className="text-lg font-medium text-secondary-900 dark:text-white">
								{t("Hosts Using This Repository ({{total}})", {
									total: hosts.length,
								})}
							</h3>
						</div>
					</div>

					{/* Search */}
					<div className="relative max-w-sm">
						<Search className="absolute left-3 top-1/2 transform -translate-y-1/2 h-4 w-4 text-secondary-400" />
						<input
							type="text"
							placeholder={t("Search hosts...")}
							value={searchTerm}
							onChange={(e) => {
								setSearchTerm(e.target.value);
								setCurrentPage(1);
							}}
							className="w-full pl-10 pr-4 py-2 border border-secondary-300 dark:border-secondary-600 rounded-md focus:ring-2 focus:ring-primary-500 focus:border-transparent bg-white dark:bg-secondary-800 text-secondary-900 dark:text-white placeholder-secondary-500 dark:placeholder-secondary-400"
						/>
					</div>
				</div>

				<div className="overflow-x-auto">
					{filteredAndPaginatedHosts.length === 0 ? (
						<div className="text-center py-8">
							<Server className="h-12 w-12 text-secondary-400 mx-auto mb-4" />
							<p className="text-secondary-500 dark:text-white">
								{searchTerm
									? t("No hosts match your search")
									: t("This repository hasn't been reported by any hosts yet.")}
							</p>
						</div>
					) : (
						<>
							{/* Desktop table */}
							<div className="hidden md:block">
								<table className="min-w-full divide-y divide-secondary-200 dark:divide-secondary-600">
									<thead className="bg-secondary-50 dark:bg-secondary-700">
										<tr>
											<th className="px-6 py-3 text-left text-xs font-medium text-secondary-500 dark:text-white uppercase tracking-wider">
												{t("Host")}
											</th>
											<th className="px-6 py-3 text-left text-xs font-medium text-secondary-500 dark:text-white uppercase tracking-wider">
												{t("Operating System")}
											</th>
											<th className="px-6 py-3 text-left text-xs font-medium text-secondary-500 dark:text-white uppercase tracking-wider">
												{t("Last Checked")}
											</th>
											<th className="px-6 py-3 text-left text-xs font-medium text-secondary-500 dark:text-white uppercase tracking-wider">
												{t("Last Update")}
											</th>
											<th className="px-6 py-3 text-left text-xs font-medium text-secondary-500 dark:text-white uppercase tracking-wider">
												{t("Reboot Required")}
											</th>
										</tr>
									</thead>
									<tbody className="bg-white dark:bg-secondary-800 divide-y divide-secondary-200 dark:divide-secondary-600">
										{filteredAndPaginatedHosts.map((hostRepo) => (
											<tr
												key={hostRepo.id}
												className="hover:bg-secondary-50 dark:hover:bg-secondary-700 cursor-pointer transition-colors"
												onClick={() => handleHostClick(hostRepo.hosts.id)}
											>
												<td className="px-6 py-4 whitespace-nowrap">
													<div className="flex items-center">
														<div
															className={`w-2 h-2 rounded-full mr-3 ${
																hostRepo.hosts.status === "active"
																	? "bg-success-500"
																	: hostRepo.hosts.status === "pending"
																		? "bg-warning-500"
																		: "bg-danger-500"
															}`}
														/>
														<Server className="h-5 w-5 text-secondary-400 mr-3" />
														<div>
															<div className="text-sm font-medium text-secondary-900 dark:text-white">
																{hostRepo.hosts.friendly_name ||
																	hostRepo.hosts.hostname}
															</div>
															{hostRepo.hosts.friendly_name &&
																hostRepo.hosts.hostname && (
																	<div className="text-sm text-secondary-500 dark:text-white">
																		{hostRepo.hosts.hostname}
																	</div>
																)}
														</div>
													</div>
												</td>
												<td className="px-6 py-4 whitespace-nowrap text-sm text-secondary-900 dark:text-white">
													{hostRepo.hosts.os_type} {hostRepo.hosts.os_version}
												</td>
												<td className="px-6 py-4 whitespace-nowrap text-sm text-secondary-500 dark:text-white">
													{hostRepo.last_checked
														? formatRelativeTime(hostRepo.last_checked)
														: t("Never")}
												</td>
												<td className="px-6 py-4 whitespace-nowrap text-sm text-secondary-500 dark:text-white">
													{hostRepo.hosts.last_update
														? formatRelativeTime(hostRepo.hosts.last_update)
														: t("Never")}
												</td>
												<td className="px-6 py-4 whitespace-nowrap">
													{getRebootBadge(hostRepo.hosts) || (
														<span className="text-sm text-secondary-500 dark:text-white">
															{t("No")}
														</span>
													)}
												</td>
											</tr>
										))}
									</tbody>
								</table>
							</div>

							{/* Mobile cards */}
							<div className="md:hidden divide-y divide-secondary-200 dark:divide-secondary-600">
								{filteredAndPaginatedHosts.map((hostRepo) => (
									<button
										type="button"
										key={hostRepo.id}
										className="w-full text-left p-4 hover:bg-secondary-50 dark:hover:bg-secondary-700 cursor-pointer transition-colors min-h-[44px]"
										onClick={() => handleHostClick(hostRepo.hosts.id)}
									>
										<div className="flex items-start justify-between gap-2 mb-2">
											<div className="flex items-center min-w-0">
												<div
													className={`w-2 h-2 rounded-full mr-2 flex-shrink-0 ${
														hostRepo.hosts.status === "active"
															? "bg-success-500"
															: hostRepo.hosts.status === "pending"
																? "bg-warning-500"
																: "bg-danger-500"
													}`}
												/>
												<Server className="h-4 w-4 text-secondary-400 mr-2 flex-shrink-0" />
												<div className="min-w-0">
													<div className="text-sm font-medium text-secondary-900 dark:text-white truncate">
														{hostRepo.hosts.friendly_name ||
															hostRepo.hosts.hostname}
													</div>
													{hostRepo.hosts.friendly_name &&
														hostRepo.hosts.hostname && (
															<div className="text-xs text-secondary-500 dark:text-secondary-400 truncate">
																{hostRepo.hosts.hostname}
															</div>
														)}
												</div>
											</div>
											<div className="flex-shrink-0">
												{getRebootBadge(hostRepo.hosts)}
											</div>
										</div>
										<div className="text-xs text-secondary-500 dark:text-secondary-400">
											{hostRepo.hosts.os_type} {hostRepo.hosts.os_version}
										</div>
										<div className="flex items-center justify-between gap-2 mt-1 text-xs text-secondary-500 dark:text-secondary-400">
											<span>
												{t("Checked: {{when}}", {
													when: hostRepo.last_checked
														? formatRelativeTime(hostRepo.last_checked)
														: t("Never"),
												})}
											</span>
											<span>
												{t("Updated: {{when}}", {
													when: hostRepo.hosts.last_update
														? formatRelativeTime(hostRepo.hosts.last_update)
														: t("Never"),
												})}
											</span>
										</div>
									</button>
								))}
							</div>

							{/* Pagination */}
							{totalPages > 1 && (
								<div className="px-6 py-3 bg-white dark:bg-secondary-800 border-t border-secondary-200 dark:border-secondary-600 flex items-center justify-between">
									<div className="flex items-center gap-2">
										<span className="text-sm text-secondary-700 dark:text-white">
											{t("Rows per page:")}
										</span>
										<select
											value={pageSize}
											onChange={(e) => {
												setPageSize(Number(e.target.value));
												setCurrentPage(1);
											}}
											className="text-sm border border-secondary-300 dark:border-secondary-600 rounded px-2 py-1 bg-white dark:bg-secondary-700 text-secondary-900 dark:text-white"
										>
											<option value={25}>25</option>
											<option value={50}>50</option>
											<option value={100}>100</option>
										</select>
									</div>
									<div className="flex items-center gap-2">
										<button
											type="button"
											onClick={() => setCurrentPage(currentPage - 1)}
											disabled={currentPage === 1}
											className="px-3 py-1 text-sm border border-secondary-300 dark:border-secondary-600 rounded disabled:opacity-50 disabled:cursor-not-allowed hover:bg-secondary-50 dark:hover:bg-secondary-700"
										>
											{t("Previous")}
										</button>
										<span className="text-sm text-secondary-700 dark:text-white">
											{t("Page {{page}} of {{totalPages}}", {
												page: currentPage,
												totalPages,
											})}
										</span>
										<button
											type="button"
											onClick={() => setCurrentPage(currentPage + 1)}
											disabled={currentPage === totalPages}
											className="px-3 py-1 text-sm border border-secondary-300 dark:border-secondary-600 rounded disabled:opacity-50 disabled:cursor-not-allowed hover:bg-secondary-50 dark:hover:bg-secondary-700"
										>
											{t("Next")}
										</button>
									</div>
								</div>
							)}
						</>
					)}
				</div>
			</div>

			{/* Packages from this Repository */}
			<div className="card">
				<div className="px-6 py-4 border-b border-secondary-200 dark:border-secondary-600">
					<div className="flex items-center justify-between mb-4">
						<div className="flex items-center gap-3">
							<Package className="h-5 w-5 text-primary-600" />
							<h3 className="text-lg font-medium text-secondary-900 dark:text-white">
								{packagesPagination.total != null
									? t("Packages from this Repository ({{total}})", {
											total: packagesPagination.total,
										})
									: t("Packages from this Repository")}
							</h3>
						</div>
					</div>

					{/* Search */}
					<div className="relative max-w-sm">
						<Search className="absolute left-3 top-1/2 transform -translate-y-1/2 h-4 w-4 text-secondary-400" />
						<input
							type="text"
							placeholder={t("Search packages...")}
							value={packagesSearchInput}
							onChange={(e) => handlePackagesSearchChange(e.target.value)}
							className="w-full pl-10 pr-4 py-2 border border-secondary-300 dark:border-secondary-600 rounded-md focus:ring-2 focus:ring-primary-500 focus:border-transparent bg-white dark:bg-secondary-800 text-secondary-900 dark:text-white placeholder-secondary-500 dark:placeholder-secondary-400"
						/>
					</div>
				</div>

				<div className="overflow-x-auto">
					{packagesLoading ? (
						<div className="flex items-center justify-center py-12">
							<RefreshCw className="h-8 w-8 animate-spin text-primary-600" />
						</div>
					) : packages.length === 0 ? (
						<div className="text-center py-8">
							<Package className="h-12 w-12 text-secondary-400 mx-auto mb-4" />
							<p className="text-secondary-500 dark:text-white">
								{packagesSearch
									? t("No packages match your search")
									: t("No packages found from this repository.")}
							</p>
						</div>
					) : (
						<>
							{/* Desktop table */}
							<div className="hidden md:block">
								<table className="min-w-full divide-y divide-secondary-200 dark:divide-secondary-600">
									<thead className="bg-secondary-50 dark:bg-secondary-700">
										<tr>
											<th className="px-6 py-3 text-left text-xs font-medium text-secondary-500 dark:text-white uppercase tracking-wider">
												{t("Package Name")}
											</th>
											<th className="px-6 py-3 text-left text-xs font-medium text-secondary-500 dark:text-white uppercase tracking-wider">
												{t("Latest Version")}
											</th>
											<th className="px-6 py-3 text-left text-xs font-medium text-secondary-500 dark:text-white uppercase tracking-wider">
												{t("Status")}
											</th>
											<th className="px-6 py-3 text-left text-xs font-medium text-secondary-500 dark:text-white uppercase tracking-wider">
												{t("Installed On")}
											</th>
										</tr>
									</thead>
									<tbody className="bg-white dark:bg-secondary-800 divide-y divide-secondary-200 dark:divide-secondary-600">
										{packages.map((pkg) => (
											<tr
												key={pkg.id}
												className="hover:bg-secondary-50 dark:hover:bg-secondary-700 cursor-pointer transition-colors"
												onClick={() => handlePackageClick(pkg.id)}
											>
												<td className="px-6 py-4 whitespace-nowrap">
													<div className="text-sm font-medium text-secondary-900 dark:text-white">
														{pkg.name}
													</div>
													{pkg.description && (
														<div className="text-xs text-secondary-500 dark:text-secondary-400 truncate max-w-xs">
															{pkg.description}
														</div>
													)}
												</td>
												<td className="px-6 py-4 whitespace-nowrap text-sm text-secondary-900 dark:text-white">
													{pkg.latest_version || "—"}
												</td>
												<td className="px-6 py-4 whitespace-nowrap">
													{getPackageStatusBadge(pkg.stats)}
												</td>
												<td className="px-6 py-4 whitespace-nowrap text-sm text-secondary-900 dark:text-white">
													{t("{{count}} host", {
														count: pkg.stats?.totalInstalls || 0,
													})}
												</td>
											</tr>
										))}
									</tbody>
								</table>
							</div>

							{/* Mobile cards */}
							<div className="md:hidden divide-y divide-secondary-200 dark:divide-secondary-600">
								{packages.map((pkg) => (
									<button
										type="button"
										key={pkg.id}
										className="w-full text-left p-4 hover:bg-secondary-50 dark:hover:bg-secondary-700 cursor-pointer transition-colors min-h-[44px]"
										onClick={() => handlePackageClick(pkg.id)}
									>
										<div className="flex items-center justify-between mb-2">
											<span className="text-sm font-medium text-secondary-900 dark:text-white">
												{pkg.name}
											</span>
											{getPackageStatusBadge(pkg.stats)}
										</div>
										<div className="flex items-center justify-between text-xs text-secondary-500 dark:text-secondary-400">
											<span>{pkg.latest_version || "—"}</span>
											<span>
												{t("{{count}} host", {
													count: pkg.stats?.totalInstalls || 0,
												})}
											</span>
										</div>
									</button>
								))}
							</div>

							{/* Pagination */}
							{(packagesPagination.pages || 0) > 1 && (
								<div className="px-6 py-3 bg-white dark:bg-secondary-800 border-t border-secondary-200 dark:border-secondary-600 flex items-center justify-between">
									<div className="flex items-center gap-2">
										<span className="text-sm text-secondary-700 dark:text-white">
											{t("Rows per page:")}
										</span>
										<select
											value={packagesPageSize}
											onChange={(e) => {
												setPackagesPageSize(Number(e.target.value));
												setPackagesPage(1);
											}}
											className="text-sm border border-secondary-300 dark:border-secondary-600 rounded px-2 py-1 bg-white dark:bg-secondary-700 text-secondary-900 dark:text-white"
										>
											<option value={25}>25</option>
											<option value={50}>50</option>
											<option value={100}>100</option>
										</select>
									</div>
									<div className="flex items-center gap-2">
										<button
											type="button"
											onClick={() => setPackagesPage(packagesPage - 1)}
											disabled={packagesPage === 1}
											className="px-3 py-1 text-sm border border-secondary-300 dark:border-secondary-600 rounded disabled:opacity-50 disabled:cursor-not-allowed hover:bg-secondary-50 dark:hover:bg-secondary-700"
										>
											{t("Previous")}
										</button>
										<span className="text-sm text-secondary-700 dark:text-white">
											{t("Page {{page}} of {{pages}}", {
												page: packagesPage,
												pages: packagesPagination.pages,
											})}
										</span>
										<button
											type="button"
											onClick={() => setPackagesPage(packagesPage + 1)}
											disabled={packagesPage === packagesPagination.pages}
											className="px-3 py-1 text-sm border border-secondary-300 dark:border-secondary-600 rounded disabled:opacity-50 disabled:cursor-not-allowed hover:bg-secondary-50 dark:hover:bg-secondary-700"
										>
											{t("Next")}
										</button>
									</div>
								</div>
							)}
						</>
					)}
				</div>
			</div>
		</div>
	);
};

export default RepositoryDetail;
