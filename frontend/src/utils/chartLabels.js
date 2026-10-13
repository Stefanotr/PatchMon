import { Chart as ChartJS } from "chart.js";

// Dashboard cards are narrow: a long chart label (an OS name, a compliance
// profile, an alert type, more so translated) was cut off at the edge of
// the canvas. These helpers shorten what the axis or the legend shows to
// what the chart's width leaves room for, with an ellipsis; the tooltip
// still shows the whole label.

// Average width of a character of the chart font (12px), in pixels.
const CHAR_PX = 6.5;

const charsIn = (px, max) =>
	Math.max(4, Math.min(max, Math.floor(px / CHAR_PX)));

export const shortLabel = (label, max) => {
	const text = String(label ?? "");
	return text.length > max ? `${text.slice(0, max - 1).trimEnd()}…` : text;
};

// categoryTicks is a tick callback for the category axis of a horizontal
// bar chart. Chart.js gives that axis at most half of the chart's width.
export const categoryTicks = (max = 24) =>
	function tick(value) {
		return shortLabel(
			this.getLabelForValue(value),
			charsIn(this.chart.width / 2 - 28, max),
		);
	};

// shortLegendLabels is a generateLabels for the legend of a pie or doughnut
// chart: Chart.js' own labels for that chart type, with shortened text.
export const shortLegendLabels =
	(max = 32) =>
	(chart) => {
		const own =
			ChartJS.overrides[chart.config.type]?.plugins?.legend?.labels
				?.generateLabels ??
			ChartJS.defaults.plugins.legend.labels.generateLabels;
		const fit = charsIn(chart.width - 48, max);
		return own(chart).map((item) => ({
			...item,
			text: shortLabel(item.text, fit),
		}));
	};
