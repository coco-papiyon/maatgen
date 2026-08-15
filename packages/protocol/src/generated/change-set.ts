/* eslint-disable */
/**
 * Generated from packages/protocol/schema. Do not edit by hand.
 * Run `corepack pnpm --filter @maatgen/protocol generate` after changing a schema.
 */

/**
 * This interface was referenced by `ChangeSet`'s JSON-Schema
 * via the `definition` "reviewStatus".
 */
export type ReviewStatus = 'pending' | 'partially_accepted' | 'accepted' | 'rejected';
/**
 * This interface was referenced by `ChangeSet`'s JSON-Schema
 * via the `definition` "hunkStatus".
 */
export type HunkStatus = 'pending' | 'accepted' | 'rejected';

export interface ChangeSet {
  sessionId: string;
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
  reviewMode: 'hunk' | 'file';
  status: ReviewStatus;
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
