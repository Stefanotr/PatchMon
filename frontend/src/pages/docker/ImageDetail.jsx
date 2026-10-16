import { useQuery } from "@tanstack/react-query";
import {
	AlertTriangle,
	ArrowLeft,
	Container,
	ExternalLink,
	Package,
	RefreshCw,
	Server,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link, useParams } from "react-router-dom";
import UpdateStatusBadge, {
	CheckReason,
	ContainerState,
	RecreateNeededBadge,
	SourceName,
} from "../../components/docker/UpdateStatusBadge";
import api, { formatRelativeTime } from "../../utils/api";
import {
	generateRegistryLink,
	shortDigest,
	shortImageId,
} from "../../utils/docker";

const Digest = ({ value }) =>
	value ? (
		<span className="font-mono" title={value}>
			{shortDigest(value)}
		</span>
	) : (
		<span className="text-secondary-400">-</span>
	);

const ImageDetail = () => {
	const { t } = useTranslation();
	const { id } = useParams();

	const { data, isLoading, error } = useQuery({
		queryKey: ["docker", "image", id],
		queryFn: async () => {
			const response = await api.get(`/docker/images/${id}`);
			return response.data;
		},
		refetchInterval: 30000,
	});

	const image = data?.image;
	const hosts = data?.hosts || [];
	const containers = image?.docker_containers || [];
	const checks = image?.docker_image_checks || [];
	const hostsById = new Map(hosts.map((h) => [h.id, h]));
	const hostLabel = (hostId, host) => {
		const h = host || hostsById.get(hostId);
		return h?.friendly_name || h?.hostname || hostId;
	};

	if (isLoading) {
		return (
			<div className="flex items-center justify-center min-h-[calc(100vh-var(--app-main-inset))]">
				<RefreshCw className="h-8 w-8 animate-spin text-secondary-400" />
			</div>
		);
	}

	if (error || !image) {
		return (
			<div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
				<div className="bg-danger-50 dark:bg-danger-900/20 border border-danger-200 dark:border-danger-800 rounded-lg p-4">
					<div className="flex">
						<AlertTriangle className="h-5 w-5 text-danger-400" />
						<div className="ml-3">
							<h3 className="text-sm font-medium text-danger-800 dark:text-danger-200">
								{t("Image not found")}
							</h3>
						</div>
					</div>
				</div>
				<Link
					to="/docker"
					className="mt-4 inline-flex items-center text-primary-600 hover:text-primary-900"
				>
					<ArrowLeft className="h-4 w-4 mr-2" />
					{t("Back to Docker")}
				</Link>
			</div>
		);
	}

	const registryLink = generateRegistryLink(image.repository, image.source);
	const updateCheck = image.update_check;

	return (
		<div className="space-y-6">
			<div>
				<Link
					to="/docker"
					className="inline-flex items-center text-sm text-primary-600 hover:text-primary-900 dark:text-primary-400 dark:hover:text-primary-300 mb-4"
				>
					<ArrowLeft className="h-4 w-4 mr-2" />
					{t("Back to Docker")}
				</Link>
				<div className="flex items-start justify-between">
					<div className="flex items-center min-w-0">
						<Package className="h-8 w-8 text-secondary-400 mr-3 flex-shrink-0" />
						<div className="min-w-0">
							<h1 className="text-2xl font-bold text-secondary-900 dark:text-white [overflow-wrap:anywhere]">
								{image.repository}:{image.tag}
							</h1>
							<div className="mt-1 flex flex-wrap items-center gap-2 text-sm text-secondary-600 dark:text-white">
								<span>
									{t("Image ID: {{id}}", { id: shortImageId(image.image_id) })}
								</span>
								<UpdateStatusBadge
									status={image.update_status}
									checkedAt={updateCheck?.checked_at}
									reason={updateCheck?.reason}
								/>
								<RecreateNeededBadge
									count={image.containers_needing_recreate}
								/>
							</div>
						</div>
					</div>
				</div>
			</div>

			{/* Overview Cards */}
			<div className="grid grid-cols-1 gap-5 sm:grid-cols-2 lg:grid-cols-4">
				<div className="card p-4">
					<div className="flex items-center">
						<div className="flex-shrink-0">
							<Container className="h-5 w-5 text-green-600 mr-2" />
						</div>
						<div className="w-0 flex-1">
							<p className="text-sm text-secondary-500 dark:text-white">
								{t("Containers")}
							</p>
							<p className="text-xl font-semibold text-secondary-900 dark:text-white">
								{containers.length}
							</p>
						</div>
					</div>
				</div>

				<div className="card p-4">
					<div className="flex items-center">
						<div className="flex-shrink-0">
							<Server className="h-5 w-5 text-purple-600 mr-2" />
						</div>
						<div className="w-0 flex-1">
							<p className="text-sm text-secondary-500 dark:text-white">
								{t("Hosts")}
							</p>
							<p className="text-xl font-semibold text-secondary-900 dark:text-white">
								{hosts.length}
							</p>
						</div>
					</div>
				</div>

				<div className="card p-4">
					<div className="flex items-center">
						<div className="flex-shrink-0">
							<Package className="h-5 w-5 text-blue-600 mr-2" />
						</div>
						<div className="w-0 flex-1">
							<p className="text-sm text-secondary-500 dark:text-white">
								{t("Size")}
							</p>
							<p className="text-xl font-semibold text-secondary-900 dark:text-white">
								{image.size_bytes
									? t("{{size}} MB", {
											size: (Number(image.size_bytes) / 1024 / 1024).toFixed(0),
										})
									: t("N/A")}
							</p>
						</div>
					</div>
				</div>

				<div className="card p-4">
					<div className="flex items-center">
						<div className="flex-shrink-0">
							<AlertTriangle className="h-5 w-5 text-warning-600 mr-2" />
						</div>
						<div className="w-0 flex-1">
							<p className="text-sm text-secondary-500 dark:text-white">
								{t("Update Status")}
							</p>
							<div className="mt-1">
								<UpdateStatusBadge
									status={image.update_status}
									checkedAt={updateCheck?.checked_at}
									reason={updateCheck?.reason}
								/>
							</div>
							{updateCheck?.hosts_checked > 0 && (
								<p className="mt-1 text-xs text-secondary-500 dark:text-white">
									{t("{{behind}} of {{count}} host behind", {
										behind: updateCheck.hosts_update_available || 0,
										count: updateCheck.hosts_checked,
									})}
								</p>
							)}
						</div>
					</div>
				</div>
			</div>

			{/* Image Information */}
			<div className="card">
				<div className="px-6 py-5 border-b border-secondary-200 dark:border-secondary-700">
					<h3 className="text-lg leading-6 font-medium text-secondary-900 dark:text-white">
						{t("Image Information")}
					</h3>
				</div>
				<div className="px-6 py-5 space-y-6">
					<div className="grid grid-cols-1 gap-6 sm:grid-cols-2">
						<div>
							<dt className="text-sm font-medium text-secondary-500 dark:text-white">
								{t("Repository")}
							</dt>
							<dd className="mt-1 text-sm text-secondary-900 dark:text-white [overflow-wrap:anywhere]">
								{registryLink ? (
									<a
										href={registryLink}
										target="_blank"
										rel="noopener noreferrer"
										className="inline-flex items-center gap-1 text-primary-600 hover:text-primary-900 dark:text-primary-400 dark:hover:text-primary-300"
									>
										{image.repository}
										<ExternalLink className="h-3 w-3 flex-shrink-0" />
									</a>
								) : (
									image.repository
								)}
							</dd>
						</div>
						<div>
							<dt className="text-sm font-medium text-secondary-500 dark:text-white">
								{t("Tag")}
							</dt>
							<dd className="mt-1 text-sm text-secondary-900 dark:text-white [overflow-wrap:anywhere]">
								{image.tag}
							</dd>
						</div>
						<div>
							<dt className="text-sm font-medium text-secondary-500 dark:text-white">
								{t("Source")}
							</dt>
							<dd className="mt-1 text-sm text-secondary-900 dark:text-white">
								<SourceName source={image.source} />
							</dd>
						</div>
						<div>
							<dt className="text-sm font-medium text-secondary-500 dark:text-white">
								{t("Created")}
							</dt>
							<dd className="mt-1 text-sm text-secondary-900 dark:text-white">
								{image.created_at
									? formatRelativeTime(image.created_at)
									: t("Unknown")}
							</dd>
						</div>
						<div>
							<dt className="text-sm font-medium text-secondary-500 dark:text-white">
								{t("Image ID")}
							</dt>
							<dd className="mt-1 text-sm font-mono text-secondary-900 dark:text-white [overflow-wrap:anywhere]">
								{image.image_id}
							</dd>
						</div>
						<div>
							<dt className="text-sm font-medium text-secondary-500 dark:text-white">
								{t("Last Update Check")}
							</dt>
							<dd className="mt-1 text-sm text-secondary-900 dark:text-white">
								{updateCheck?.checked_at
									? formatRelativeTime(updateCheck.checked_at)
									: t("Never")}
							</dd>
						</div>
						{image.digest && (
							<div className="sm:col-span-2">
								<dt className="text-sm font-medium text-secondary-500 dark:text-white">
									{t("Digest")}
								</dt>
								<dd className="mt-1 text-sm font-mono text-secondary-900 dark:text-white [overflow-wrap:anywhere]">
									{image.digest}
								</dd>
							</div>
						)}
					</div>
				</div>
			</div>

			<div className="card">
				<div className="px-6 py-5 border-b border-secondary-200 dark:border-secondary-700">
					<h3 className="text-lg leading-6 font-medium text-secondary-900 dark:text-white">
						{t("Update Checks ({{count}})", { count: checks.length })}
					</h3>
				</div>
				{checks.length === 0 ? (
					<div className="text-center py-8 px-4">
						<p className="text-secondary-500 dark:text-white">
							{t("No update check has been reported for this image yet")}
						</p>
						<p className="text-sm text-secondary-400 dark:text-white mt-2">
							{t(
								"Agents check their images every few hours, or on demand with Check for updates.",
							)}
						</p>
					</div>
				) : (
					<>
						<div className="md:hidden p-4 space-y-3">
							{checks.map((check) => (
								<div key={check.host_id} className="card p-4 space-y-2 text-sm">
									<div className="flex items-center justify-between gap-2">
										<Link
											to={`/hosts/${check.host_id}`}
											className="font-semibold text-primary-600 dark:text-primary-400 min-h-[44px] inline-flex items-center"
										>
											{hostLabel(check.host_id, check.host)}
										</Link>
										<UpdateStatusBadge
											status={check.status}
											checkedAt={check.checked_at}
											reason={check.reason}
										/>
									</div>
									<div className="text-secondary-600 dark:text-white">
										{t("Local:")} <Digest value={check.local_digest} />
									</div>
									<div className="text-secondary-600 dark:text-white">
										{t("Registry:")} <Digest value={check.remote_digest} />
									</div>
									{check.reason && (
										<div className="text-secondary-500 dark:text-white text-xs">
											<CheckReason reason={check.reason} />
										</div>
									)}
									<div className="text-xs text-secondary-500 dark:text-white">
										{check.checked_at
											? t("Checked {{time}}", {
													time: formatRelativeTime(check.checked_at),
												})
											: t("Checked never")}
									</div>
								</div>
							))}
						</div>
						<div className="hidden md:block overflow-x-auto">
							<table className="min-w-full divide-y divide-secondary-200 dark:divide-secondary-600">
								<thead className="bg-secondary-50 dark:bg-secondary-700">
									<tr>
										{[
											t("Host"),
											t("Status"),
											t("Local Digest"),
											t("Registry Digest"),
											t("Reason"),
											t("Checked"),
										].map((label) => (
											<th
												key={label}
												scope="col"
												className="px-3 sm:px-4 py-2 text-left text-xs font-medium text-secondary-500 dark:text-white uppercase tracking-wider whitespace-nowrap"
											>
												{label}
											</th>
										))}
									</tr>
								</thead>
								<tbody className="bg-white dark:bg-secondary-800 divide-y divide-secondary-200 dark:divide-secondary-600">
									{checks.map((check) => (
										<tr
											key={check.host_id}
											className="hover:bg-secondary-50 dark:hover:bg-secondary-700 transition-colors"
										>
											<td className="px-3 sm:px-4 py-2 whitespace-nowrap text-sm">
												<Link
													to={`/hosts/${check.host_id}`}
													className="font-medium text-primary-600 hover:text-primary-900 dark:text-primary-400 dark:hover:text-primary-300"
												>
													{hostLabel(check.host_id, check.host)}
												</Link>
											</td>
											<td className="px-3 sm:px-4 py-2 whitespace-nowrap text-sm">
												<UpdateStatusBadge
													status={check.status}
													checkedAt={check.checked_at}
													reason={check.reason}
												/>
											</td>
											<td className="px-3 sm:px-4 py-2 whitespace-nowrap text-sm text-secondary-900 dark:text-white">
												<Digest value={check.local_digest} />
											</td>
											<td className="px-3 sm:px-4 py-2 whitespace-nowrap text-sm text-secondary-900 dark:text-white">
												<Digest value={check.remote_digest} />
											</td>
											<td className="px-3 sm:px-4 py-2 text-sm text-secondary-500 dark:text-white">
												{check.reason ? (
													<CheckReason reason={check.reason} />
												) : (
													"-"
												)}
											</td>
											<td className="px-3 sm:px-4 py-2 whitespace-nowrap text-sm text-secondary-500 dark:text-white">
												{check.checked_at
													? formatRelativeTime(check.checked_at)
													: t("Never")}
											</td>
										</tr>
									))}
								</tbody>
							</table>
						</div>
					</>
				)}
			</div>

			{/* Containers using this image */}
			<div className="card">
				<div className="px-6 py-5 border-b border-secondary-200 dark:border-secondary-700">
					<h3 className="text-lg leading-6 font-medium text-secondary-900 dark:text-white">
						{t("Containers ({{count}})", { count: containers.length })}
					</h3>
				</div>
				{containers.length === 0 ? (
					<div className="text-center py-8 px-4">
						<p className="text-secondary-500 dark:text-white">
							{t("No containers use this image")}
						</p>
					</div>
				) : (
					<>
						<div className="md:hidden p-4 space-y-3">
							{containers.map((container) => (
								<div key={container.id} className="card p-4 space-y-2">
									<div className="flex items-center justify-between gap-2">
										<Link
											to={`/docker/containers/${container.id}`}
											className="text-base font-semibold text-primary-600 dark:text-primary-400 min-h-[44px] inline-flex items-center"
										>
											{container.name}
										</Link>
										<span className="badge-secondary w-fit">
											<ContainerState state={container.status} />
										</span>
									</div>
									<div className="flex items-center justify-between gap-2 text-sm text-secondary-500 dark:text-white">
										<span>{hostLabel(container.host_id, container.host)}</span>
										<UpdateStatusBadge status={container.update_status} />
									</div>
								</div>
							))}
						</div>
						<div className="hidden md:block overflow-x-auto">
							<table className="min-w-full divide-y divide-secondary-200 dark:divide-secondary-600">
								<thead className="bg-secondary-50 dark:bg-secondary-700">
									<tr>
										{[
											t("Container Name"),
											t("Status"),
											t("Host"),
											t("Update Status"),
										].map((label) => (
											<th
												key={label}
												scope="col"
												className="px-3 sm:px-4 py-2 text-left text-xs font-medium text-secondary-500 dark:text-white uppercase tracking-wider whitespace-nowrap"
											>
												{label}
											</th>
										))}
										<th
											scope="col"
											className="px-3 sm:px-4 py-2 text-left text-xs font-medium text-secondary-500 dark:text-white uppercase tracking-wider"
										>
											{t("Actions")}
										</th>
									</tr>
								</thead>
								<tbody className="bg-white dark:bg-secondary-800 divide-y divide-secondary-200 dark:divide-secondary-600">
									{containers.map((container) => (
										<tr
											key={container.id}
											className="hover:bg-secondary-50 dark:hover:bg-secondary-700 transition-colors"
										>
											<td className="px-3 sm:px-4 py-2 whitespace-nowrap">
												<Link
													to={`/docker/containers/${container.id}`}
													className="text-sm font-medium text-primary-600 hover:text-primary-900 dark:text-primary-400 dark:hover:text-primary-300"
												>
													{container.name}
												</Link>
											</td>
											<td className="px-3 sm:px-4 py-2 whitespace-nowrap">
												<span className="badge-secondary">
													<ContainerState state={container.status} />
												</span>
											</td>
											<td className="px-3 sm:px-4 py-2 whitespace-nowrap text-sm">
												<Link
													to={`/hosts/${container.host_id}`}
													className="text-primary-600 hover:text-primary-900 dark:text-primary-400 dark:hover:text-primary-300"
												>
													{hostLabel(container.host_id, container.host)}
												</Link>
											</td>
											<td className="px-3 sm:px-4 py-2 whitespace-nowrap">
												<UpdateStatusBadge status={container.update_status} />
											</td>
											<td className="px-3 sm:px-4 py-2 whitespace-nowrap text-sm font-medium">
												<Link
													to={`/docker/containers/${container.id}`}
													className="text-primary-600 hover:text-primary-900 dark:text-primary-400 dark:hover:text-primary-300 inline-flex items-center"
												>
													{t("View")}
													<ExternalLink className="ml-1 h-4 w-4" />
												</Link>
											</td>
										</tr>
									))}
								</tbody>
							</table>
						</div>
					</>
				)}
			</div>

			<div className="card">
				<div className="px-6 py-5 border-b border-secondary-200 dark:border-secondary-700">
					<h3 className="text-lg leading-6 font-medium text-secondary-900 dark:text-white">
						{t("Hosts ({{count}})", { count: hosts.length })}
					</h3>
				</div>
				<div className="md:hidden p-4 space-y-3">
					{hosts.map((host) => (
						<div key={host.id} className="card p-4">
							<Link
								to={`/hosts/${host.id}`}
								className="text-base font-semibold text-primary-600 dark:text-primary-400 min-h-[44px] inline-flex items-center"
							>
								{host.friendly_name || host.hostname}
							</Link>
							{host.ip && (
								<p className="text-sm text-secondary-500 dark:text-white font-mono">
									{host.ip}
								</p>
							)}
						</div>
					))}
				</div>
				<div className="hidden md:block overflow-x-auto">
					<table className="min-w-full divide-y divide-secondary-200 dark:divide-secondary-600">
						<thead className="bg-secondary-50 dark:bg-secondary-700">
							<tr>
								{[t("Host Name"), t("IP Address"), t("Actions")].map(
									(label) => (
										<th
											key={label}
											scope="col"
											className="px-3 sm:px-4 py-2 text-left text-xs font-medium text-secondary-500 dark:text-white uppercase tracking-wider"
										>
											{label}
										</th>
									),
								)}
							</tr>
						</thead>
						<tbody className="bg-white dark:bg-secondary-800 divide-y divide-secondary-200 dark:divide-secondary-600">
							{hosts.map((host) => (
								<tr
									key={host.id}
									className="hover:bg-secondary-50 dark:hover:bg-secondary-700 transition-colors"
								>
									<td className="px-3 sm:px-4 py-2 whitespace-nowrap">
										<Link
											to={`/hosts/${host.id}`}
											className="text-sm font-medium text-primary-600 hover:text-primary-900 dark:text-primary-400 dark:hover:text-primary-300"
										>
											{host.friendly_name || host.hostname}
										</Link>
									</td>
									<td className="px-3 sm:px-4 py-2 whitespace-nowrap text-sm font-mono text-secondary-500 dark:text-white">
										{host.ip || "-"}
									</td>
									<td className="px-3 sm:px-4 py-2 whitespace-nowrap text-sm font-medium">
										<Link
											to={`/hosts/${host.id}`}
											className="text-primary-600 hover:text-primary-900 dark:text-primary-400 dark:hover:text-primary-300 inline-flex items-center"
										>
											{t("View")}
											<ExternalLink className="ml-1 h-4 w-4" />
										</Link>
									</td>
								</tr>
							))}
						</tbody>
					</table>
				</div>
			</div>
		</div>
	);
};

export default ImageDetail;
