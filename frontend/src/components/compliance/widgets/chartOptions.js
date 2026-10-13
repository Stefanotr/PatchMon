import { categoryTicks, shortLegendLabels } from "../../../utils/chartLabels";

/**
 * Shared Chart.js options for compliance widgets (theme-aware).
 * Use from widgets that use useTheme() and pass isDark.
 */
export function getDoughnutOptions(is_dark, is_mobile = false) {
	return {
		responsive: true,
		maintainAspectRatio: false,
		cutout: "70%",
		elements: {
			arc: { borderRadius: 5 },
		},
		plugins: {
			// Below the chart: the cards are narrow, and a legend beside the
			// chart cut its labels off (more so translated ones).
			legend: {
				position: "bottom",
				labels: {
					color: is_dark ? "#ffffff" : "#374151",
					font: { size: is_mobile ? 10 : 11 },
					padding: 6,
					usePointStyle: true,
					pointStyle: "circle",
					generateLabels: shortLegendLabels(),
				},
			},
		},
	};
}

export function getBarOptions(is_dark, index_axis = "x") {
	return {
		responsive: true,
		maintainAspectRatio: false,
		indexAxis: index_axis,
		plugins: {
			legend: {
				display: index_axis === "y",
				position: "top",
				labels: {
					color: is_dark ? "#ffffff" : "#374151",
					font: { size: 12 },
					padding: 15,
					usePointStyle: true,
				},
			},
		},
		scales: {
			x: {
				stacked: index_axis === "y",
				ticks: { color: is_dark ? "#ffffff" : "#374151", font: { size: 12 } },
				grid: { color: is_dark ? "#374151" : "#e5e7eb" },
			},
			y: {
				stacked: index_axis === "y",
				ticks: {
					color: is_dark ? "#ffffff" : "#374151",
					font: { size: 12 },
					// Categories on this axis: shorten long profile names.
					...(index_axis === "y" ? { callback: categoryTicks() } : {}),
				},
				grid: { color: is_dark ? "#374151" : "#e5e7eb" },
			},
		},
	};
}

export function getPieOptions(is_dark) {
	return {
		responsive: true,
		maintainAspectRatio: false,
		elements: {
			arc: { borderRadius: 5 },
		},
		plugins: {
			legend: {
				position: "bottom",
				labels: {
					color: is_dark ? "#ffffff" : "#374151",
					font: { size: 11 },
					padding: 6,
					usePointStyle: true,
					pointStyle: "circle",
					generateLabels: shortLegendLabels(),
				},
			},
		},
	};
}
