# Coding Agent VS Code Extension 設計書

> 2026-08-20更新: Session作成時に一度だけclocでGit管理下のコード数を計測し、Web版のUsage／Changesと並ぶ「コード数」Tabで表示する（13章）。Runごとの再計測は行わない。
> 2026-08-22更新: Sessionオープン時とRun完了時に、選択Providerのアカウント使用量を取得する。CodexはApp Serverの`account/rateLimits/read`、Copilotは公式SDKの`account.getQuota`、Claude CodeはCLIの`/usage`を利用し、Web版とVS Code版のヘッダーへ残量を表示する。

> 2026-08-19更新: Claude Code Adapterを追加する。Claude CodeはCLIがRunごとのtoken数とUSDコストを返すため、Managerは料金表からコストを再計算せず、CLIが報告した金額をそのまま保存する。

> 2026-08-16更新: RunごとのUsage詳細表示はWeb版限定とする。Web版ではUsageのRunカードから中央Conversation領域をRun詳細へ切り替え、トークン内訳、コマンド、関連Eventを表示する。詳細画面にはチャットへ戻る導線を設ける。VS Code版のUsage UIは変更しない。

> 2026-08-15更新: Agentは対象リポジトリを直接変更する。変更の承認操作は設けず、Run前後のcheckpoint差分を確認し、必要なFile／HunkだけをRun前の状態へ戻す。

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
7. Agentの変更を承認操作なしで利用可能にし、必要なFile／HunkだけをRun開始時のチェックポイントへ戻せるようにする。
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

### 4.4 Markdown出力の表示責務

Agent ManagerはAgentが返したMarkdownをHTMLへ変換せず、`assistant_message`／`reasoning_summary`の本文として保存する。表示側はイベント種別に応じてMarkdown rendererを選択し、Web UIでは見出し・リスト・コードブロック等を含む全文を表示する。VS Code Webviewではサイドバーに適したコンパクトな最新結果を表示し、リンクは外部リンクとして扱う。

Markdown rendererはHTMLタグ、`javascript:` URL、任意スクリプトを許可しない。Web UIとVS Code Webviewは同じ安全方針を使い、システムイベントやコマンド結果はMarkdown本文として扱わず、専用のログ表示とする。

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
- Runごとの差分表示
- File／Hunk単位のチェックポイント復元
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

  getCheckpoints(
    sessionId: string
  ): Promise<Checkpoint[]>;

  restoreHunk(
    sessionId: string,
    checkpointId: string,
    hunkId: string
  ): Promise<void>;

  restoreFile(
    sessionId: string,
    checkpointId: string,
    fileId: string
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

VS Code Extension HostはAgent ManagerのHTTP APIを利用し、WebviewへSession状態、Runイベント、Usage、ChangeSetを通知する。WebviewからAgent Managerへ直接接続せず、認証TokenはExtension設定で管理する。

Web版とVS Code版は同一Agent ManagerのSession／Run／Event／Usage／ChangeSet APIを共有する。VS Codeで作成したSessionは、同じManager URLとBearer tokenでWeb版を開くとSession履歴から選択して参照できる。VS Code側の履歴取得はAPIのcursorを辿って全ページを読み込む。

---

## 8. Agent Manager

Agent Manager は本システムの中核とする。

VS Code Extension から各 CLI を直接起動せず、原則としてすべての Coding Agent を Agent Manager 経由で実行する。

### 主な責務

```text
Agent Manager
├─ Agent process 起動
├─ Agent process 停止
├─ コマンド承認（設定／AI診断／利用者）
├─ Session 管理
├─ CLI JSON / JSONL 解析
├─ 共通Event形式への変換
├─ Token usage 収集
├─ Run前後のCheckpoint管理
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
- 実行前コマンド承認request／response

### 9.1 Codex CLI Adapterとコマンド承認

通常RunのCodex Adapterは、承認要求を双方向に扱うため`codex app-server --stdio`を起動する。Adapter内のread loopはJSON-RPC response、notification、server requestを振り分け、write mutexでrequestとapproval responseを直列化する。`item/commandExecution/requestApproval`はAgent Manager内のApproval Coordinatorへ渡し、判定完了後に`accept`または`decline`をCodexへ返す。Run自体はHTTP requestから独立したworkerで動作するため、利用者の承認待ちでもAPI、Event配信、別SessionのRunを停止しない。

Approval Coordinatorは次の順にfail-closedで判定する。

1. `commandApproval.allowedCommands`のargvルールに全command segmentが一致すれば設定で許可する。
2. 一致しなければ、同じProviderの設定済み軽量モデルをread-onlyの短命processとして実行し、`safe`／`low`／`high`／`critical`を判定する。設定した最大risk以下かつconfidence閾値以上の場合だけAIで許可する。
3. それ以外はRunを`waiting_for_approval`にし、SQLiteへ要求を保存してWebとVS Codeへ通知する。利用者は1回、Session中、永続、または不許可を選択する。

永続許可は設定ファイルへargv配列としてatomicに保存する。複合commandは`|`、`||`、`&&`、`;`、改行、`&`で分割し、すべてのsegmentが個別に一致した場合だけ全体を許可する。subshell、redirection、動的実行など静的に安全に解釈できない構文は設定で自動許可しない。詳細は[ADR-006](./decisions/006-command-approval.md)を参照する。

### 9.2 GitHub Copilot CLI Adapter

GitHub Copilot CLIはprogrammatic modeを使用し、対象Repositoryをcwdとして次の引数で実行する。

```text
copilot -C <repository> --prompt <message> --output-format json \
  --allow-all --no-ask-user --no-auto-update --no-color \
  --no-remote --no-remote-export [--model <model>] [--resume=<sessionId>]
```

`--output-format json`のJSONLはCopilot Adapter内で解釈する。`assistant.message`、`assistant.intent`、`tool.execution_start`、`tool.execution_complete`、`assistant.usage`、`result`、`session.error`を共通`SessionEvent`へ正規化し、未知Eventはマスク済みRaw Eventとして保持する。実動作モデルは`assistant.message.data.model`も補助的に参照する。extended thinkingである`assistant.reasoning`はユーザー向けReasoning Summaryへ変換せず、sub-agent由来のassistant Eventもmain chatへ混在させない。

CopilotのUsageはtoken数を共通Usageへ転記せず、`assistant.usage`または`assistant.message.data.model`の`model`を実動作モデルとして記録する。特に`--model auto`では、この値を選択結果として表示する。課金量はモデル倍率の`cost`ではなく、`copilotUsage.totalNanoAiu / 1_000_000_000`をAI creditsとしてRun内で加算する。旧CLIが`assistant.usage`を出さず最終`result.usage.premiumRequests`だけを出す場合は、その値を取得して既存Usageへ保存する。

料金表はManager起動時に公式Webページから取得し、`model_pricing`へモデル別の入力／cached入力／cache write／出力単価と取得元URL・取得時刻を保存する。Codexは保存済みtoken数からUSDコストを算出し、CopilotはAI credit（1 credit = $0.01）をUSDへ変換する。料金取得に失敗してもRunは継続し、コストは未算出として扱う。

過去データのバックフィルは`cost_usd IS NULL`の全Usageを対象にする。CopilotはAI creditだけで計算でき、旧Codexデータにモデルが保存されていない場合は現在のprovider default、未指定なら先頭の設定モデルを使用する。

初回RunでJSONLの`sessionId`を`AgentSession.agentThreadId`へ保存し、次Runから`--resume=<sessionId>`で同じ会話を継続する。CLI未導入、認証／quota error、timeout、cancelはCodexと同じRun終端処理およびafter snapshot取得へ合流する。

### 9.3 Claude Code Adapter

Claude Code CLIはprint modeを使用し、対象Repositoryをcwdとして次の引数で実行する。Promptは引数ではなくstdinへ渡し、Repositoryの内容がプロセス引数へ露出することとコマンドライン長の上限を避ける。

```text
claude --print --output-format stream-json --verbose \
  --permission-mode bypassPermissions [--model <model>] [--resume <sessionId>]
```

`--permission-mode bypassPermissions`はCopilotの`--allow-all`に相当する。[ADR-006](./decisions/006-command-approval.md)のコマンド承認はCodex専用であり、Claude Codeでは使用しない。

`stream-json`の各行はClaude Code Adapter内で解釈する。`system`（`subtype: init`）、`assistant`、`user`、`result`を共通`SessionEvent`へ正規化し、`stream_event`などの未知Eventはマスク済みRaw Eventとして保持する。`assistant`の`content` blockは種別ごとに分解し、`text`を`assistant_message`、`thinking`を`reasoning_summary`、`tool_use`を`command_started`へ、`user`の`tool_result`を`command_completed`へ変換する。`parent_tool_use_id`を持つsub-agent由来のEventはmain chatへ混在させない。

Usageは`result`の`usage`から記録する。`input_tokens`はcache write・cache readを含まないため、`inputTokens`にはこの3値の合計を、`cachedInputTokens`には`cache_read_input_tokens`を保存する。実動作モデルは`modelUsage`のうち出力token数が最大のモデルとし、`modelUsage`がない場合は`system`（`subtype: init`）の`model`を使用する。

コストはCLIが返す`total_cost_usd`をそのまま保存する。Claude Code CLIはcache readとcache writeを含めてモデル別に課金額を算出するため、Managerは料金表からの再計算を行わず、`claude` providerの料金取得も行わない。Managerが料金表からコストを算出するのは、CLIがコストを報告しないProviderのUsageに限る。

初回Runで`session_id`を`AgentSession.agentThreadId`へ保存し、次Runから`--resume <sessionId>`で同じ会話を継続する。CLI未導入、timeout、cancelはCodexと同じRun終端処理およびafter snapshot取得へ合流する。

---

## 10. Session

1回の Agent 作業を Session として管理する。

```ts
export interface AgentSession {
  id: string;

  agent: 'codex' | 'claude' | 'copilot';

  agentThreadId?: string;

  model?: string;

  actualModel?: string;

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

## 12. Usage

Agent ごとに取得可能な情報が異なるため、Token Usage の各項目は Optional とする。

```ts
export interface TokenUsage {
  inputTokens?: number;

  cachedInputTokens?: number;

  outputTokens?: number;

  totalTokens?: number;

  model?: string;

  aiCredits?: number;

  source:
    | 'cli'
    | 'api'
    | 'estimated'
    | 'unknown';
}
```

`model`は実行時に指定したモデル（CodexとClaude Codeの未指定は`default`、Copilotの自動選択は`auto`）で、`actualModel`はCLIが返す実際のモデルとする。Codexはtoken使用量を記録し、Codexのdefault指定時もCLIが返した実モデルを`actualModel`へ保存する。Claude Codeはtoken使用量に加えてCLIが算出した`costUsd`を保存する。Copilotは`assistant.usage.model`または`assistant.message.data.model`を`actualModel`へ保存し、token使用量は記録しない。UsageはSession単位だけでなく、可能な場合はEvent単位でも記録する。

UIはAI creditsを報告するProvider（Copilot）でのみcredit表示へ切り替え、CodexとClaude Codeはtoken内訳を表示する。

---

## 13. Source Stats（コード数）

Session作成時に一度だけ`cloc --vcs=git --json <workspace>`を実行し、Git管理下のファイルを言語別に集計する。Runごとの再計測は行わない。計測に失敗した場合（`cloc`未導入など）はSession作成自体を失敗させず、コード数は未計測のまま扱う。

```ts
export interface SourceStatsLanguage {
  language: string;
  files: number;
  blank: number;
  comment: number;
  code: number;
}

export interface SourceStats {
  sessionId: string;
  languages: SourceStatsLanguage[];
  total: SourceStatsLanguage;
}
```

`GET /api/v1/sessions/{id}/source-stats`で取得する。SQLiteは`session_source_stats`テーブルに言語ごとの行と、`language`列に`SUM`を格納した合計行を保持する。

UIはWeb版限定で、Usage／Changesと並ぶ「コード数」Tabとして表示する。VS Code版のUIは変更しない。

---

## 14. Agent 操作ログ

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
git write-tree                                      # 現在のindex tree
GIT_INDEX_FILE=<temp> git read-tree HEAD
GIT_INDEX_FILE=<temp> git add -A -- .
GIT_INDEX_FILE=<temp> git write-tree                # before Working Tree tree
git update-ref refs/maatgen/checkpoints/<session>/<run>/before <before-tree>
```

Agent 実行後：

```bash
# 同じ一時index方式でafter Working Tree treeを作成
git update-ref refs/maatgen/checkpoints/<session>/<run>/after <after-tree>
git diff --stat <before-tree> <after-tree>
git diff <before-tree> <after-tree>
```

実際の実装では`GIT_INDEX_FILE`にManager管理の一時indexを指定し、ユーザーのindex用`git write-tree`とWorking Tree snapshot用`git write-tree`を分離する。

---

## 15. Source Change Tracking

Agent はユーザーが開いているリポジトリを直接変更する。変更は承認待ちにせず即座にWorking Treeへ現れ、利用者はそのままコードを編集・実行できる。

Managerは各Runの開始直前と終了直後にGit checkpointを作成し、その差分をChangeSetとして管理する。ChangeSetは承認対象ではなく、変更内容の確認と復元範囲の選択に使う。

```text
Agent
  │
  ▼
Before Checkpoint
  │
  ▼
Working Treeを直接変更
  │
  ▼
After Snapshot / ChangeSet
  │
  ├─ File A
  │   ├─ Hunk 1
  │   └─ Hunk 2
  │
  └─ File B
      └─ Hunk 3
```

---

## 16. ChangeSet データモデル

```ts
export interface ChangeSet {
  sessionId: string;
  runId: string;
  checkpointId: string;
  beforeTree: string;
  afterTree: string;

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
    | 'changed'
    | 'restored'
    | 'conflict';
}
```

---

## 17. Checkpoint / Restore

承認操作は設けない。AgentがRunを完了した時点で変更は対象リポジトリに反映済みとする。利用者が不要と判断した変更だけを、FileまたはHunk単位でRun開始時のチェックポイントへ戻す。

```text
foo.ts

Hunk 1
──────────────
- return a - b;
+ return a + b;

[Restore to checkpoint]


Hunk 2
──────────────
- const x = 1;
+ const x = 2;

[Restore to checkpoint]
```

以下も提供する。

- Restore Hunk
- Restore File
- Restore All Changes from Run
- Next Change
- Previous Change

復元操作もEventとして記録する。

```json
{
  "type": "change_restored",
  "sessionId": "session-123",
  "runId": "run-456",
  "checkpointId": "checkpoint-456-before",
  "file": "src/foo.ts",
  "hunkId": "hunk-2",
  "action": "restore_hunk"
}
```

### 16.1 Checkpoint作成

Run開始直前に、次の状態を記録する。

- `HEAD` commit
- index tree
- tracked fileとuntrackedかつnon-ignored fileを含むWorking Tree tree
- file mode、symlink、binary blob

Working Tree treeは一時indexとGit plumbing commandで作成し、ユーザーのindex、Working Tree、branch、HEADを変更しない。作成したtreeは`refs/maatgen/checkpoints/<sessionId>/<runId>/{before,after}`から直接参照し、Git GCで失われないようにする。ignored fileはSecretや生成物を含む可能性があるためcheckpoint対象外とする。

before checkpointを作成できなければAgentは起動しない。after snapshotはRunがcompleted、failed、cancelled、timeoutのいずれで終了しても作成し、途中までの変更も確認・復元できるようにする。after snapshotを保存できない場合は次のRunを開始しない。

### 16.2 安全な復元

復元は記録済みのafter→before逆差分として適用する。対象File／Hunkの現在内容がafter snapshotから変わっていない場合だけatomicに反映する。

Run完了後に利用者または後続Runが同じ箇所を変更していた場合は、上書きせず`409 checkpoint_conflict`を返す。競合していない別のHunkは個別に復元できる。復元はWorking Treeだけを変更し、ユーザーのindexを暗黙に更新しない。

### 16.3 継続指示

Sessionは複数Runを保持する。Run完了後、利用者はAgentの変更を直接編集し、その状態から同じSessionへ次の指示を送れる。次のRun開始時に新しいbefore checkpointを作成し、保存済みAgent Thread IDを使って会話をresumeする。

実行中の同一Sessionへの追加指示はqueueせず`409 Conflict`とする。利用者によるファイル編集は妨げないが、実行中にAgentと同じ箇所を編集した場合、そのRunの差分には双方の変更が含まれ、Agent変更との厳密な帰属は保証しない。

---

## 18. Diff 表示

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

VS Code 固有 UI は Extension 側に実装し、差分データ自体は Web 版と共通化する。

ExtensionはChangeSetの`original`／`modified`から読み取り専用の仮想ドキュメントを生成し、`vscode.diff`でRun前後の固定スナップショットを表示する。modified側にはCodeLensを表示し、HunkまたはFileをcheckpointへ戻せるようにする。WebviewのChanges一覧からもFile／Run全体のRestoreを実行できる。Restore前に対象ファイルの未保存VS Codeバッファを検査し、未保存編集がある場合とRun実行中は復元しない。

ChangeSetはSession切替、Run終了、`change_restored`検出時に再取得し、通常のポーリングではキャッシュを利用する。

---

## 19. Agent Workspace

Agentの作業ディレクトリは、Session作成時に利用者が指定したリポジトリのWorking Treeそのものとする。専用Git Worktreeは作成しない。

```text
Repository
│
└─ Working Tree
     ├─ User / VS Code
     └─ Codex / Claude / Copilot
```

処理フロー：

```text
Run前Checkpoint
   │
   ▼
AgentがWorking Treeを直接変更
   │
   ▼
Run後Snapshot
   │
   ▼
Checkpoint差分を表示
   │
   ▼
必要なFile／HunkだけRestore
```

Session開始時にcleanであることは要求しない。既存の未コミット変更は最初のRunのbefore checkpointへ含め、Agent変更前の状態として保護する。Agent実行中はbranch／HEAD／indexを変更しないことを実行ポリシーとして指示し、Run後にHEADまたはindexの予期しない変化を検出した場合はRepository state conflictとして通知する。

旧Worktree方式、Accept／Reject API、旧ChangeSet schemaとの後方互換性は維持しない。新設計を正規実装とし、既存ローカルデータは必要に応じて再作成する。

---

## 20. HTTP API

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
GET    /api/sessions/{id}/checkpoints

POST   /api/sessions/{id}/checkpoints/{checkpointId}/restore
POST   /api/sessions/{id}/checkpoints/{checkpointId}/files/{fileId}/restore
POST   /api/sessions/{id}/checkpoints/{checkpointId}/hunks/{hunkId}/restore
```

Agent Manager は原則として loopback interface のみに bind する。

---

## 21. WebSocket

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

## 22. SQLite

初期バージョンでは SQLite を採用する。

### sessions

```text
id
agent
model
workspace
repository
branch
status
started_at
finished_at
```

Providerのモデル選択はブラウザだけに保持せず、Managerのツール設定に`defaultModel`として保存する。通常の実行ファイルでは設定パスを実行ファイル基準で解決し、`go run`の一時実行ファイルでは起動時のカレントディレクトリ基準に切り替える。
Web／VS CodeのRun設定ではModelの隣にReasoning Effort（Default、low、medium、high、xhigh、max）を表示する。未指定時は各CLIの既定値に委譲し、指定値はProtocolの`reasoningEffort`としてAgent ManagerからCodex App Serverの`effort`、Claude Code／Copilot CLIの`--effort`へ渡す。

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
run_id
checkpoint_id
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
restored_at
```

### checkpoints

```text
id
session_id
run_id
head_commit
index_tree
before_tree
after_tree
before_ref
after_ref
status
created_at
completed_at
```

---

## 23. UI

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

## 24. Web開発モード

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

## 25. Mock モード

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
- Checkpoint restore
- Token 表示
- Session History

---

## 26. テスト方針

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
- File／Hunk restore
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

---

## 27. VS Code Extension

VS Code Extension は極力薄くする。

主な責務：

```text
Extension
├─ Webviewの起動
├─ Workspace情報取得
├─ VS Code Command登録
├─ Diff Editor表示
├─ CodeLens / Decoration
└─ Agent Manager起動・接続
```

AI Agent のビジネスロジックを Extension 側に持たせない。

---

## 28. Agent Manager 配布

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

## 29. 配布

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

## 30. セキュリティ

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

### Direct Working Tree

- Session開始時に、Agentが対象repositoryを直接変更することをUIへ明示する。
- checkpointの対象はtracked fileとuntracked non-ignored fileであり、ignored fileは復元保証の対象外と表示する。
- Restore前にafter snapshotと現在内容を比較し、利用者の後続編集を上書きしない。
- Agentによるbranch、HEAD、indexの変更を禁止する実行指示を加え、Run後にも状態変化を検査する。
- CheckpointはCommitや外部バックアップの代替ではない。

---

## 31. 実装フェーズ

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

直接編集、Checkpoint、Diff、Restoreを実装する。

- Git Diff
- Run前後Checkpoint
- ChangeSet
- Monaco Diff
- Restore Hunk／File

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

## 32. 将来拡張

以下を将来的な拡張候補とする。

### Agent 比較

```text
Agent別
├─ 実行回数
├─ 成功率
├─ 平均Token数
├─ 平均実行時間
├─ Restore率
├─ 競合率
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
Diff確認／必要箇所のRestore
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

## 33. 設計上の基本原則

本システムでは以下を基本原則とする。

1. **UI と Agent 実行処理を分離する。**
2. **すべての Agent 実行を Agent Manager 経由にする。**
3. **CLI 固有仕様は Adapter に閉じ込める。**
4. **Agent の生ログを保存する。**
5. **共通 Event に正規化する。**
6. **Agent の申告ではなく Git の実差分も記録する。**
7. **Agentは対象Working Treeを直接変更し、Run前後のCheckpointで確認と安全な復元を可能にする。**
8. **Web UI と VS Code Webview で UI を共通化する。**
9. **Web アプリ単体で大部分の開発・テストを可能にする。**
10. **VS Code Extension は VS Code 固有処理だけを担当する。**

---

## 34. 最終アーキテクチャ

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
│ Checkpoint UI       │    │ Git / Checkpoint         │
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
