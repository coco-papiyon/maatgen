/* eslint-disable */
/**
 * Generated from packages/protocol/schema. Do not edit by hand.
 * Run `corepack pnpm --filter @maatgen/protocol generate` after changing a schema.
 */

export interface UsageSummary {
  granularity: 'day' | 'week' | 'month';
  provider?: 'codex' | 'claude' | 'copilot';
  model?: string;
  seriesBy: 'provider' | 'model';
  periods: UsagePeriod[];
}
/**
 * This interface was referenced by `UsageSummary`'s JSON-Schema
 * via the `definition` "usagePeriod".
 */
export interface UsagePeriod {
  period: string;
  costUsd: number;
  aiCredits: number;
  totalTokens: number;
  inputTokens: number;
  cachedInputTokens: number;
  outputTokens: number;
  reasoningOutputTokens: number;
  series: UsageSeriesPoint[];
}
/**
 * This interface was referenced by `UsageSummary`'s JSON-Schema
 * via the `definition` "usageSeriesPoint".
 */
export interface UsageSeriesPoint {
  key: string;
  costUsd: number;
  aiCredits: number;
  totalTokens: number;
}
