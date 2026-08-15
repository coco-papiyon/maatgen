/* eslint-disable */
/**
 * Generated from packages/protocol/schema. Do not edit by hand.
 * Run `corepack pnpm --filter @maatgen/protocol generate` after changing a schema.
 */

/**
 * This interface was referenced by `ChangeSet`'s JSON-Schema
 * via the `definition` "restoreStatus".
 */
export type RestoreStatus = 'changed' | 'partially_restored' | 'restored' | 'conflict';
/**
 * This interface was referenced by `ChangeSet`'s JSON-Schema
 * via the `definition` "hunkStatus".
 */
export type HunkStatus = 'changed' | 'restored' | 'conflict';

export interface ChangeSet {
  sessionId: string;
  runId: string;
  checkpointId: string;
  beforeTree: string;
  afterTree: string;
  files: FileChange[];
}
/**
 * This interface was referenced by `ChangeSet`'s JSON-Schema
 * via the `definition` "fileChange".
 */
export interface FileChange {
  id: string;
  oldPath?: string;
  newPath?: string;
  kind: 'modify' | 'add' | 'delete' | 'rename' | 'binary' | 'mode_change';
  original?: string;
  modified?: string;
  restoreMode: 'hunk' | 'file';
  status: RestoreStatus;
  hunks: ChangeHunk[];
}
/**
 * This interface was referenced by `ChangeSet`'s JSON-Schema
 * via the `definition` "changeHunk".
 */
export interface ChangeHunk {
  id: string;
  oldStart: number;
  oldLines: number;
  newStart: number;
  newLines: number;
  originalText: string;
  modifiedText: string;
  status: HunkStatus;
}
