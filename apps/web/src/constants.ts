import type { ReasoningEffort } from './api';

export const reasoningEffortOptions: Array<{ value: ReasoningEffort | ''; label: string }> = [
  { value: '', label: 'Default' },
  { value: 'low', label: 'Low' },
  { value: 'medium', label: 'Medium' },
  { value: 'high', label: 'High' },
  { value: 'xhigh', label: 'XHigh' },
  { value: 'max', label: 'Max' },
];
