# Coding Agent 実装計画

## 1. 方針

本計画は、[coding-agent-design.md](./coding-agent-design.md) を実装可能な単位へ分解したものである。

> 2026-08-15設計変更: Agent専用worktreeとAccept／Reject方式を廃止し、対象Working Treeの直接編集、Run単位checkpoint、必要箇所のRestore方式へ置換する。既存のWorktree／Review実装は後方互換性を維持せず置換対象とする。

Codex CLI版の共通Protocol、Session管理、Checkpoint／Diff／Restore、Web／VS Code連携が完了したため、追加AdapterとしてGitHub Copilot CLIとClaude Code CLIを対象に加える。

Codexの実行前コマンド承認には双方向通信が必要なため、通常Runは`codex app-server --stdio`を使用する。短命なAI診断はtool実行を許可しない`codex exec --json`を使用する。詳細は[ADR-006](./decisions/006-command-approval.md)に従う。

## 2. MVPの完成条件

以下の一連のフローがブラウザから実行できることをMVPの完成条件とする。

1. Agent Managerをlocalhostで起動する
2. GitリポジトリからSessionを作成する。既存の未コミット変更は許可する
3. Run開始直前のWorking Treeをcheckpointとして保存する
4. 対象リポジトリをcwdとしてCodexを実行し、Working Treeを直接変更する
5. CodexのJSONL出力をリアルタイム表示する
6. Prompt、Assistant message、Command、File change、Usage、Raw Eventを保存する
7. Run前後の実Git差分からChangeSetを作成して表示する
8. コマンドは設定、AI診断、利用者確認の順に実行前承認し、Agentによるファイル変更自体はAccept操作なしで利用できる
9. 利用者が直接編集した現在のコードから、同じSessionで次のCodex指示を継続できる
10. Session履歴を再起動後も表示できる
11. WebSocket切断後にEventを再取得して表示を復元する

## 3. 先に固定する設計判断

### 3.1 Codexの実行方式

通常Runは`codex app-server --stdio`を起動し、JSON-RPCの`thread/start`または`thread/resume`、`turn/start`を使用する。stdoutはresponse、notification、server requestへ振り分け、stderrは診断ログとして扱う。コマンド承認server requestへの応答中もread loopを継続する。TUIのPTY制御とCodex内部データベースの直接参照は対象外とする。

### 3.2 SessionとRunを分離する

Sessionはユーザーが管理する論理的な会話、Runは1回のCodex CLIプロセスとする。

```text
AgentSession
└─ AgentRun 1
└─ AgentRun 2
└─ AgentRun 3
```

```ts
interface AgentSession {
  id: string;
  agent: 'codex' | 'claude' | 'copilot';
  workspace: string;
  agentThreadId?: string;
  status: 'active' | 'closed';
  createdAt: string;
  closedAt?: string;
}

interface AgentRun {
  id: string;
  sessionId: string;
  status:
    | 'queued'
    | 'starting'
    | 'running'
    | 'waiting_for_approval'
    | 'completed'
    | 'failed'
    | 'cancelled';
  prompt: string;
  startedAt?: string;
  finishedAt?: string;
  exitCode?: number;
}
```

初期版では、実行中Sessionへの追加メッセージは許可せず、`409 Conflict`を返す。メッセージqueueingやsteeringは後続課題とする。

### 3.3 Adapter境界

CodexとCopilotを実装し、Agent ManagerからCLI固有処理を分離する。

```go
type AgentAdapter interface {
    Name() string
    Check(ctx context.Context) (AgentInfo, error)
    Run(ctx context.Context, req RunRequest, emit EventEmitter) (RunResult, error)
}
```

```text
AgentAdapter
├─ CodexAdapter
├─ ClaudeAdapter
└─ CopilotAdapter
```

CLI仕様の差分はAdapterへ閉じ込め、過度なcapability抽象化は行わない。

### 3.4 Eventモデル

ユーザー操作とManager内部イベントも記録できるよう、`AgentEvent`ではなく`SessionEvent`とする。

```ts
interface SessionEvent {
  id: string;
  sessionId: string;
  runId?: string;
  sequence: number;
  timestamp: string;
  schemaVersion: 2;
  source: 'user' | 'codex' | 'claude' | 'copilot' | 'manager';
  type:
    | 'user_prompt'
    | 'assistant_message'
    | 'reasoning_summary'
    | 'command_started'
    | 'command_completed'
    | 'file_change_reported'
    | 'usage_reported'
    | 'change_detected'
    | 'checkpoint_created'
    | 'change_restored'
    | 'run_started'
    | 'run_completed'
    | 'run_failed'
    | 'run_cancelled'
    | 'error';
  data: unknown;
}
```

Codexの`thread.started`、Copilot JSONLの`sessionId`、Claude Code stream-jsonの`session_id`から得られるAgent Thread IDは、ManagerのSession IDとは別に保存する。

`reasoning`はCodexが出力した要約だけを保存し、内部推論を再構成・推測しない。

### 3.5 Direct Working TreeとCheckpoint

AgentのcwdはSessionに指定されたリポジトリとし、専用worktreeは作成しない。Session開始時にcleanであることも要求しない。

```text
ユーザーのリポジトリ
└─ Working Tree
   ├─ 利用者／Editor
   └─ Agent
```

各Runの開始直前にbefore checkpoint、終了直後にafter snapshotを作成する。checkpointは一時indexからGit treeを生成し、`refs/maatgen/checkpoints/<sessionId>/<runId>/{before,after}`からtreeを直接参照して保持する。ユーザーのindex、Working Tree、branch、HEADはcheckpoint作成によって変更しない。

before checkpointの作成に失敗した場合はAgentを起動しない。after snapshotはRunのcompleted／failed／cancelled／timeoutを問わず作成し、途中までの変更もDiffとRestoreの対象にする。after snapshotを保存できない間は次のRunを開始せず、診断と再取得操作を表示する。

tracked fileとuntrackedかつnon-ignored fileを対象とする。ignored file、submodule、非Gitディレクトリは初期対象外とする。既存の未コミット変更はbefore checkpointへ含める。

### 3.6 ChangeSet

Hunkだけでなく、新規、削除、rename、binary、file mode変更を表現できるようにする。

```ts
interface ChangeSet {
  sessionId: string;
  runId: string;
  checkpointId: string;
  beforeTree: string;
  afterTree: string;
  files: FileChange[];
}

interface FileChange {
  id: string;
  oldPath?: string;
  newPath?: string;
  kind: 'modify' | 'add' | 'delete' | 'rename' | 'binary' | 'mode_change';
  original?: string;
  modified?: string;
  restoreMode: 'hunk' | 'file';
  status: 'changed' | 'partially_restored' | 'restored' | 'conflict';
  hunks: ChangeHunk[];
}
```

通常のテキスト変更はHunk単位、binaryやrenameなどはFile単位でcheckpointへ戻す。

### 3.7 Restore

承認操作は設けない。Agentの変更はRun完了時点ですでに対象Working Treeへ反映済みであり、利用者が不要な変更だけを戻す。

```text
changed → restored
changed → conflict
```

Restore時はafter snapshotからbefore checkpointへの逆差分を生成する。対象File／Hunkの現在内容がafter snapshotと一致する場合だけatomicに反映する。

Run後に利用者または後続Runが同じ箇所を編集している場合は上書きせず`409 checkpoint_conflict`を返す。競合しないHunkは個別にRestoreできる。RestoreはWorking Treeだけを変更し、indexは暗黙に更新しない。

### 3.8 同一Sessionでの継続修正

Run完了後もSessionをactiveのまま保持する。利用者はAgentの変更を直接編集でき、その現在状態から同じSessionへ次のPromptを送信する。次のRunは新しいbefore checkpointを作成し、`agentThreadId`を使ってCodex、Claude Code、またはCopilotをresumeする。

同一Sessionで実行中のRunは1つまでとし、実行中の追加Promptは`409 Conflict`とする。実行中の利用者編集は妨げないが、Agentと同じ箇所を同時編集した場合は差分の帰属を保証しない。

### 3.9 後方互換性

今回の設計変更では後方互換性を考慮しない。旧Worktree、Accept／Reject、旧ChangeSet API、旧SQLiteデータを維持するためのlegacy mode、互換endpoint、dual schema、migration shimは実装しない。既存のローカル開発データは必要に応じて再作成し、新設計を正規実装とする。

## 4. リポジトリ構成

```text
apps/
├─ web/
│  ├─ src/
│  └─ vite.config.ts
├─ vscode/
│  ├─ src/
│  │  ├─ extension.ts
│  │  ├─ webviewProvider.ts
│  │  └─ commands/
│  └─ package.json
└─ agent-manager/
   ├─ cmd/
   ├─ internal/
   │  ├─ agent/
   │  │  └─ codex/
   │  ├─ session/
   │  ├─ run/
   │  ├─ event/
   │  ├─ diff/
   │  ├─ git/
   │  ├─ process/
   │  ├─ storage/
   │  ├─ security/
   │  └─ server/
   └─ go.mod
packages/
├─ protocol/
├─ api-client/
└─ ui/
```

## 5. 実装フェーズ

### Phase 0：基盤と契約

- pnpm workspace、TypeScript、Vue、Vite、Vitestを設定
- Go module、lint、test設定
- `SessionEvent`、`AgentSession`、`AgentRun`、`ChangeSet`、`TokenUsage`を定義
- HTTP APIとWebSocket EventのJSON fixtureを作成
- TypeScript／Go双方でfixture互換テストを追加
- 共通エラー形式とHTTP statusを確定

完了条件:

- ルートからfrontend／backendのlint、typecheck、testが実行できる
- TypeScriptとGoで同一fixtureを読み書きできる

### Phase 1：Mock Web UI

- `AgentApi`インターフェース
- `MockAgentApi`
- Chat、Timeline、Usage、History、Diff画面
- Streaming、Cancel、Error、再接続表示
- Monaco Diff Editor
- Hunk／File単位のRestore
- 正常、失敗、Cancel、複数HunkのMock scenario

完了条件:

- `pnpm dev:mock`で主要フローを確認できる
- Component testでEvent描画とRestore操作を検証できる

### Phase 2：Agent Manager基盤

- `net/http`によるlocalhost HTTP Server
- `127.0.0.1`限定bind
- Bearer token認証
- WebSocket ticketとOrigin検証
- SQLite migration
- Session／Run／Event／Usage／Raw Event CRUD
- Event sequence採番
- WebSocket配信と`afterSequence`再取得
- graceful shutdown

完了条件:

- API統合テストが通る
- Manager再起動後も履歴を取得できる
- WebSocket再接続でEvent欠落を復元できる

### Phase 3：Codex Adapter

- `codex`実行ファイルの検出とversion取得
- `codex exec --json`の起動
- stdinへのPrompt送信
- stdout JSONLの行単位読込
- stderr読込
- Codex Eventから`SessionEvent`への変換
- `thread.started`のThread ID保存
- `turn.completed`のUsage保存
- `turn.failed`、終了コード、timeout処理
- Process cancel
- JSONL parserのfixture test

完了条件:

- fake Codex CLIで正常、遅延、invalid JSON、異常終了、cancelを再現できる
- 実Codex CLIでPrompt、Streaming、完了、Usageを確認できる

### Phase 4：Direct Working Tree、Checkpoint、ChangeSet

- Git repository検証（dirty Working Treeを許可）
- Agentのcwdを対象リポジトリに固定
- Run開始直前のHEAD／index／Working Tree checkpoint作成
- Run終了直後のafter snapshot作成
- Git private refによるcheckpoint保持
- 実行前後のGit状態取得
- text／binary／rename／delete／mode changeの差分検出
- unified diffからHunk生成
- 内容hashベースのHunk ID生成
- checkpoint保持期限とcleanup

完了条件:

- Agentの変更が対象Working Treeへ直接現れる
- 既存の未コミット変更を保持したままRunを開始できる
- Agentが申告しない変更もGit差分として検出できる
- 複数ファイルと複数Hunkを再現できる

### Phase 5：Checkpoint Restore

- after→before逆Hunk patch生成
- File／Hunk／Run全体のRestore
- after hash／現在hash検証
- Restore API
- `409 checkpoint_conflict`処理
- Restore Event保存
- 冪等な再送処理

完了条件:

- 一部HunkだけをRun前の状態へ戻せる
- Run後の利用者変更や後続Run変更を上書きしない
- API再送で二重適用されない

### Phase 6：Web版Codex MVP統合

- `HttpAgentApi`
- HTTP／WebSocket client
- reconnect／backoff
- Event重複排除
- Session履歴のpagination
- Manager未起動、Codex未導入、認証失敗の診断表示
- MockとReal APIの切り替え

完了条件:

```text
Browser
  → Agent Manager
  → Codex CLI
  → Repository Working Tree
  → Checkpoint
  → ChangeSet
  → Optional Restore
```

### Phase 7：VS Code Extension

- WebviewViewProvider
- CSPとnonce
- Web UI packageの再利用
- Agent Manager起動とhealth check
- Workspace情報連携
- Manager HTTP APIによるSession作成／再利用、Run送信／cancel、イベント／Usage／ChangeSet同期
- VS Code WebviewでのRun操作、Session履歴選択、イベント／Usage／変更ファイル表示
- Web版とVS Code版の共有Manager Session履歴、およびcursorページングの統合
- Manager URL／Bearer tokenのExtension設定
- `vscode.diff`
- CodeLens／Decoration
- Extension終了時のManager cleanup（checkpointは保持期限に従う）

ExtensionにはCodex固有の実行ロジックを持たせない。

### Phase 8：VSIX化

- GoバイナリのWindows／Linux／macOS向けcross build
- VSIXへのバイナリ同梱
- `vsce package`
- checksum、version、Third-party notice
- GitHub Releases用成果物

### Phase 9：追加Adapter

Codex MVPの完了後に、同じ`AgentAdapter`と`SessionEvent`契約へGitHub Copilot CLIとClaude Code CLIを追加する。

Copilotは`--output-format json`、`--prompt`、`--resume=<sessionId>`を使用する。Claude Codeは`--print --output-format stream-json --verbose --permission-mode bypassPermissions`を使用し、Promptはstdinへ、継続は`--resume <sessionId>`で渡す。JSONL parser、引数、利用不可エラーだけをAdapterへ閉じ込め、Session／Run／Checkpoint／RestoreとWeb／VS Code UIは共通契約を使う。ADR-006のコマンド承認はCodex専用とする。

実行結果の`model`には指定モデル（CodexとClaude Codeの未指定は`default`、Copilot自動選択は`auto`）を、`actualModel`には実モデルを保存する。Copilotの`assistant.usage.model`または`assistant.message.data.model`を実モデルとして保存し、`auto`指定時にも選択されたモデルを確認可能にする。Codexのdefault指定時もCLIが返したモデルを`actualModel`へ保存する。Claude Codeは`modelUsage`のうち出力token数が最大のモデル、なければ`system`（`subtype: init`）の`model`を`actualModel`とする。Copilotではtoken数を保存せず、リクエスト単位の`copilotUsage.totalNanoAiu`をAI creditsへ変換してRun／Session単位に集計する。旧CLIの`result.usage.premiumRequests`形式もフォールバックとして取り込む。

Claude Codeのtoken数は`result.usage`から取り、`inputTokens`は`input_tokens`・`cache_creation_input_tokens`・`cache_read_input_tokens`の合計、`cachedInputTokens`は`cache_read_input_tokens`とする。

Manager起動時に[OpenAIモデル比較](https://developers.openai.com/api/docs/models/compare)と[GitHub Copilotモデル料金](https://docs.github.com/en/copilot/reference/copilot-billing/models-and-pricing)を取得し、モデル料金をSQLiteへ保持する。Run完了時にCodexはtoken単価から、CopilotはAI credit単価からUSDコストを計算し、Web／VS Codeへ表示する。Claude CodeはCLIが`result.total_cost_usd`を返すため、その金額をそのまま保存し、`claude` providerの料金取得と再計算は行わない。

`--backfill-costs`は未計算の全Usageを再処理する。保存モデルがない旧Codex Usageは現在の既定モデル（未指定時は設定リスト先頭）を用い、Copilot Usageはモデルの有無にかかわらず保存済みAI creditから計算する。Claude CodeのUsageはRun完了時点でコストが確定するため、backfillの対象にならない。

## 6. HTTP API

```text
POST   /api/sessions
GET    /api/sessions
GET    /api/sessions/{id}
POST   /api/sessions/{id}/messages
POST   /api/sessions/{id}/close
POST   /api/runs/{id}/cancel
GET    /api/sessions/{id}/events?afterSequence={n}
GET    /api/sessions/{id}/changes
GET    /api/sessions/{id}/checkpoints
POST   /api/sessions/{id}/checkpoints/{checkpointId}/restore
POST   /api/sessions/{id}/checkpoints/{checkpointId}/files/{fileId}/restore
POST   /api/sessions/{id}/checkpoints/{checkpointId}/hunks/{hunkId}/restore
POST   /api/ws-tickets
```

WebSocketは次の形式とする。

```text
ws://127.0.0.1:<port>/ws
```

HTTP取得とWebSocket配信では同じ`SessionEvent` JSONを使用する。

## 7. SQLiteスキーマ

### sessions

```text
id
agent
workspace
codex_thread_id
status
created_at
closed_at
```

### runs

```text
id
session_id
prompt
status
started_at
finished_at
exit_code
created_at
```

### events

```text
id
session_id
run_id
sequence
source
event_type
payload_json
created_at
```

### run_usage

```text
run_id
input_tokens
cached_input_tokens
output_tokens
reasoning_output_tokens
total_tokens
model
ai_credits
source
raw_json
```

`model_pricing`にはprovider、model、入力／cached入力／cache write／出力の1M token単価、source URL、retrieved_atを保存する。

### session_source_stats

```text
id
session_id
language
files
blank
comment
code
created_at
```

Session作成時に一度だけclocの結果を保存する。言語ごとに1行、合計は`language = 'SUM'`の行として保存する。

### redacted_raw_events

```text
id
session_id
run_id
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
old_path
change_kind
restore_mode
old_start
old_lines
new_start
new_lines
original_text
modified_text
status
before_hash
after_hash
restored_at
created_at
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

## 8. テスト方針

### Unit test

- Codex JSONL parser
- Event normalization
- Session／Run state transition
- Usage集計
- Secret masking
- unified diff parser
- Hunk ID生成
- checkpoint tree生成
- Restore逆patch再生成

### Integration test

- SQLite migrationとCRUD
- HTTP認証
- WebSocket ticket
- Event sequenceと再取得
- fake Codex process
- process cancel／timeout
- dirty Working Treeを含むcheckpoint作成
- Restore時の競合検出
- 同一Sessionでの複数Runとcheckpoint連鎖

### E2E test

- Mock UIフロー
- Browser → Manager → fake Codex → direct edit → diff → restore
- Manager再起動後の履歴復元
- WebSocket切断・再接続

CIでは実Codexを起動せず、fake CLIでJSONL、遅延、invalid JSON、異常終了、終了シグナルを再現する。実Codex確認は手動の統合テストとする。

## 9. セキュリティと運用上の制約

- Agent Managerは`127.0.0.1`のみにbindする
- HTTPはBearer token必須
- WebSocketは短命ticketを使用する
- 許可するOriginを限定する
- API keyをManagerが常時保持・ログ出力しない
- Raw Eventはマスク済みJSONのみ保存する
- コマンド出力、Prompt、ソースコードの保存期間を設定可能にする
- Sessionのrepository pathを正規化し、Session作成時にGit repositoryであることを検証する
- checkpoint作成でignored fileを保存しない
- Restore時に現在内容を検証し、後続編集を上書きしない
- Process起動時の引数は配列で渡し、shell経由で実行しない
- Sessionごとに同時Runを1つに制限する

## 10. 将来拡張

以下はCodex MVP後に対応する。

- submodule
- queueing／steering
- binary差分の専用UI
- Agent比較、Cost集計、成功率
- GitHub Issue、Commit、Pull Request連携
- Multi-Agent Session

## 11. 決定事項チェックリスト

ステータスの意味は次のとおりとする。

| ステータス | 意味 |
|---|---|
| 未 | まだ具体案・方針を決めていない |
| 検討中 | 推奨案や候補はあるが、実装の前提として確定していない |
| 検討済み | 本計画上の方針として確定している。変更する場合はDecision Recordを更新する。**決定事項**が「—」の場合は、**推奨（案）**の記載を決定事項とする |

### 11.1 MVP実装開始前に決めること

| ID | 決定対象 | 推奨（案） | 決定事項 | 決定期限 | ステータス |
|---|---|---|---|---|---|
| D-01 | Node.jsパッケージマネージャー | pnpm workspace | pnpm 10.15.0 workspace | Phase 0開始前 | 検討済み |
| D-02 | Node.js／TypeScriptバージョン | Node.js LTS、TypeScript最新安定版 | Node.js 20.19以上、TypeScript 5系 | Phase 0開始前 | 検討済み |
| D-03 | Goバージョン | 現行安定版の1世代固定 | Go 1.22.5以上 | Phase 0開始前 | 検討済み |
| D-04 | Go HTTPルーター | Go標準の`net/http`と`http.ServeMux`を使用 | `net/http`と`http.ServeMux` | Phase 0開始前 | 検討済み |
| D-05 | WebSocketライブラリ | `github.com/coder/websocket` | `github.com/coder/websocket` | Phase 0開始前 | 検討済み |
| D-06 | SQLiteドライバ | `modernc.org/sqlite`を`database/sql`経由で使用 | `modernc.org/sqlite`と`database/sql` | Phase 0開始前 | 検討済み |
| D-07 | Protocolの正規ソース | JSON Schemaをwire契約の正規ソースとし、TypeScript型とGo型を生成・検証 | JSON Schemaをwire契約の正規ソースとする。TypeScript公開型は`json-schema-to-typescript`で生成し、genericなどの補助型だけを手書きする。Go型は`time.Time`、`json.RawMessage`、内部定数を持つ手書き型とし、共有fixtureでSchema互換性を検証する | Phase 0開始前 | 検討済み |
| D-08 | APIのバージョニング | HTTPは`/api/v1`、Eventは`schemaVersion`を併用 | HTTPは`/api/v1`、Eventは`schemaVersion`を併用 | Phase 0開始前 | 検討済み |
| D-09 | Agent Managerのbind先 | `127.0.0.1`のみ | `127.0.0.1`のみ | Phase 0開始前 | 検討済み |
| D-10 | Portの割当 | 3100を優先し、使用中またはport=0指定時はOSの空きportを使用 | 3100とし、引数で指定可能とする | Phase 0開始前 | 検討済み |
| D-11 | Port／tokenの通知方法 | 親プロセス指定のruntime metadata fileへJSONを書き、stdoutにも機械可読な1行を出力 | runtime metadata JSONにaddress／token／PID／versionを保存し、ログにはfile pathだけを出力 | Phase 0開始前 | 検討済み |
| D-12 | Managerデータ保存先 | OS標準のユーザーデータディレクトリ配下の`maatgen/` | `os.UserConfigDir()/maatgen`。`--data-dir`で変更可能 | Phase 0開始前 | 検討済み |
| D-13 | Agent作業場所 | 利用者が指定したrepositoryのWorking Treeを直接使用 | 専用worktreeは作成せず、Sessionのrepository pathをAgentのcwdにする | Phase 0開始前 | 検討済み |
| D-14 | Session開始条件 | Git repositoryであること。dirty Working Treeを許可しRun前checkpointへ含める | Git repositoryであれば開始可能とし、既存のtracked／untracked non-ignored変更をRun前checkpointへ含める | Phase 0開始前 | 検討済み |
| D-15 | Codex実行バイナリ検出 | PATHから検出し、shellを介さず`codex --version`で検証・絶対パスとversionを保存 | `exec.LookPath`で検出し、shellなしの`--version`成功後に絶対パスとversionをAdapterへ保持 | Phase 0開始前 | 検討済み |
| D-16 | Codex sandbox policy | `workspace-write` | `workspace-write` | Codex Adapter着手前 | 検討済み |
| D-17 | Codex approval policy | 設定、AI診断、利用者確認の三段階 | App Serverの承認requestをAgent Managerで判定する。診断用Codexだけ`--ask-for-approval never`とread-only sandboxを使用する | Codex Adapter着手前 | 検討済み |
| D-18 | Codex model指定 | 未指定時はCodex設定に委譲し、要求値だけ保存 | 未指定時はCLI設定へ委譲し、指定時だけ`--model`を渡す。Web UIで選択した既定モデルは、Managerのツール設定（`config/providers.json`）へ保存し、次回起動時に復元する | Codex Adapter着手前 | 検討済み |
| D-19 | Codex timeout | Run単位の上限時間を設定可能にする | 既定30分。Run要求で変更可能とし、timeout時はプロセスツリーを終了する | Codex Adapter着手前 | 検討済み |
| D-20 | 認証方式 | HTTP Bearer token、WebSocket短命ticket | HTTP Bearer token、WebSocket短命ticket | Phase 2開始前 | 検討済み |
| D-21 | 許可Origin | Vite開発URLとVS Code Webviewのみ | — | Phase 2開始前 | 検討中 |
| D-22 | Raw Event保存方針 | マスク済みJSONのみ保存 | マスク済みJSONのみ保存 | Phase 2開始前 | 検討済み |
| D-23 | Secret Masking対象 | API key、token、password、環境変数名のallowlist | JSON keyは`token`、`password`、`secret`、`apiKey`、`authorization`を大文字小文字・区切り差を無視してマスクする。文字列中のBearer token、`sk-`形式、key／token／password代入値もマスクする | Phase 2開始前 | 検討済み |
| D-24 | ログ保持期間 | 初期値と最大DBサイズを設定可能にする | — | Phase 2開始前 | 未 |
| D-25 | 同時実行制限 | SessionごとにRunを1つまで | SessionごとにRunを1つまで | Phase 2開始前 | 検討済み |
| D-26 | 実行中の追加Prompt | 初期版は409で拒否 | 初期版は409で拒否 | Phase 2開始前 | 検討済み |
| D-27 | Session終了操作 | 明示的なclose APIを用意 | 明示的なclose APIを用意 | Phase 2開始前 | 検討済み |
| D-28 | Checkpoint cleanup | active Session中は保持し、close時にGit private refを削除 | Session close時に`refs/maatgen/checkpoints/<sessionId>/`を削除する。削除失敗は状態と試行回数を保存し、冪等なclose APIで再試行する | Phase 4開始前 | 検討済み |
| D-29 | Hunk ID方式 | 内容を含むSHA-256 | 内容を含むSHA-256 | Phase 4開始前 | 検討済み |
| D-30 | 変更確定方式 | 承認を不要とし、不要な変更だけcheckpointへ戻す | AgentはWorking Treeを直接変更する。Accept／Rejectは廃止し、Hunk／File／Run全体のRestoreを提供する | Phase 5開始前 | 検討済み |
| D-31 | Restore競合時の動作 | 後続編集を上書きせず409 Conflict | 現在内容がafter snapshotと一致しない対象は`409 checkpoint_conflict`とし、atomicに変更せず終了する | Phase 5開始前 | 検討済み |
| D-32 | binary／renameのRestore単位 | File単位 | binary／rename／delete／mode changeはFile単位でRestoreする | Phase 5開始前 | 検討済み |

#### Phase 0前の検討結果（案）

| 対象 | 推奨案 | 判断理由 | 残る確認 |
|---|---|---|---|
| D-01 | pnpm workspace | 設計書のmonorepo構成とworkspace protocolの相性がよく、Web、Extension、共有packagesを一つのlockfileで管理できる | CIでのNode／pnpm固定方法 | 
| D-02 | Node.js LTSを採用し、CIとローカルのmajorを固定 | 最新版追従による差分を避け、Vite／Vueのサポート範囲と合わせやすい | 採用するLTS major | 
| D-03 | Goの安定版を1世代固定し、`go.mod`のtoolchainとCIで一致させる | Go Modulesで依存と再現性を管理しやすい | 採用するGo version | 
| D-04 | `net/http`と`http.ServeMux` | MVPのAPI数ではRouter依存を増やす必要がなく、標準機能で十分 | パスパラメータの実装規約 | 
| D-05 | `github.com/coder/websocket` | context対応、JSON helper、zero dependencyを備え、ManagerのWebSocket用途に適する | 採用versionとライセンス確認 | 
| D-06 | `modernc.org/sqlite` | CGO不要のpure Go構成にでき、VSIX同梱やcross buildの障害を減らせる | 実際のWindows／macOS／Linux build検証 | 
| D-07 | JSON Schemaをwire契約の正規ソースにする | TypeScriptとGoの型ずれをfixtureだけに依存せず検出できる | 型生成ツールとCI検証コマンド | 
| D-08 | HTTPは`/api/v1`、Eventは`schemaVersion` | API全体の互換性とEvent単位の再生互換性を分離できる | 最初のv1 endpoint一覧 | 
| D-10 | 3100を優先し、衝突時またはport=0時はOSの空きport | 開発時の分かりやすさと複数Workspace起動を両立できる | 空きportの通知方法 | 
| D-11 | runtime metadata JSONを主経路、stdoutの機械可読行を補助 | Browser開発とExtension起動の双方でManager情報を取得できる | metadata fileの権限とcleanup | 
| D-12 | OS標準のユーザーデータ配下に`maatgen/` | Repository外にSession DBとruntime metadataを保持できる | OS別の具体パス |
| D-13 | 対象repositoryのWorking Treeを直接使用 | 利用者とAgentが同じコードを編集でき、適用待ちをなくせる | checkpointと後続編集の競合検出 |
| D-15 | PATHから検出し、shellなしで`codex --version`を実行 | 任意コマンド実行を避け、診断画面に実体とversionを表示できる | Windowsの実行ファイル拡張子処理 | 

上記は検討結果の推奨案であり、ユーザー承認前は各行の「決定事項」を空欄のまま保持する。承認後に決定事項へ転記し、ステータスを「検討済み」へ変更する。

### 11.2 実装中に決めること

| ID | 決定対象 | 推奨（案） | 決定事項 | 決定タイミング | ステータス |
|---|---|---|---|---|---|
| D-33 | UIレイアウトとレスポンシブ方針 | Session履歴、会話、変更一覧の3ペインを基本とし、狭い画面では段階的に情報量を減らす | Desktopは左Session／中央Conversation／右Changesの3ペイン、1050px未満でChangesを省略、720px未満でSessionとConversationを縦積みにする | Phase 1 | 検討済み |
| D-34 | VS Codeテーマ変数の対応範囲 | foreground／background／font／border／button／focusをVS Codeテーマ変数へ合わせ、製品固有アクセントだけfallbackを持つ | Webviewの文字色、背景色、フォント、境界線、button、focus、status色は`--vscode-*`を使用する。製品アクセントは`--vscode-testing-iconPassed`を優先し、未定義時だけ固定色へfallbackする | Phase 1またはPhase 7 | 検討済み |
| D-35 | Event payloadの詳細フィールド | Agent共通表示に必要な最小フィールドへ正規化し、Codex固有の元JSONは別保存 | message／reasoningはtext、commandはcommand／status／output／exitCode、file changeはchanges、全itemにitemIdを保持 | 各Parser実装時 | 検討済み |
| D-36 | JSONLの不正行の表示方法 | Error Eventへ変換して後続行の処理を継続 | `invalid_codex_jsonl` Error Eventへ変換し、元行は有効なJSON wrapperに格納して後続行を継続 | Codex Adapter実装時 | 検討済み |
| D-37 | stderrのUI表示レベル | stderrは診断用Raw Eventとしてマスク後に保存し、通常UIには直接表示しない。Run失敗時は共通のError／Run Failed Eventを表示する | stderrはマスク済みRaw Eventとして保存する。stderr単独ではUI Eventにせず、終了結果に基づくRun Failed EventをUIへ表示する | Codex Adapter実装時 | 検討済み |
| D-38 | Usageが累積値の場合の集計方法 | `turn.completed`の値をRunの最終値として保存 | `turn.completed.usage`をRun単位の最終値として上書き保存し、Event間では加算しない | Usage fixture確認時 | 検討済み |
| D-39 | Session履歴のpagination | `createdAt`とSession IDによるkeyset paginationを使い、cursorはopaqueな文字列として扱う | `createdAt`とSession IDによるkeyset pagination。HTTPは`nextCursor`を返し、Web UIは25件ずつLoad moreで追加する | Phase 2またはPhase 6 | 検討済み |
| D-40 | APIエラーコードの詳細一覧 | — | — | API統合テスト前 | 未 |
| D-41 | WebSocket再接続backoff | 0.5秒開始、最大10秒の指数backoff。再接続ごとに短命ticketを再取得し、最後のsequenceから再開する | 0.5秒開始、最大10秒の指数backoff。再接続ごとに短命ticketを再取得し、最後のsequenceから再開する。受信成功時にbackoffをリセットする | Web UI統合時 | 検討済み |
| D-42 | 大きなDiff／ログの遅延読み込み | — | — | Web UI統合時 | 未 |
| D-43 | 新規・削除ファイルの表示UI | 既存Diffと同じ左右比較を使い、存在しない側を明示する | Original／Modifiedの左右比較を使い、存在しない内容は`∅`で表示する | Diff UI実装時 | 検討済み |
| D-44 | File mode変更の表示UI | File単位Restoreとして変更種別を明示する | 内容DiffではなくFile単位Restoreの案内とRestore操作を表示する | Diff UI実装時 | 検討済み |
| D-45 | checkpoint cleanup失敗時のユーザー通知 | APIエラーとSession状態の両方で通知し、再試行操作を可能にする | close APIは`503 checkpoint_cleanup_failed`を返し、Sessionへcleanup状態と試行回数を保存する。同じclose APIで再試行する | Checkpoint実装時 | 検討済み |
| D-46 | fake Codex CLIのfixture仕様 | Go test helper processでversion、JSONL、stderr、終了コード、遅延を再現 | Go test binaryのhelper modeとJSONL testdataを使用し、外部scriptへ依存しない | Codex Adapter着手前 | 検討済み |
| D-47 | CIでの実Codex手動テスト方法 | CIはfake CLIのみとし、実Codexはリリース前に検証用repositoryで主要フローを手動確認する | CIはfake CLIでUbuntu／Windows／macOSを検証する。実Codexはリリース前にSession作成、dirty状態のcheckpoint、Prompt、直接変更、Diff、Restore、継続Prompt、cleanupを手動確認する | CI構築時 | 検討済み |
| D-61 | Checkpoint実装方式 | Git plumbingとprivate refを使用し、ユーザーのindex／HEADを変更しない | 一時indexからbefore／after treeを作り、`refs/maatgen/checkpoints/<sessionId>/<runId>/`からtreeを直接参照する。ignored fileは対象外とする | Checkpoint実装時 | 検討済み |
| D-62 | Run間の継続と利用者編集 | Run完了後の現在Working Treeを次Runの基準にする | Sessionをactiveのまま保持し、次Promptの直前に新しいcheckpointを作って同じAgent Threadをresumeする。実行中の同一箇所編集は帰属を保証しない | Checkpoint実装時 | 検討済み |
| D-63 | 後方互換性 | 旧Worktree／Accept／Reject設計の互換性は持たない | 旧設計のlegacy mode、互換endpoint、dual schema、migration shimは実装せず、既存ローカルデータは必要に応じて再作成する | 設計変更時 | 検討済み |

### 11.3 Codex MVP後に決めること

| ID | 決定対象 | 推奨（案） | 決定事項 | 関連機能 | ステータス |
|---|---|---|---|---|---|
| D-48 | dirty Working Treeのsnapshot方式 | Git plumbingによるRun前checkpoint | D-61へ統合しMVP対象へ移動 | 未コミット変更対応 | 検討済み |
| D-49 | submodule対応方針 | — | — | 複合Repository | 未 |
| D-50 | Agent変更のUndo | Run前checkpointへのRestore | Accept／Rejectを廃止し、Hunk／File／Run単位RestoreとしてMVP対象へ移動 | Restore | 検討済み |
| D-51 | Checkpointの再開・期限切れ | active Session中は保持しclose時に削除 | D-28に統合 | 長期Session | 検討済み |
| D-52 | 実行中Promptのqueueing／steering | — | — | 複数Turn制御 | 未 |
| D-53 | binary Diffの専用表示 | — | — | Binary変更確認／Restore | 未 |
| D-54 | Cost計算の料金表管理 | — | — | 使用量分析 | 未 |
| D-55 | Agent比較指標 | — | — | Analytics | 未 |
| D-56 | Claude Codeの出力・resume方式 | print modeのstream-JSONとCLI管理のログイン情報を使用する | `--print --output-format stream-json --verbose --permission-mode bypassPermissions`を正規化する。Promptはstdinへ渡し、認証情報は保存せずCLIのログイン状態を利用する。`session_id`を`agentThreadId`へ保存して`--resume`する | Claude Adapter | 検討済み |
| D-65 | Claude Code Usageとコストの記録 | CLIが報告するtoken数とUSDコストをそのまま保存する | `result.usage`から`inputTokens`（`input_tokens`＋cache write＋cache read）、`cachedInputTokens`（cache read）、`outputTokens`を保存し、`result.total_cost_usd`をコストとする。`claude` providerの料金表取得と再計算は行わない | Claude Adapter | 検討済み |
| D-57 | Copilot CLIの出力・認証方式 | programmatic JSONLとCLI管理のログイン情報を使用 | `--output-format json`を正規化し、認証情報は保存せずCLIのログイン状態を利用する。Session IDは`agentThreadId`へ保存して`--resume`する | Copilot Adapter | 検討済み |
| D-64 | Copilot Usageの記録単位 | 実動作モデルとAI credits | `assistant.usage.model`を保存し、`copilotUsage.totalNanoAiu / 1_000_000_000`をRun内で加算する。Copilotのtoken数とモデル倍率`cost`は共通Usageへ保存しない | Copilot Adapter | 検討済み |
| D-66 | Session作成時のコード数計測 | cloc（`--vcs=git`）をSession作成時に一度だけ実行し結果を保存する | `cloc --vcs=git --json <workspace>`をSession作成のCreateSession内で同期実行し、`session_source_stats`テーブルへ言語別・合計行を保存する。Runごとの再計測は行わない。計測失敗（cloc未導入含む）はSession作成を失敗させず、コード数を未計測のまま扱う。UIはWeb版限定でUsage／Changesと並ぶ「コード数」Tabに表示する | Session作成／Web UI | 検討済み |
| D-58 | GitHub Issue／PR連携方式 | — | — | GitHub連携 | 未 |
| D-59 | Multi-Agent Sessionの分離単位 | — | — | 複数Agent実行 | 未 |
| D-60 | リモート実行・外部公開の認証 | — | — | 将来のServer化 | 未 |

### 11.4 決定記録のルール

決定した項目は、設計書本文を直接書き換えるだけでなく、次のDecision Recordへ残す。

```text
docs/decisions/
├─ 001-runtime-and-toolchain.md
├─ 002-codex-execution.md
├─ 003-session-and-run.md
├─ 004-direct-workspace-and-checkpoint.md
└─ 005-security-and-retention.md
```

各Decision Recordには、決定内容、背景、代替案、影響範囲、決定日、見直し条件を記録する。

## 12. 実装進捗

### Phase 0：基盤と契約

2026-08-15時点の進捗は次のとおり。

- [x] pnpm workspaceとlockfile
- [x] Node.js／TypeScriptの基礎設定
- [x] Go moduleとAgent Managerのentry point
- [x] `/api/v1/health`
- [x] `AgentSession`、`AgentRun`、`TokenUsage`、`SessionEvent`、`ChangeSet`のTypeScript型
- [x] 対応するGo Protocol型
- [x] `SessionEvent` JSON Schema
- [x] TypeScript／Goで共有するfixture
- [x] TypeScriptのtypecheck、contract test、build
- [x] GoのProtocol／HTTP handler test、build
- [x] 残りのProtocol JSON Schema
- [x] JSON SchemaからのTypeScript型生成と生成差分検出
- [x] 共通APIエラー形式
- [x] CI workflow

### Phase 1：Web UI

- [x] Vue 3／Vite Webアプリ基盤
- [x] 実Agent Managerへ接続する`AgentApi`
- [x] Session作成・履歴UI
- [x] Chat／Timeline／Cancel／Error表示
- [x] Changed Files／Hunk概要表示
- [x] Desktop 3ペインとモバイル向けレスポンシブ表示
- [x] `npm run dev`によるManager／Web UI同時起動
- [x] WebSocket streamingと再接続表示
- [x] Diff詳細表示
- [x] 旧Accept／Reject UIをCheckpoint Restore UIへ置換
- [x] Component testとMock scenario
- [x] Session履歴のkeyset pagination
- [x] Manager未起動、認証失敗、Codex未導入の診断表示
  - [x] UsageのRunカードから中央のRun詳細（トークン内訳、コマンド、関連Event）を表示し、チャットへ戻る導線を提供
  - [x] Run詳細表示はWeb版限定とし、VS Code版のUsage UIは変更しない

### Phase 2：Agent Manager基盤（先行実装）

- [x] `modernc.org/sqlite`と`database/sql`の導入
- [x] 初期SQLite migration
- [x] Sessionの作成・取得・一覧・Thread ID更新・close
- [x] Runの作成・取得・状態更新
- [x] foreign keyとmigrationの冪等性テスト
- [x] Manager起動時のデータディレクトリ作成とDB初期化
- [x] Event StoreとSession単位の自動sequence採番
- [x] `afterSequence`によるEvent再取得
- [x] Run Usage Store
- [x] redacted Raw Event Store
- [x] Change Store
- [x] Session一覧・詳細取得HTTP API
- [x] pagination parameterの検証と共通APIエラー変換
- [x] 256-bit token生成とruntime metadata
- [x] Bearer token認証
- [x] Event取得HTTP API
- [x] 30秒間有効な一回限りのWebSocket ticket
- [x] Origin allowlist
- [x] WebSocketによる既存／新着Event配信
- [x] SQLite pollingからセッション単位Event Brokerへの置き換え

### Phase 3：Codex Adapter（先行実装）

- [x] `codex`実行ファイルのPATH検出と絶対パス化
- [x] shellを介さない`codex --version`検証
- [x] `workspace-write`／approval `never`／stdin Promptの固定引数生成
- [x] 新規`exec`とThread `resume`の引数生成
- [x] stdout／stderrの行単位streaming基盤
- [x] Run単位timeoutとcontext cancel
- [x] Windows Job Object（親Job制約時は`taskkill /T`）／Unix process groupによるプロセスツリー終了
- [x] Go test helperによるfake Codex CLI
- [x] Codex JSONL parserと正規化Event候補への変換
- [x] Thread IDとRun単位Usageの抽出
- [x] 未知eventのraw保持と不正JSONLのError Event化
- [x] Run永続化とmessage／cancel API統合
- [x] Session単位の同時Run制限とgraceful shutdown時の実行停止
- [x] stdout／stderr Raw EventのSecret Masking

### Phase 4：Direct Working Tree、Checkpoint、ChangeSet（再設計）

旧Worktree方式は後方互換性を維持せず、直接Working Tree方式へ置換する。

- [x] Git実行ファイルの検出
- [x] text／binary／rename／delete／mode changeの分類
- [x] unified diffからのHunk生成と内容ベースSHA-256 ID
- [x] clean Working Tree必須条件を削除
- [x] detached worktree作成を廃止し、Agentのcwdを対象repositoryへ変更
- [x] Run前のHEAD／index／Working Tree checkpoint作成
- [x] Run後snapshotとprivate ref保存
- [x] Run単位のChangeSetへ移行
- [x] Session close時のcheckpoint ref cleanupとretry
- [x] 同一Sessionの次Run開始時に新checkpointを作成
- [x] 旧Worktree実装、Accept／Reject API、旧ChangeSet schemaの削除または置換

### Phase 5：Checkpoint Restore（再設計）

旧Accept／Reject方式は実装済みだが、後方互換性を維持せず置換する。

- [x] Accept／Reject APIと状態モデルを廃止
- [x] Hunk／File／Run全体のRestore API
- [x] binary／rename／delete／mode changeのFile単位Restore
- [x] after snapshotと現在内容による競合検出
- [x] atomic file writeと`409 checkpoint_conflict`
- [x] Restore状態とRestore Eventの永続化
- [x] 冪等な同一Restore操作の再送
- [x] 詳細DiffとRestore UI

### Phase 7：VS Code Extension

- [x] Extension workspaceとExtension Development Host起動設定
- [x] Explorer内の`WebviewViewProvider`
- [x] `default-src 'none'`とnonceによるWebview CSP
- [x] AgentのMarkdown結果をWeb UI／VS Code Webviewの表示密度に応じてレンダリング
- [x] Workspace名／pathのExtension-Webviewメッセージ連携
- [x] VS Codeテーマ変数に対応した初期UI
- [ ] Web UI componentと`AgentApi`契約の再利用
- [ ] Agent Manager起動、runtime metadata読込、health check
- [ ] Extension host経由のHTTP／WebSocket bridge
- [ ] `vscode.diff`とCheckpoint Restore command
- [ ] CodeLens／Decoration
- [ ] Extension終了時のManager cleanup（active Sessionのcheckpointは維持）

### Phase 9：GitHub Copilot CLI Adapter

- [x] `copilot`実行ファイルのPATH検出と`--version`検証
- [x] programmatic mode、permission bypass、remote export無効化の引数生成
- [x] `--resume=<sessionId>`による同一Session継続
- [x] Copilot JSONLからAssistant／Intent／Tool／Usage／Error Eventへの正規化（extended thinkingはRawのみ）
- [x] Agent別Adapter選択、Agent Thread ID、Raw Event sourceの共通化
- [x] Protocol／SQLite schemaのCodex＋Copilot対応
- [x] Web／VS CodeのProvider／Model選択とCLI診断表示
- [x] fake Copilot CLI、parser、複数Adapter統合テスト

### Phase 10：Codexコマンド承認

- [x] `commandApproval`設定とargv単位の許可ルール
- [x] 複合commandのsegment分割と全segment照合
- [x] Codex App Serverのread loop、pending response map、直列write
- [x] 軽量Codexモデルによる短命Diagnostic Reviewer
- [x] `waiting_for_approval`、approval Event、SQLite migration
- [x] pending一覧／decision HTTP APIと二重応答の競合検出
- [x] 1回、Session、永続、不許可のdecision scope
- [x] Web／VS Codeのpending復元と承認UI
- [x] cancel、timeout、Manager再起動時のfail-closed recovery
- [x] parser、rule、AI、human decision、永続化のGoテスト
- [ ] 実Codex CLIを使ったWindows／macOS／Linux手動統合試験
