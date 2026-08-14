# Coding Agent 実装計画

## 1. 方針

本計画は、[coding-agent-design.md](./coding-agent-design.md) を実装可能な単位へ分解したものである。

初期リリースでは Codex CLI のみを対象とする。Claude Code と GitHub Copilot CLI は、Codex対応版で共通Protocol、Session管理、Diff Review、Extension連携が安定した後に追加する。

初期のCodex連携には、安定扱いの `codex exec --json` を使用する。Codex App Serverはexperimentalであるため、初期実装では採用しない。

## 2. MVPの完成条件

以下の一連のフローがブラウザから実行できることをMVPの完成条件とする。

1. Agent Managerをlocalhostで起動する
2. cleanなGitリポジトリからSessionを作成する
3. Agent専用worktree上でCodexを実行する
4. CodexのJSONL出力をリアルタイム表示する
5. Prompt、Assistant message、Command、File change、Usage、Raw Eventを保存する
6. Agent worktreeの実Git差分からChangeSetを作成する
7. Diffを表示し、Hunk単位でAccept／Rejectする
8. Acceptした変更だけをメインWorking Treeへ反映する
9. Session履歴を再起動後も表示できる
10. WebSocket切断後にEventを再取得して表示を復元する

## 3. 先に固定する設計判断

### 3.1 Codexの実行方式

```text
codex exec --json --sandbox workspace-write --cd <worktree> -
```

Promptはstdinから渡す。stdoutはJSONL、stderrは診断ログとして扱う。

追加メッセージは次の形でCodex Threadを再開する。

```text
codex exec resume <codexThreadId> --json --cd <worktree> -
```

App Server、TUIのPTY制御、Codex内部データベースの直接参照は初期対象外とする。

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
  agent: 'codex';
  workspace: string;
  worktree: string;
  baseCommit: string;
  codexThreadId?: string;
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

初期版はCodexのみ実装するが、Agent ManagerからCLI固有処理を分離する。

```go
type AgentAdapter interface {
    Name() string
    Check(ctx context.Context) (AgentInfo, error)
    Run(ctx context.Context, req RunRequest, emit EventEmitter) (RunResult, error)
}
```

```text
AgentAdapter
└─ CodexAdapter

将来
├─ ClaudeAdapter
└─ CopilotAdapter
```

Claude／Copilotの仕様が確定するまでは、過度なcapability抽象化は行わない。

### 3.4 Eventモデル

ユーザー操作とManager内部イベントも記録できるよう、`AgentEvent`ではなく`SessionEvent`とする。

```ts
interface SessionEvent {
  id: string;
  sessionId: string;
  runId?: string;
  sequence: number;
  timestamp: string;
  schemaVersion: 1;
  source: 'user' | 'codex' | 'manager';
  type:
    | 'user_prompt'
    | 'assistant_message'
    | 'reasoning_summary'
    | 'command_started'
    | 'command_completed'
    | 'file_change_reported'
    | 'usage_reported'
    | 'change_detected'
    | 'change_reviewed'
    | 'run_started'
    | 'run_completed'
    | 'run_failed'
    | 'run_cancelled'
    | 'error';
  data: unknown;
}
```

Codexの`thread.started`から得られるThread IDは、ManagerのSession IDとは別に保存する。

`reasoning`はCodexが出力した要約だけを保存し、内部推論を再構成・推測しない。

### 3.5 Worktree

初期版では、メインWorking TreeがcleanであることをSession開始条件とする。

```text
ユーザーのリポジトリ
└─ メインWorking Tree

ユーザーデータディレクトリ
└─ maatgen/worktrees/<sessionId>
   └─ Agent Worktree
```

Agent worktreeはリポジトリ外に `git worktree add --detach` で作成する。Session作成時のHEADを`baseCommit`として固定する。

dirty treeのスナップショット、submodule、非GitディレクトリはMVP後に対応する。

### 3.6 ChangeSet

Hunkだけでなく、新規、削除、rename、binary、file mode変更を表現できるようにする。

```ts
interface FileChange {
  id: string;
  oldPath?: string;
  newPath?: string;
  kind: 'modify' | 'add' | 'delete' | 'rename' | 'binary' | 'mode_change';
  original?: string;
  modified?: string;
  reviewMode: 'hunk' | 'file';
  status: 'pending' | 'partially_accepted' | 'accepted' | 'rejected';
  hunks: ChangeHunk[];
}
```

通常のテキスト変更はHunk単位、binaryやrenameなどはFile単位でReviewする。

### 3.7 Accept／Reject

MVPではReview操作を不可逆とする。

```text
pending → accepted
pending → rejected
```

Accept時は、base内容と既にAccept済みのHunkから期待ファイル内容を再生成し、メインWorking Treeのhashが期待値と一致する場合だけatomicに反映する。一致しない場合は上書きせず`409 Conflict`を返す。

RejectはメインWorking Treeを変更せず、状態とReview Eventだけを保存する。

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
- Hunk／File単位のAccept／Reject
- 正常、失敗、Cancel、複数HunkのMock scenario

完了条件:

- `pnpm dev:mock`で主要フローを確認できる
- Component testでEvent描画とReview操作を検証できる

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

### Phase 4：Git WorktreeとChangeSet

- clean tree検証
- base commit取得
- Session専用detached worktree作成
- Agentのcwdをworktreeに固定
- 実行前後のGit状態取得
- text／binary／rename／delete／mode changeの差分検出
- unified diffからHunk生成
- 内容hashベースのHunk ID生成
- Session終了時のcleanupとcleanup retry

完了条件:

- Agent実行中にメインWorking Treeが変更されない
- Agentが申告しない変更もGit差分として検出できる
- 複数ファイルと複数Hunkを再現できる

### Phase 5：Review適用

- Hunk patch生成
- File単位変更の適用
- base hash／期待hash検証
- Accept／Reject API
- Accept All／Reject All
- `409 Conflict`処理
- Review Event保存
- 冪等な再送処理

完了条件:

- 一部HunkだけをメインWorking Treeへ反映できる
- 外部変更を上書きしない
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
  → Agent Worktree
  → ChangeSet
  → Review
  → Main Working Tree
```

### Phase 7：VS Code Extension

- WebviewViewProvider
- CSPとnonce
- Web UI packageの再利用
- Agent Manager起動とhealth check
- Workspace情報連携
- `vscode.diff`
- WorkspaceEdit
- CodeLens／Decoration
- Extension終了時のManager cleanup

ExtensionにはCodex固有の実行ロジックを持たせない。

### Phase 8：VSIX化

- GoバイナリのWindows／Linux／macOS向けcross build
- VSIXへのバイナリ同梱
- `vsce package`
- checksum、version、Third-party notice
- GitHub Releases用成果物

### Phase 9：追加Adapter

Codex MVPの完了後に、同じ`AgentAdapter`と`SessionEvent`契約へClaude Code、GitHub Copilot CLIを追加する。

追加順序は、各CLIの構造化出力、resume方式、Usage、cancel方式を調査して決定する。UIやSession管理へCLI固有分岐を追加しない。

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
POST   /api/sessions/{id}/changes/{hunkId}/accept
POST   /api/sessions/{id}/changes/{hunkId}/reject
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
worktree
codex_thread_id
base_commit
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
source
raw_json
```

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
file_path
old_path
change_kind
review_mode
old_start
old_lines
new_start
new_lines
original_text
modified_text
status
base_hash
expected_hash
reviewed_at
created_at
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
- Accept patch再生成

### Integration test

- SQLite migrationとCRUD
- HTTP認証
- WebSocket ticket
- Event sequenceと再取得
- fake Codex process
- process cancel／timeout
- Git worktree
- Accept時の競合検出

### E2E test

- Mock UIフロー
- Browser → Manager → fake Codex → diff → review
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
- Agent worktreeのパスをユーザー入力で任意指定させない
- Process起動時の引数は配列で渡し、shell経由で実行しない
- Sessionごとに同時Runを1つに制限する

## 10. 将来拡張

以下はCodex MVP後に対応する。

- dirty Working Treeのsnapshot
- submodule
- Hunk ReviewのUndo
- queueing／steering
- binary差分の専用UI
- Agent比較、Cost集計、成功率
- Claude Code Adapter
- GitHub Copilot CLI Adapter
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
| D-07 | Protocolの正規ソース | JSON Schemaをwire契約の正規ソースとし、TypeScript型とGo型を生成・検証 | JSON Schemaと共有fixtureをwire契約の正規ソースとする | Phase 0開始前 | 検討済み |
| D-08 | APIのバージョニング | HTTPは`/api/v1`、Eventは`schemaVersion`を併用 | HTTPは`/api/v1`、Eventは`schemaVersion`を併用 | Phase 0開始前 | 検討済み |
| D-09 | Agent Managerのbind先 | `127.0.0.1`のみ | `127.0.0.1`のみ | Phase 0開始前 | 検討済み |
| D-10 | Portの割当 | 3100を優先し、使用中またはport=0指定時はOSの空きportを使用 | 3100とし、引数で指定可能とする | Phase 0開始前 | 検討済み |
| D-11 | Port／tokenの通知方法 | 親プロセス指定のruntime metadata fileへJSONを書き、stdoutにも機械可読な1行を出力 | — | Phase 0開始前 | 検討済み |
| D-12 | Managerデータ保存先 | OS標準のユーザーデータディレクトリ配下の`maatgen/` | — | Phase 0開始前 | 検討済み |
| D-13 | Worktree保存先 | Managerデータディレクトリ配下 | — | Phase 0開始前 | 検討済み |
| D-14 | Session開始条件 | Git repositoryかつclean Working Tree | Git repositoryかつclean Working Tree | Phase 0開始前 | 検討済み |
| D-15 | Codex実行バイナリ検出 | PATHから検出し、shellを介さず`codex --version`で検証・絶対パスとversionを保存 | — | Phase 0開始前 | 検討済み |
| D-16 | Codex sandbox policy | `workspace-write` | `workspace-write` | Codex Adapter着手前 | 検討済み |
| D-17 | Codex approval policy | 初期版は明示的な固定値として設定 | — | Codex Adapter着手前 | 検討済み |
| D-18 | Codex model指定 | 未指定時はCodex設定に委譲し、要求値だけ保存 | — | Codex Adapter着手前 | 検討済み |
| D-19 | Codex timeout | Run単位の上限時間を設定可能にする | — | Codex Adapter着手前 | 検討済み |
| D-20 | 認証方式 | HTTP Bearer token、WebSocket短命ticket | HTTP Bearer token、WebSocket短命ticket | Phase 2開始前 | 検討済み |
| D-21 | 許可Origin | Vite開発URLとVS Code Webviewのみ | — | Phase 2開始前 | 検討中 |
| D-22 | Raw Event保存方針 | マスク済みJSONのみ保存 | マスク済みJSONのみ保存 | Phase 2開始前 | 検討済み |
| D-23 | Secret Masking対象 | API key、token、password、環境変数名のallowlist | — | Phase 2開始前 | 検討中 |
| D-24 | ログ保持期間 | 初期値と最大DBサイズを設定可能にする | — | Phase 2開始前 | 未 |
| D-25 | 同時実行制限 | SessionごとにRunを1つまで | SessionごとにRunを1つまで | Phase 2開始前 | 検討済み |
| D-26 | 実行中の追加Prompt | 初期版は409で拒否 | 初期版は409で拒否 | Phase 2開始前 | 検討済み |
| D-27 | Session終了操作 | 明示的なclose APIを用意 | 明示的なclose APIを用意 | Phase 2開始前 | 検討済み |
| D-28 | Worktree cleanup | 全Review完了またはclose後に実行、失敗は再試行 | — | Phase 4開始前 | 検討中 |
| D-29 | Hunk ID方式 | 内容を含むSHA-256 | 内容を含むSHA-256 | Phase 4開始前 | 検討済み |
| D-30 | Accept／Rejectの可逆性 | MVPでは不可逆 | MVPでは不可逆 | Phase 5開始前 | 検討済み |
| D-31 | Accept競合時の動作 | 上書きせず409 Conflict | 上書きせず409 Conflict | Phase 5開始前 | 検討済み |
| D-32 | binary／renameのReview単位 | File単位 | File単位 | Phase 5開始前 | 検討済み |

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
| D-12 | OS標準のユーザーデータ配下に`maatgen/` | Repositoryを汚さず、Session DBとworktreeを一元管理できる | OS別の具体パス | 
| D-13 | D-12配下の`worktrees/<sessionId>` | DB、metadata、worktreeのライフサイクルを揃えやすい | 長いパスとcleanup retry | 
| D-15 | PATHから検出し、shellなしで`codex --version`を実行 | 任意コマンド実行を避け、診断画面に実体とversionを表示できる | Windowsの実行ファイル拡張子処理 | 

上記は検討結果の推奨案であり、ユーザー承認前は各行の「決定事項」を空欄のまま保持する。承認後に決定事項へ転記し、ステータスを「検討済み」へ変更する。

### 11.2 実装中に決めること

| ID | 決定対象 | 推奨（案） | 決定事項 | 決定タイミング | ステータス |
|---|---|---|---|---|---|
| D-33 | UIレイアウトとレスポンシブ方針 | — | — | Phase 1 | 未 |
| D-34 | VS Codeテーマ変数の対応範囲 | — | — | Phase 1またはPhase 7 | 未 |
| D-35 | Event payloadの詳細フィールド | — | — | 各Parser実装時 | 未 |
| D-36 | JSONLの不正行の表示方法 | — | — | Codex Adapter実装時 | 未 |
| D-37 | stderrのUI表示レベル | — | — | Codex Adapter実装時 | 未 |
| D-38 | Usageが累積値の場合の集計方法 | — | — | Usage fixture確認時 | 未 |
| D-39 | Session履歴のpagination | — | — | Phase 2またはPhase 6 | 未 |
| D-40 | APIエラーコードの詳細一覧 | — | — | API統合テスト前 | 未 |
| D-41 | WebSocket再接続backoff | — | — | Web UI統合時 | 未 |
| D-42 | 大きなDiff／ログの遅延読み込み | — | — | Web UI統合時 | 未 |
| D-43 | 新規・削除ファイルの表示UI | — | — | Diff UI実装時 | 未 |
| D-44 | File mode変更の表示UI | — | — | Diff UI実装時 | 未 |
| D-45 | cleanup失敗時のユーザー通知 | — | — | Worktree実装時 | 未 |
| D-46 | fake Codex CLIのfixture仕様 | — | — | Codex Adapter着手前 | 未 |
| D-47 | CIでの実Codex手動テスト方法 | — | — | CI構築時 | 未 |

### 11.3 Codex MVP後に決めること

| ID | 決定対象 | 推奨（案） | 決定事項 | 関連機能 | ステータス |
|---|---|---|---|---|---|
| D-48 | dirty Working Treeのsnapshot方式 | — | — | 未コミット変更対応 | 未 |
| D-49 | submodule対応方針 | — | — | 複合Repository | 未 |
| D-50 | Accept済みHunkのUndo | — | — | Review改善 | 未 |
| D-51 | Reviewの再開・期限切れ | — | — | 長期Session | 未 |
| D-52 | 実行中Promptのqueueing／steering | — | — | 複数Turn制御 | 未 |
| D-53 | binary Diffの専用表示 | — | — | Binary変更Review | 未 |
| D-54 | Cost計算の料金表管理 | — | — | 使用量分析 | 未 |
| D-55 | Agent比較指標 | — | — | Analytics | 未 |
| D-56 | Claude Codeの出力・resume方式 | — | — | Claude Adapter | 未 |
| D-57 | Copilot CLIの出力・認証方式 | — | — | Copilot Adapter | 未 |
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
├─ 004-worktree-and-review.md
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
- [ ] 残りのProtocol JSON Schema
- [ ] JSON Schemaからの型生成
- [ ] 共通APIエラー形式
- [ ] CI workflow
