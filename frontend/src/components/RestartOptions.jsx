import {
	REBOOT_MAX_DELAY_MINUTES,
	REBOOT_MAX_MESSAGE_LENGTH,
	REBOOT_MIN_DELAY_MINUTES,
	restartChoiceError,
} from "../utils/patchingApi";

const inputClass =
	"text-sm rounded border border-secondary-300 dark:border-secondary-600 bg-white dark:bg-secondary-800 text-secondary-900 dark:text-white py-1 px-2";

/**
 * Restart choice: whether (when showMode), when, and the message shown to
 * signed-in users. value/onChange hold a DEFAULT_RESTART_CHOICE-shaped object.
 */
export default function RestartOptions({ value, onChange, showMode = true }) {
	const set = (patch) => onChange({ ...value, ...patch });
	const enabled = !showMode || value.mode !== "never";
	const error = enabled
		? restartChoiceError(showMode ? value : { ...value, mode: "always" })
		: null;

	return (
		<div className="space-y-3">
			{showMode && (
				<label className="block">
					<span className="text-xs font-medium text-secondary-500 dark:text-secondary-400">
						Restart after patching
					</span>
					<select
						value={value.mode}
						onChange={(e) => set({ mode: e.target.value })}
						className={`mt-1 w-full ${inputClass}`}
					>
						<option value="never">Never (restart by hand)</option>
						<option value="if_needed">If the host needs a restart</option>
						<option value="always">Always</option>
					</select>
				</label>
			)}

			{enabled && (
				<>
					<fieldset className="space-y-2">
						<legend className="text-xs font-medium text-secondary-500 dark:text-secondary-400">
							When
						</legend>
						<label className="flex items-center gap-2 text-sm text-secondary-700 dark:text-secondary-300">
							<input
								type="radio"
								checked={value.when !== "at"}
								onChange={() => set({ when: "delay" })}
							/>
							In
							<input
								type="number"
								min={REBOOT_MIN_DELAY_MINUTES}
								max={REBOOT_MAX_DELAY_MINUTES}
								value={value.delayMinutes}
								disabled={value.when === "at"}
								onChange={(e) => set({ delayMinutes: e.target.value })}
								className={`w-20 ${inputClass}`}
								aria-label="Delay in minutes"
							/>
							minutes{showMode ? " after the run" : ""}
						</label>
						<label className="flex items-center gap-2 text-sm text-secondary-700 dark:text-secondary-300">
							<input
								type="radio"
								checked={value.when === "at"}
								onChange={() => set({ when: "at" })}
							/>
							At
							<input
								type="time"
								value={value.atTime}
								disabled={value.when !== "at"}
								onChange={(e) => set({ atTime: e.target.value })}
								className={inputClass}
								aria-label="Time of day"
							/>
							<span className="text-xs text-secondary-500 dark:text-secondary-400">
								(your local time, next occurrence)
							</span>
						</label>
					</fieldset>

					<label className="block">
						<span className="text-xs font-medium text-secondary-500 dark:text-secondary-400">
							Message for signed-in users (optional)
						</span>
						<input
							type="text"
							maxLength={REBOOT_MAX_MESSAGE_LENGTH}
							value={value.message}
							onChange={(e) => set({ message: e.target.value })}
							placeholder="This computer restarts to finish installing updates"
							className={`mt-1 w-full ${inputClass}`}
						/>
					</label>

					{error && (
						<p className="text-xs text-danger-600 dark:text-danger-400">
							{error}
						</p>
					)}
				</>
			)}
		</div>
	);
}
