// Copyright (c) 2026 Vassel Goleyu. MIT License.
// Modular ECharts: only what the level chart needs. This module is imported lazily by Chart.svelte,
// so it stays out of the initial bundle and out of the map page.
import { LineChart } from 'echarts/charts';
import {
	AriaComponent,
	GridComponent,
	MarkAreaComponent,
	MarkLineComponent,
	TooltipComponent
} from 'echarts/components';
import * as echarts from 'echarts/core';
import { CanvasRenderer } from 'echarts/renderers';

echarts.use([
	LineChart,
	GridComponent,
	TooltipComponent,
	MarkLineComponent,
	MarkAreaComponent,
	AriaComponent,
	CanvasRenderer
]);

export { echarts };
