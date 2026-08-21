import * as vscode from 'vscode';
import type { ChangeFile, ChangeSet } from './agent-manager-client.js';

const SCHEME = 'maatgen-checkpoint';

interface DocumentEntry {
  content: string;
  file: ChangeFile;
  side: 'before' | 'after';
  sessionId: string;
  checkpointId: string;
}

export class CheckpointDocumentProvider implements vscode.TextDocumentContentProvider, vscode.CodeLensProvider {
  static readonly scheme = SCHEME;
  private readonly documents = new Map<string, DocumentEntry>();
  private readonly codeLensEmitter = new vscode.EventEmitter<void>();
  readonly onDidChangeCodeLenses = this.codeLensEmitter.event;

  createDiffUris(changeSet: ChangeSet, file: ChangeFile): { before: vscode.Uri; after: vscode.Uri } {
    const displayPath = file.newPath ?? file.oldPath ?? file.id;
    const before = this.createUri(changeSet, file, 'before', displayPath);
    const after = this.createUri(changeSet, file, 'after', displayPath);
    return { before, after };
  }

  updateChangeSet(changeSet: ChangeSet): void {
    const files = new Map(changeSet.files.map((file) => [file.id, file]));
    for (const [key, entry] of this.documents) {
      if (entry.sessionId !== changeSet.sessionId || entry.checkpointId !== changeSet.checkpointId) continue;
      const file = files.get(entry.file.id);
      if (file) this.documents.set(key, { ...entry, file });
    }
    this.codeLensEmitter.fire();
  }

  provideTextDocumentContent(uri: vscode.Uri): string {
    return this.documents.get(uri.toString())?.content ?? '';
  }

  provideCodeLenses(document: vscode.TextDocument): vscode.CodeLens[] {
    const entry = this.documents.get(document.uri.toString());
    if (!entry || entry.side !== 'after') return [];
    if (entry.file.restoreMode === 'file') {
      if (entry.file.status === 'restored') return [];
      return [new vscode.CodeLens(new vscode.Range(0, 0, 0, 0), {
        title: '$(discard) Restore file to checkpoint',
        command: 'maatgen.restoreFile',
        arguments: [entry.file.id],
      })];
    }
    return entry.file.hunks
      .filter((hunk) => hunk.status !== 'restored')
      .map((hunk) => new vscode.CodeLens(new vscode.Range(Math.max(0, hunk.newStart - 1), 0, Math.max(0, hunk.newStart - 1), 0), {
        title: hunk.status === 'conflict' ? '$(warning) Restore conflict' : '$(discard) Restore hunk to checkpoint',
        command: 'maatgen.restoreHunk',
        arguments: [hunk.id],
      }));
  }

  dispose(): void {
    this.documents.clear();
    this.codeLensEmitter.dispose();
  }

  private createUri(changeSet: ChangeSet, file: ChangeFile, side: 'before' | 'after', displayPath: string): vscode.Uri {
    const uri = vscode.Uri.from({
      scheme: SCHEME,
      authority: 'checkpoint',
      path: `/${encodeURIComponent(changeSet.checkpointId)}/${side}/${encodeURIComponent(file.id)}/${displayPath}`,
    });
    this.documents.set(uri.toString(), {
      content: side === 'before' ? (file.original ?? '') : (file.modified ?? ''),
      file,
      side,
      sessionId: changeSet.sessionId,
      checkpointId: changeSet.checkpointId,
    });
    return uri;
  }
}
