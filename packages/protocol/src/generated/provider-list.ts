/* eslint-disable */
/**
 * Generated from packages/protocol/schema. Do not edit by hand.
 * Run `corepack pnpm --filter @maatgen/protocol generate` after changing a schema.
 */

export interface ProviderListResponse {
  providers: {
    id: 'codex' | 'claude' | 'copilot';
    label: string;
    models: string[];
    defaultModel?: string;
  }[];
}
