import { AlertTriangle } from "lucide-react";
import { Trans, useTranslation } from "react-i18next";
import ModalPortal from "../../components/ui/ModalPortal";

// The host name is given to <Trans> as a component, not as a value: a value
// is parsed as markup with the translation, a component's content is not.
const QuotedHostName = ({ name }) => (
	<span className="font-semibold">"{name}"</span>
);

const DeleteConfirmationModal = ({
	host,
	isOpen,
	onClose,
	onConfirm,
	isLoading,
}) => {
	const { t } = useTranslation();
	if (!isOpen || !host) return null;

	const modal = (
		<div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-[120]">
			<div className="bg-white dark:bg-secondary-800 rounded-lg p-6 w-full max-w-md">
				<div className="flex items-center gap-3 mb-4">
					<div className="w-10 h-10 bg-danger-100 dark:bg-danger-900 rounded-full flex items-center justify-center">
						<AlertTriangle className="h-5 w-5 text-danger-600 dark:text-danger-400" />
					</div>
					<div>
						<h3 className="text-lg font-semibold text-secondary-900 dark:text-white">
							{t("Delete Host")}
						</h3>
						<p className="text-sm text-secondary-600 dark:text-white">
							{t("This action cannot be undone")}
						</p>
					</div>
				</div>

				<div className="mb-6">
					<p className="text-secondary-700 dark:text-white">
						<Trans
							i18nKey="Are you sure you want to delete the host <name/>?"
							components={{
								name: <QuotedHostName name={host.friendly_name} />,
							}}
						/>
					</p>
					<div className="mt-3 p-3 bg-danger-50 dark:bg-danger-900 border border-danger-200 dark:border-danger-700 rounded-md">
						<p className="text-sm text-danger-800 dark:text-danger-200">
							<Trans
								i18nKey="<strong>Warning:</strong> This will permanently remove the host and all its associated data, including package information and update history."
								components={{ strong: <strong /> }}
							/>
						</p>
					</div>
				</div>

				<div className="flex justify-end gap-3">
					<button
						type="button"
						onClick={onClose}
						className="btn-outline"
						disabled={isLoading}
					>
						{t("Cancel")}
					</button>
					<button
						type="button"
						onClick={onConfirm}
						className="btn-danger"
						disabled={isLoading}
					>
						{isLoading ? t("Deleting...") : t("Delete Host")}
					</button>
				</div>
			</div>
		</div>
	);

	return <ModalPortal>{modal}</ModalPortal>;
};

export default DeleteConfirmationModal;
