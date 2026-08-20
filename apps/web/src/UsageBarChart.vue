<script setup lang="ts">
import { computed, ref } from 'vue';

export interface UsageSeriesDef {
  key: string;
  label: string;
  color: string;
}

export interface UsageStackedPeriod {
  period: string;
  values: Record<string, number>;
}

const props = defineProps<{
  title: string;
  periods: UsageStackedPeriod[];
  series: UsageSeriesDef[];
  formatValue: (value: number) => string;
  compact?: boolean;
}>();

const width = 640;
const height = props.compact ? 130 : 200;
const padding = props.compact
  ? { top: 10, right: 12, bottom: 22, left: 60 }
  : { top: 12, right: 12, bottom: 26, left: 60 };
const plotWidth = width - padding.left - padding.right;
const plotHeight = height - padding.top - padding.bottom;
const gap = 1;

function niceMax(value: number): number {
  if (value <= 0) return 1;
  const magnitude = 10 ** Math.floor(Math.log10(value));
  const normalized = value / magnitude;
  const step = normalized <= 1 ? 1 : normalized <= 2 ? 2 : normalized <= 5 ? 5 : 10;
  return step * magnitude;
}

function periodTotal(period: UsageStackedPeriod): number {
  return props.series.reduce((sum, item) => sum + (period.values[item.key] ?? 0), 0);
}

const activeSeries = computed(() => props.series.filter((item) => props.periods.some((period) => (period.values[item.key] ?? 0) > 0)));
const showLegend = computed(() => activeSeries.value.length >= 2);

const maxValue = computed(() => niceMax(Math.max(0, ...props.periods.map(periodTotal))));
const ticks = computed(() => [0, maxValue.value / 2, maxValue.value]);
const slotWidth = computed(() => (props.periods.length ? plotWidth / props.periods.length : plotWidth));
const barWidth = computed(() => Math.max(3, Math.min(24, slotWidth.value - 2)));

function tickY(tick: number): number {
  return padding.top + plotHeight - (tick / maxValue.value) * plotHeight;
}

function barX(index: number): number {
  return padding.left + index * slotWidth.value + (slotWidth.value - barWidth.value) / 2;
}

interface Segment {
  key: string;
  label: string;
  color: string;
  value: number;
  top: number;
  bottom: number;
  isTop: boolean;
}

function segmentsFor(period: UsageStackedPeriod): Segment[] {
  const yBase = padding.top + plotHeight;
  let cumulative = 0;
  const nonZero = props.series.filter((item) => (period.values[item.key] ?? 0) > 0);
  return nonZero.map((item, index) => {
    const value = period.values[item.key] ?? 0;
    const bottomValue = cumulative;
    const topValue = cumulative + value;
    cumulative = topValue;
    const isBottom = index === 0;
    const isTop = index === nonZero.length - 1;
    const rawTop = yBase - (topValue / maxValue.value) * plotHeight;
    const rawBottom = yBase - (bottomValue / maxValue.value) * plotHeight;
    return {
      key: item.key,
      label: item.label,
      color: item.color,
      value,
      top: rawTop + (isTop ? 0 : gap),
      bottom: rawBottom - (isBottom ? 0 : gap),
      isTop,
    };
  });
}

function segmentPath(segment: Segment, x: number, w: number): string {
  const h = Math.max(0, segment.bottom - segment.top);
  if (h <= 0) return '';
  return roundedTopRectPath(x, segment.top, w, h, segment.isTop ? Math.min(4, w / 2, h) : 0);
}

function roundedTopRectPath(x: number, y: number, w: number, h: number, r: number): string {
  if (r <= 0) return `M${x},${y} L${x + w},${y} L${x + w},${y + h} L${x},${y + h} Z`;
  return `M${x},${y + r} Q${x},${y} ${x + r},${y} L${x + w - r},${y} Q${x + w},${y} ${x + w},${y + r} L${x + w},${y + h} L${x},${y + h} Z`;
}

const labels = computed(() => {
  const count = props.periods.length;
  const step = count <= 8 ? 1 : Math.ceil(count / 6);
  const indexes = new Set<number>();
  for (let i = 0; i < count; i += step) indexes.add(i);
  if (count > 0) indexes.add(count - 1);
  return [...indexes].sort((a, b) => a - b).map((index) => ({ index, period: props.periods[index]!.period }));
});

const hoveredIndex = ref(-1);
const hoveredPeriod = computed(() => (hoveredIndex.value >= 0 ? props.periods[hoveredIndex.value] : undefined));
const hoveredBreakdown = computed(() => {
  if (!hoveredPeriod.value) return [];
  return props.series
    .map((item) => ({ ...item, value: hoveredPeriod.value!.values[item.key] ?? 0 }))
    .filter((item) => item.value > 0);
});
const tooltipLeftPercent = computed(() => {
  if (hoveredIndex.value < 0) return 50;
  return ((barX(hoveredIndex.value) + barWidth.value / 2) / width) * 100;
});

const showTable = ref(false);
</script>

<template>
  <section class="usage-chart" :class="{ compact }">
    <div class="usage-chart-head">
      <h3>{{ title }}</h3>
      <button type="button" class="usage-chart-table-toggle" @click="showTable = !showTable">
        {{ showTable ? 'グラフ表示' : '表で表示' }}
      </button>
    </div>
    <div v-if="showLegend" class="usage-chart-legend">
      <span v-for="item in activeSeries" :key="item.key" class="usage-chart-legend-item">
        <span class="usage-chart-legend-swatch" :style="{ background: item.color }" />{{ item.label }}
      </span>
    </div>
    <p v-if="!periods.length" class="usage-chart-empty">データがありません</p>
    <template v-else-if="showTable">
      <table class="usage-chart-table">
        <thead>
          <tr>
            <th>期間</th>
            <th v-for="item in activeSeries" :key="item.key">{{ item.label }}</th>
            <th v-if="activeSeries.length > 1">合計</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="period in periods" :key="period.period">
            <td>{{ period.period }}</td>
            <td v-for="item in activeSeries" :key="item.key">{{ formatValue(period.values[item.key] ?? 0) }}</td>
            <td v-if="activeSeries.length > 1">{{ formatValue(periodTotal(period)) }}</td>
          </tr>
        </tbody>
      </table>
    </template>
    <div v-else class="usage-chart-body">
      <svg :viewBox="`0 0 ${width} ${height}`" :style="{ height: `${height}px` }" preserveAspectRatio="none" role="img" :aria-label="title">
        <line
          v-for="tick in ticks"
          :key="tick"
          class="usage-chart-grid"
          :x1="padding.left"
          :x2="width - padding.right"
          :y1="tickY(tick)"
          :y2="tickY(tick)"
        />
        <text
          v-for="tick in ticks"
          :key="`label-${tick}`"
          class="usage-chart-axis-label"
          :x="padding.left - 8"
          :y="tickY(tick) + 3"
          text-anchor="end"
        >{{ formatValue(tick) }}</text>
        <template v-for="(period, index) in periods" :key="period.period">
          <path
            v-for="segment in segmentsFor(period)"
            :key="segment.key"
            :d="segmentPath(segment, barX(index), barWidth)"
            :fill="segment.color"
            :opacity="hoveredIndex === -1 || hoveredIndex === index ? 1 : 0.55"
          />
          <rect
            class="usage-chart-hit"
            :x="padding.left + index * slotWidth"
            :y="padding.top"
            :width="slotWidth"
            :height="plotHeight"
            @mouseenter="hoveredIndex = index"
            @mouseleave="hoveredIndex = -1"
          />
        </template>
        <text
          v-for="label in labels"
          :key="`x-${label.index}`"
          class="usage-chart-axis-label"
          :x="barX(label.index) + barWidth / 2"
          :y="height - 6"
          text-anchor="middle"
        >{{ label.period }}</text>
      </svg>
      <div v-if="hoveredPeriod" class="usage-chart-tooltip" :style="{ left: `${tooltipLeftPercent}%` }">
        <strong>{{ hoveredPeriod.period }}</strong>
        <span v-for="item in hoveredBreakdown" :key="item.key" class="usage-chart-tooltip-row">
          <span class="usage-chart-legend-swatch" :style="{ background: item.color }" />{{ item.label }}: {{ formatValue(item.value) }}
        </span>
        <span v-if="hoveredBreakdown.length > 1" class="usage-chart-tooltip-total">合計: {{ formatValue(periodTotal(hoveredPeriod)) }}</span>
      </div>
    </div>
  </section>
</template>
