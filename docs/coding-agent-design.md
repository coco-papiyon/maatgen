# Coding Agent VS Code Extension 設計書

## 1. 概要

本システムは、VS Code 上から複数の Coding Agent を利用できる拡張機能を提供する。

対象とする Coding Agent は以下を想定する。

- OpenAI Codex CLI
- Claude Code
- GitHub Copilot CLI
- 将来的に追加されるその他の CLI 型 Coding Agent

VS Code 拡張機能自身は AI 推論を行わず、Coding Agent の実行・監視・ログ収集を担当する **Agent Manager** と通信する。

また、UI は VS Code Webview 専用にはせず、通常の Web アプリケーションとして単独起動できる構成とする。  
これにより、日常的な UI 開発・動作確認はブラウザ上で行い、VS Code 固有機能のみ Extension Development Host で確認できるようにする。

---

## 2. 目的

本システムの主な目的は以下である。

1. VS Code 上から Coding Agent に自然言語で作業を指示する。
2. Codex / Claude Code / GitHub Copilot CLI などをバックグラウンドプロセスとして実行する。
3. Agent に与えた指示内容を記録する。
4. Agent が行った操作を記録する。
5. Agent のトークン使用量を可能な範囲で記録する。
6. Agent によるソースコード変更を差分として表示する。
7. 修正ブロック（Hunk）単位で Accept / Reject を行えるようにする。
8. Agent ごとの実行履歴、成功率、トークン使用量等を後から確認できるようにする。
9. UI を Web アプリとして単独起動し、VS Code を起動せずに開発・確認できるようにする。
10. VS Code Marketplace を利用せず、VSIX により限定配布できるようにする。

---

## 3. システム構成

```text
                           ┌─────────────────────────┐
                           │       Web Browser       │
                           │                         │
                           │ Vue 3 + TypeScript      │
                           │ Vite                    │
                           └────────────┬────────────┘
                                        │
                                  HTTP / WebSocket
                                        │
                                        ▼
┌─────────────────────────┐    ┌────────────────────────────┐
│       VS Code           │    │       Agent Manager        │
│                         │    │            Go              │
│ VS Code Extension       │    │                            │
│      │                  │    │ Session Manager            │
│      ▼                  │    │ Event Normalizer           │
│ Webview                 │◀──▶│ Process Manager            │
│ Vue 3 + TypeScript      │    │ Change Detector            │
│                         │    │ Usage Collector            │
└─────────────────────────┘    │ Logger                     │
                               └─────────────┬──────────────┘
                                             │
                     ┌───────────────────────┼──────────────────────┐
                     │                       │                      │
                     ▼                       ▼                      ▼
               ┌───────────┐          ┌─────────────┐       ┌─────────────┐
               │ Codex CLI │          │ Claude Code │       │ Copilot CLI │
               └───────────┘          └─────────────┘       └─────────────┘
                                             │
                                             ▼
                                      Workspace / Git
                                             │
                                             ▼
                                           SQLite
```

---

## 4. 技術スタック

### 4.1 フロントエンド

| 項目 | 採用技術 |
|---|---|
| Framework | Vue 3 |
| Language | TypeScript |
| Build Tool | Vite |
| State Management | Pinia |
| Editor / Diff | Monaco Editor |
| Markdown | markdown-it |
| 通信 | Fetch API / WebSocket |
| Styling | CSS / VS Code Theme Variables |

### 4.2 VS Code Extension

| 項目 | 採用技術 |
|---|---|
| Language | TypeScript |
| Runtime | Node.js Extension Host |
| UI | Webview |
| VS Code連携 | VS Code Extension API |
| Diff補助 | vscode.diff / Decoration / CodeLens |
| 配布 | VSIX |

### 4.3 Agent Manager

| 項目 | 採用技術 |
|---|---|
| Language | Go |
| HTTP API | net/http または軽量Router |
| Realtime | WebSocket |
| Process Execution | os/exec |
| Storage | SQLite |
| Git Integration | git CLI |
| Log Format | 共通Event + Raw JSONL |

---

## 5. リポジトリ構成

Monorepo 構成を採用する。

```text
coding-agent/
├─ apps/
│  ├─ web/
│  │  ├─ src/
│  │  └─ vite.config.ts
│  │
│  ├─ vscode/
│  │  ├─ src/
│  │  │  ├─ extension.ts
│  │  │  ├─ webviewProvider.ts
│  │  │  ├─ commands/
│  │  │  └─ vscodeBridge/
│  │  └─ package.json
│  │
│  └─ agent-manager/
│     ├─ cmd/
│     ├─ internal/
│     │  ├─ agent/
│     │  │  ├─ codex/
│     │  │  ├─ claude/
│     │  │  └─ copilot/
│     │  ├─ session/
│     │  ├─ event/
│     │  ├─ diff/
│     │  ├─ git/
│     │  ├─ storage/
│     │  └─ server/
│     └─ go.mod
│
├─ packages/
│  ├─ ui/
│  │  ├─ components/
│  │  ├─ views/
│  │  └─ composables/
│  │
│  ├─ protocol/
│  │  ├─ session.ts
│  │  ├─ event.ts
│  │  ├─ change.ts
│  │  └─ usage.ts
│  │
│  └─ api-client/
│     ├─ agentApi.ts
│     ├─ httpAgentApi.ts
│     └─ vscodeAgentApi.ts
│
└─ package.json
```

---

## 6. コンポーネント責務

### 6.1 Web UI

Web UI は通常のブラウザアプリケーションとして動作する。

主な責務は以下。

- Agent 選択
- Prompt 入力
- Agent 実行開始
- Agent 停止
- 実行状況表示
- Agent メッセージ表示
- Command / Tool 実行履歴表示
- Token 使用量表示
- Changed Files 表示
- Diff 表示
- Hunk 単位の Accept / Reject
- Session 履歴表示
- ログ閲覧

Web UI から VS Code API を直接呼び出してはならない。

---

## 7. API 抽象化

UI は実行環境を意識しないように API を抽象化する。

```ts
export interface AgentApi {
  startSession(request: StartSessionRequest): Promise<AgentSession>;

  sendMessage(
    sessionId: string,
    message: string
  ): Promise<void>;

  cancelSession(
    sessionId: string
  ): Promise<void>;

  getSession(
    sessionId: string
  ): Promise<AgentSession>;

  getChanges(
    sessionId: string
  ): Promise<FileChange[]>;

  acceptHunk(
    sessionId: string,
    hunkId: string
  ): Promise<void>;

  rejectHunk(
    sessionId: string,
    hunkId: string
  ): Promise<void>;
}
```

実装は以下の2種類を用意する。

```text
AgentApi
  ├─ HttpAgentApi
  │     └─ Webブラウザ用
  │
  └─ VsCodeAgentApi
        └─ VS Code Webview用
```

将来的には VS Code Webview から直接 Agent Manager の HTTP API を利用する構成も可能とする。

---

## 8. Agent Manager

Agent Manager は本システムの中核とする。

VS Code Extension から各 CLI を直接起動せず、原則としてすべての Coding Agent を Agent Manager 経由で実行する。

### 主な責務

```text
Agent Manager
├─ Agent process 起動
├─ Agent process 停止
├─ Session 管理
├─ CLI JSON / JSONL 解析
├─ 共通Event形式への変換
├─ Token usage 収集
├─ Git差分取得
├─ ChangeSet生成
├─ ログ保存
└─ Web UI / VS Codeへのイベント配信
```

---

## 9. Agent Adapter

CLI ごとの差異を Adapter に閉じ込める。

```go
type CodingAgent interface {
    Start(ctx context.Context, req StartRequest) (*Session, error)
    Send(ctx context.Context, sessionID string, message string) error
    Cancel(ctx context.Context, sessionID string) error
}
```

実装例：

```text
CodingAgent
├─ CodexAgent
├─ ClaudeAgent
└─ CopilotAgent
```

各 Adapter は CLI 固有の以下を処理する。

- コマンドライン引数
- JSON / JSONL 出力形式
- セッション継続方法
- Token Usage
- Tool / Command イベント
- Error 表現
- 終了コード

---

## 10. Session

1回の Agent 作業を Session として管理する。

```ts
export interface AgentSession {
  id: string;

  agent: 'codex' | 'claude' | 'copilot';

  model?: string;

  workspace: string;

  repository?: string;

  branch?: string;

  status:
    | 'starting'
    | 'running'
    | 'waiting'
    | 'completed'
    | 'failed'
    | 'cancelled';

  startedAt: string;

  finishedAt?: string;

  usage?: TokenUsage;
}
```

---

## 11. 共通イベントモデル

各 CLI の出力を共通形式に正規化する。

```ts
export interface AgentEvent {
  id: string;

  sessionId: string;

  sequence: number;

  timestamp: string;

  agent:
    | 'codex'
    | 'claude'
    | 'copilot';

  type:
    | 'user_prompt'
    | 'assistant_message'
    | 'reasoning'
    | 'tool_call'
    | 'tool_result'
    | 'command'
    | 'command_result'
    | 'file_read'
    | 'file_write'
    | 'file_edit'
    | 'usage'
    | 'change_detected'
    | 'error'
    | 'completed';

  data: unknown;
}
```

CLI の生データも別途保存する。

```text
CLI Raw JSONL
      │
      ├──────────────▶ raw_events
      │
      ▼
Agent Adapter
      │
      ▼
AgentEvent
      │
      ▼
events
```

Raw Event を保存することで、将来的な CLI フォーマット変更や再解析に対応する。

---

## 12. Token Usage

Agent ごとに取得可能な情報が異なるため、Token Usage の各項目は Optional とする。

```ts
export interface TokenUsage {
  inputTokens?: number;

  cachedInputTokens?: number;

  outputTokens?: number;

  totalTokens?: number;

  source:
    | 'cli'
    | 'api'
    | 'estimated'
    | 'unknown';
}
```

Token 使用量は Session 単位だけでなく、可能な場合は Event 単位でも記録する。

---

## 13. Agent 操作ログ

以下を可能な範囲で記録する。

### Prompt

```text
user_prompt
```

### Agent response

```text
assistant_message
```

### Command

例：

```text
git status
npm test
go test ./...
grep -R "authenticate" src/
```

### File operation

```text
file_read
file_write
file_edit
```

### Tool Call

Agent 固有 Tool の実行。

### Git Changes

Agent が申告した内容だけでなく、実際の Git Working Tree から取得する。

Agent 実行前：

```bash
git rev-parse HEAD
git status --porcelain
```

Agent 実行後：

```bash
git status --porcelain
git diff --stat
git diff
```

---

## 14. Source Change Review

Agent が変更したソースコードをそのまま確定せず、ChangeSet として管理する。

```text
Agent
  │
  ▼
Source Modification
  │
  ▼
Change Detector
  │
  ▼
ChangeSet
  │
  ├─ File A
  │   ├─ Hunk 1
  │   └─ Hunk 2
  │
  └─ File B
      └─ Hunk 3
```

---

## 15. ChangeSet データモデル

```ts
export interface ChangeSet {
  sessionId: string;

  files: FileChange[];
}

export interface FileChange {
  path: string;

  original: string;

  modified: string;

  hunks: ChangeHunk[];
}

export interface ChangeHunk {
  id: string;

  oldStart: number;
  oldLines: number;

  newStart: number;
  newLines: number;

  originalText: string;

  modifiedText: string;

  status:
    | 'pending'
    | 'accepted'
    | 'rejected';
}
```

---

## 16. Accept / Reject

ユーザーは Hunk 単位で変更を承認できる。

```text
foo.ts

Hunk 1
──────────────
- return a - b;
+ return a + b;

[Accept] [Reject]


Hunk 2
──────────────
- const x = 1;
+ const x = 2;

[Accept] [Reject]
```

以下も提供する。

- Accept
- Reject
- Accept All
- Reject All
- Next Change
- Previous Change

Review 操作も Event として記録する。

```json
{
  "type": "change_review",
  "sessionId": "session-123",
  "file": "src/foo.ts",
  "hunkId": "hunk-2",
  "action": "accept"
}
```

---

## 17. Diff 表示

### Web版

Monaco Editor の Diff Editor を利用する。

```text
┌──────────── Original ───────────┬──────────── Modified ──────────┐
│ return a - b;                   │ return a + b;                  │
└─────────────────────────────────┴────────────────────────────────┘
```

### VS Code版

以下を用途に応じて利用する。

- VS Code Diff Editor
- TextEditor Decoration
- CodeLens
- WorkspaceEdit

VS Code 固有 UI は Extension 側に実装し、差分データ自体は Web 版と共通化する。

---

## 18. Agent Workspace

Agent にユーザーの Working Tree を直接変更させない運用を選択可能とする。

推奨方式は Git Worktree である。

```text
Repository
│
├─ Main Working Tree
│    └─ User / VS Code
│
└─ Agent Worktree
     └─ .agent/worktrees/{sessionId}
          └─ Codex / Claude / Copilot
```

処理フロー：

```text
Agent実行
   │
   ▼
Agent Worktree変更
   │
   ▼
git diff
   │
   ▼
ChangeSet生成
   │
   ▼
User Review
   │
   ├─ Accept
   └─ Reject
   │
   ▼
Main Working Treeへ反映
```

これにより、Agent の変更をレビュー前に隔離できる。

---

## 19. HTTP API

Agent Manager は localhost の HTTP Server として起動する。

例：

```text
http://127.0.0.1:3100
```

API例：

```text
POST   /api/sessions
GET    /api/sessions
GET    /api/sessions/{id}

POST   /api/sessions/{id}/messages
POST   /api/sessions/{id}/cancel

GET    /api/sessions/{id}/events
GET    /api/sessions/{id}/changes

POST   /api/sessions/{id}/changes/{hunkId}/accept
POST   /api/sessions/{id}/changes/{hunkId}/reject
```

Agent Manager は原則として loopback interface のみに bind する。

---

## 20. WebSocket

リアルタイムの Agent 出力は WebSocket で配信する。

例：

```text
ws://127.0.0.1:3100/ws
```

イベント例：

```json
{
  "type": "assistant_message",
  "sessionId": "abc",
  "content": "auth.tsを確認します"
}
```

```json
{
  "type": "command",
  "sessionId": "abc",
  "command": "go test ./..."
}
```

```json
{
  "type": "usage",
  "sessionId": "abc",
  "inputTokens": 12000,
  "outputTokens": 3200
}
```

---

## 21. SQLite

初期バージョンでは SQLite を採用する。

### sessions

```text
id
agent
model
workspace
repository
branch
commit_before
commit_after
status
started_at
finished_at
```

### prompts

```text
id
session_id
role
content
created_at
```

### events

```text
id
session_id
sequence
event_type
payload_json
created_at
```

### usage

```text
session_id
input_tokens
cached_input_tokens
output_tokens
total_tokens
source
```

### raw_events

```text
id
session_id
agent
raw_json
created_at
```

### changes

```text
id
session_id
file_path
hunk_id
old_start
old_lines
new_start
new_lines
original_text
modified_text
status
created_at
reviewed_at
```

---

## 22. UI

### Main Chat

```text
┌─────────────────────────────────────┐
│ Coding Agent                        │
│                                     │
│ Agent: [Codex ▼]                   │
│ Model: [Default ▼]                 │
│                                     │
│ User                                │
│ Issue #123を実装して                 │
│                                     │
│ Codex                               │
│ auth.tsを確認しています              │
│                                     │
│ > grep -R authenticate src/         │
│ > go test ./...                     │
│                                     │
│ Tokens: 15,920                      │
│                                     │
├─────────────────────────────────────┤
│ Message...                   [Send] │
└─────────────────────────────────────┘
```

### Session History

```text
AGENT HISTORY

✓ Issue #123
  Codex
  15,920 tokens
  4m32s

✓ Unit Test
  Claude Code
  24,821 tokens
  7m11s

✗ Refactoring
  Copilot CLI
  8,520 tokens
  2m13s
```

### Session Detail

```text
Agent       Codex
Model       ...
Duration    4m32s
Tokens      15,920

Prompt
────────────────────────
Issue #123を実装して

Timeline
────────────────────────
00:00 Prompt
00:03 Read src/auth.ts
00:08 Command grep ...
00:14 Edit src/auth.ts
00:22 Command npm test
04:32 Completed

Changes
────────────────────────
src/auth.ts
src/auth.test.ts
```

---

## 23. Web開発モード

通常の UI 開発では VS Code を起動しない。

```bash
npm run dev
```

起動イメージ：

```text
Vite
http://localhost:5173

Agent Manager
http://localhost:3100
```

ブラウザから直接 Agent Manager に接続する。

```text
Browser
   │
HTTP / WebSocket
   │
   ▼
Agent Manager
   │
   ▼
Codex / Claude / Copilot
```

---

## 24. Mock モード

CLI を実際に起動せず UI を開発できる Mock Agent を用意する。

```bash
npm run dev:mock
```

Mock Event例：

```json
[
  {
    "type": "assistant_message",
    "delay": 300,
    "content": "コードを確認します"
  },
  {
    "type": "command",
    "delay": 500,
    "command": "grep -R authenticate src/"
  },
  {
    "type": "file_edit",
    "delay": 800,
    "file": "src/auth.ts"
  },
  {
    "type": "usage",
    "delay": 1000,
    "totalTokens": 12000
  }
]
```

これにより以下を Agent の Token 消費なしで確認できる。

- Chat
- Streaming
- Loading
- Cancel
- Error
- Diff
- Accept / Reject
- Token 表示
- Session History

---

## 25. テスト方針

### Level 1: Web UI + Mock

```text
Vue
 ↓
Mock Agent
```

目的：

- UI テスト
- Component テスト
- Diff UI
- Accept / Reject
- Streaming UI

---

### Level 2: Web UI + Agent Manager

```text
Vue
 ↓
Agent Manager
 ↓
Real Coding Agent
```

目的：

- CLI 統合確認
- JSONL Parsing
- Token Usage
- Process Control
- Git Diff
- ChangeSet

---

### Level 3: VS Code Integration

```text
VS Code
 ↓
Extension
 ↓
Agent Manager
 ↓
Coding Agent
```

目的：

- Webview
- VS Code Theme
- Workspace
- Decoration
- CodeLens
- vscode.diff
- WorkspaceEdit

---

## 26. VS Code Extension

VS Code Extension は極力薄くする。

主な責務：

```text
Extension
├─ Webviewの起動
├─ Workspace情報取得
├─ VS Code Command登録
├─ Diff Editor表示
├─ CodeLens / Decoration
├─ WorkspaceEdit
└─ Agent Manager起動・接続
```

AI Agent のビジネスロジックを Extension 側に持たせない。

---

## 27. Agent Manager 配布

Agent Manager は Go で単一実行ファイルとしてビルドする。

```text
bin/
├─ win32-x64/
│  └─ agent-manager.exe
├─ linux-x64/
│  └─ agent-manager
└─ darwin-arm64/
   └─ agent-manager
```

VSIX に必要なバイナリを同梱する。

利用者は VSIX をインストールするだけで Agent Manager を利用可能とする。

Codex / Claude Code / GitHub Copilot CLI 自体については、ユーザー環境にインストール済みであることを前提とする。

---

## 28. 配布

Marketplace には公開せず VSIX を利用する。

ビルド：

```bash
npm run build
vsce package
```

生成：

```text
coding-agent-0.1.0.vsix
```

インストール：

```bash
code --install-extension coding-agent-0.1.0.vsix
```

配布先として以下を想定する。

- GitHub Releases
- 社内ファイルサーバー
- SharePoint
- Teams
- その他社内配布基盤

---

## 29. セキュリティ

Agent Manager は任意コマンドを実行可能なため、以下を考慮する。

### Network

- `127.0.0.1` のみに bind する。
- 外部インターフェースでは listen しない。
- WebSocket も localhost のみに限定する。

### Authentication

Web版開発環境では Agent Manager 起動時にランダムな接続トークンを生成する方式を検討する。

### Logging

以下の情報には機密情報が含まれる可能性がある。

- Prompt
- Source Code
- Environment Variable
- Command Output
- API Key
- Access Token

ログ保存前に Secret Masking 機構を設ける。

例：

```text
OPENAI_API_KEY=****
ANTHROPIC_API_KEY=****
GITHUB_TOKEN=****
```

---

## 30. 実装フェーズ

### Phase 1

最小 Web UI を作成する。

- Vue 3
- TypeScript
- Vite
- Chat UI
- Mock Agent

### Phase 2

Agent Manager を作成する。

- Go
- HTTP API
- WebSocket
- Session
- SQLite

### Phase 3

Codex Adapter を作成する。

```text
Web
 ↓
Agent Manager
 ↓
Codex
```

以下を記録する。

- Prompt
- Response
- Command
- File operation
- Token usage
- Raw JSONL

### Phase 4

Diff Review を実装する。

- Git Diff
- ChangeSet
- Monaco Diff
- Accept
- Reject

### Phase 5

Claude Code / Copilot CLI Adapter を追加する。

### Phase 6

VS Code Extension を実装する。

- Webview
- Agent Manager 起動
- Workspace Integration
- Diff Integration

### Phase 7

VSIX 化する。

---

## 31. 将来拡張

以下を将来的な拡張候補とする。

### Agent 比較

```text
Agent別
├─ 実行回数
├─ 成功率
├─ 平均Token数
├─ 平均実行時間
├─ Accept率
├─ Reject率
└─ 変更行数 / 1000 tokens
```

### Cost

API 利用料金が取得・計算可能な場合：

```text
Session Cost
Daily Cost
Monthly Cost
Agent別Cost
Model別Cost
```

### GitHub Issue連携

```text
Issue
 ↓
Agent Session
 ↓
Implementation
 ↓
Review
 ↓
Commit / Pull Request
```

### Multi-Agent

```text
Session
├─ Codex
├─ Claude
└─ Copilot
```

同一タスクを複数 Agent に実行させ、結果・Token・Diff を比較できるようにする。

---

## 32. 設計上の基本原則

本システムでは以下を基本原則とする。

1. **UI と Agent 実行処理を分離する。**
2. **すべての Agent 実行を Agent Manager 経由にする。**
3. **CLI 固有仕様は Adapter に閉じ込める。**
4. **Agent の生ログを保存する。**
5. **共通 Event に正規化する。**
6. **Agent の申告ではなく Git の実差分も記録する。**
7. **Agent が変更した内容とユーザーが承認した内容を分離する。**
8. **Web UI と VS Code Webview で UI を共通化する。**
9. **Web アプリ単体で大部分の開発・テストを可能にする。**
10. **VS Code Extension は VS Code 固有処理だけを担当する。**

---

## 33. 最終アーキテクチャ

```text
                         ┌─────────────────────┐
                         │      Web App        │
                         │ Vue3 / TS / Vite    │
                         └──────────┬──────────┘
                                    │
                                    │
                         HTTP / WebSocket
                                    │
                                    ▼
┌─────────────────────┐    ┌──────────────────────────┐
│       VS Code       │    │      Agent Manager       │
│                     │    │           Go             │
│ Extension           │    │                          │
│   │                 │    │ Session                  │
│   └─ Webview ───────┼───▶│ Agent Adapter            │
│                     │    │ Event Logger             │
│ Diff / CodeLens     │    │ Token Collector          │
│ WorkspaceEdit       │    │ Git / ChangeSet          │
└─────────────────────┘    │ SQLite                   │
                           └─────────────┬────────────┘
                                         │
                 ┌───────────────────────┼───────────────────────┐
                 │                       │                       │
                 ▼                       ▼                       ▼
          ┌────────────┐          ┌────────────┐          ┌────────────┐
          │ Codex CLI  │          │Claude Code │          │Copilot CLI │
          └────────────┘          └────────────┘          └────────────┘
```

この構成により、VS Code 内の Coding Agent UI、ブラウザ単体での開発環境、複数 Coding Agent の統一管理、操作ログ・Token 利用量・ソース変更履歴の記録を一つのアーキテクチャで実現する。
