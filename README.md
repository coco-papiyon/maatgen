# maatgen

VS CodeおよびWebブラウザからCodex CLI／Claude Code CLI／GitHub Copilot CLIを実行・監視し、変更のDiff確認とRestoreを行うCoding Agent Managerです。

現在はCodex／Claude Code／GitHub Copilot Adapter、Session／Run管理、対象Repositoryの直接編集、Checkpoint／Diff／Restore、Web UI、VS Code拡張機能を実装しています。

## 必要な環境

- Node.js 20.19以上
- Corepack
- Go 1.22.5以上
- Git
- Codex CLI、Claude Code CLI、またはGitHub Copilot CLI（使用するProviderのみで可）

## セットアップ

```bash
corepack pnpm install
```

## 検証

```bash
corepack pnpm typecheck
corepack pnpm test
corepack pnpm build
```

ProtocolのTypeScript型は`packages/protocol/schema`のJSON Schemaから生成します。Schemaを変更した場合は次を実行します。typecheck、test、buildでは生成物が最新か自動確認されます。

```bash
npm run generate:protocol
npm run generate:check
```

## ビルドとインストール

### 開発用フロントエンドビルド

Web版のみをビルドしてWebパッケージやVSCode拡張機能を作成します（Agent Managerのバイナリは含みません）。

```bash
npm run build:frontend
```

生成物は次の場所に出力されます。

```text
apps/web/dist/                         # Web版の静的ファイル
artifacts/maatgen-web-0.1.0.tgz        # Web版パッケージ
artifacts/maatgen-0.1.0.vsix           # VSCode拡張機能パッケージ
apps/vscode-extension/dist/            # VSCode版のビルド成果物
```

### 本番用リリースビルド

Web版とAgent Managerの両方をビルドして、プラットフォーム別配布パッケージを作成します。

```bash
npm run build:release
```

このコマンドは以下を生成します：

```text
artifacts/maatgen-web-0.1.0-win32-x64.zip    # Windows版（バイナリ+静的ファイル）
artifacts/maatgen-web-0.1.0-linux-x64.zip    # Linux版（バイナリ+静的ファイル）
artifacts/maatgen-web-0.1.0-darwin-arm64.zip # macOS版（バイナリ+静的ファイル）
artifacts/maatgen-0.1.0.vsix                  # VS Code拡張機能
```

各ZIPにはAgent Managerバイナリ、Web UI静的ファイル（`web/dist`）、設定ファイルが含まれています。詳細は[本番配置ガイド](./docs/deploy.md)を参照してください。

### 配置

Web版は静的ファイルのみを出力し、単体のWebサーバー機能は持ちません。配信はAgent Managerが行います。本番配置ではZIPを展開して、Agent Managerバイナリをそのまま実行してください。Agent Managerは同じディレクトリの`web/dist`を自動検出して、`http://127.0.0.1:3100/`でAPIと静的ファイルの両方を配信します。

Web版をローカルで確認する場合は、Agent Managerを起動したうえで開発サーバーを使用します。

```bash
npm run dev
```

ブラウザで`http://127.0.0.1:5173/`を開いてください。このVite開発サーバーはHMR用のローカル開発ツールであり、`/api`と`/ws`をAgent Manager(`127.0.0.1:3100`)へProxyします。`apps/web/dist`をビルド済みの状態でAgent Managerを起動すると、`http://127.0.0.1:3100/`から直接Web版を確認できます（`--static-dir`の既定検出は下記「開発サーバー」を参照）。

### VS Code版（VSIX）のインストール

VSIXを作成してインストールするには、リポジトリルートで次を実行します。

```bash
corepack pnpm install
npm run build:frontend
```

`artifacts/maatgen-0.1.0.vsix`が生成されます。VS Codeの画面からインストールする場合は、次の手順です。

1. コマンドパレット（`Ctrl+Shift+P`／`Cmd+Shift+P`）を開く
2. `Extensions: Install from VSIX...`を選択する
3. `artifacts/maatgen-0.1.0.vsix`を選択する
4. VS CodeをReloadする

`code`コマンドが使用できる場合は、CLIからもインストールできます。

```bash
code --install-extension artifacts/maatgen-0.1.0.vsix --force
```

既存のMaatgenがインストール済みの場合は、同じ手順でVSIXを指定するか、CLIの`--force`を付けて更新できます。インストール後は、Extensionsビューで`Maatgen`を検索して確認してください。

開発用Extension Development Hostを使用する場合は、VS Codeでリポジトリを開き、`apps/vscode-extension`を対象に`F5`または「Run Extension」を実行します。この方法はVSIXをインストールせずに拡張機能を検証する場合に使用します。

VS Code版のSession画面ではCodex、Claude Code、GitHub CopilotとModelを選択できます。ModelはRunごとに選択でき、`Default model`を選ぶとAgent Managerの既定値を使用します。Provider一覧とModel一覧はAgent Managerの`/api/v1/providers`から取得します。

VSCode版のビルドだけを実行する場合は次のコマンドです。

```bash
npm --prefix apps/vscode-extension run build
```

GitHub Actionsでは、Web／Protocolの生成差分・型検査・テスト・ビルドと、Ubuntu／Windows／macOSでのGoテスト・ビルドを実行します。CIはfake Codex／Claude Code／Copilot CLIを使用し、実CLIは起動しません。

実CLIのリリース前確認では、`codex --version`、またはログイン済み環境の`claude --version`／`copilot --version`が成功する状態で`npm run dev`を起動し、Session作成、Prompt実行、直接編集、Diff表示、Restore、継続Promptまでを手動確認します。

## 開発サーバー

```bash
npm run dev
```

Agent ManagerとWeb UIが同時に起動します。ブラウザでは次のURLを開きます。

```text
http://127.0.0.1:5173/
```

CodexやAgent Managerを起動せず、正常完了、失敗、Cancel、複数Hunkの画面を確認する場合はMockモードを使用します。

```bash
npm run dev:mock
```

Agent Manager APIは`127.0.0.1:3100`で起動します。以前に起動したManagerが残っている場合は、先にそのターミナルで`Ctrl+C`を押して停止してください。

Managerだけを起動する場合は次を使用します。追加の起動オプションは`--`以降へ指定できます。

```bash
npm run dev:manager -- --port 0 --data-dir ./.maatgen
```

主なendpointは次のとおりです。

```text
GET http://127.0.0.1:3100/api/v1/health
GET http://127.0.0.1:3100/api/v1/providers
POST http://127.0.0.1:3100/api/v1/sessions
GET http://127.0.0.1:3100/api/v1/sessions?limit=25&cursor={nextCursor}
GET http://127.0.0.1:3100/api/v1/sessions/{sessionId}
POST http://127.0.0.1:3100/api/v1/sessions/{sessionId}/messages
POST http://127.0.0.1:3100/api/v1/sessions/{sessionId}/close
GET http://127.0.0.1:3100/api/v1/sessions/{sessionId}/events?afterSequence=0
GET http://127.0.0.1:3100/api/v1/sessions/{sessionId}/changes
POST http://127.0.0.1:3100/api/v1/sessions/{sessionId}/checkpoints/{checkpointId}/restore
POST http://127.0.0.1:3100/api/v1/sessions/{sessionId}/checkpoints/{checkpointId}/files/{fileId}/restore
POST http://127.0.0.1:3100/api/v1/sessions/{sessionId}/checkpoints/{checkpointId}/hunks/{hunkId}/restore
POST http://127.0.0.1:3100/api/v1/runs/{runId}/cancel
POST http://127.0.0.1:3100/api/v1/ws-tickets
```

Session作成時はGitリポジトリを指定します。dirtyなWorking Treeも利用でき、ManagerはRun直前にtracked fileとuntrackedかつnon-ignored fileをcheckpointへ保存してから対象Working Treeを直接編集します。

```json
{
  "agent": "codex",
  "workspace": "C:/path/to/repository"
}
```

選択したAgentへPromptを送るとRunが非同期で開始されます。同じSessionで実行中のRunがある場合は`409 Conflict`になります。`model`と`timeoutSeconds`は省略可能です。Copilotは`--output-format json --allow-all --no-ask-user`で、Claude Codeは`--print --output-format stream-json --verbose --permission-mode bypassPermissions`で実行し、保存したSession IDを`--resume`へ渡して継続します。Claude CodeへのPromptはstdinで渡します。

Providerとモデル候補は`GET /api/v1/providers`で取得できます。ProviderはCodex、Claude Code、GitHub Copilotです。Agent Manager起動時に各CLIの`--version`実行有無を並行して確認し（5秒の上限）、実行ファイルが見つからないProviderは一覧から除外します。設定ファイル自体は変更しないため、後からCLIを導入して再起動すれば再び表示されます。Codexは起動時に`codex debug models`を5秒の上限で実行し、失敗時は設定ファイルへフォールバックします。Claude CodeとCopilotは公式CLIのモデル名を設定ファイルから読みます。Claude Codeは既定で`defaultModel`を持たないため、Modelを選ばない場合は`--model`を渡さずCLI側の既定モデルを使用します。既定の相対パスは`config/providers.json`で、通常はAgent Manager実行ファイルのあるディレクトリ、`go run`では起動時のカレントディレクトリを基準にします。Web UIで変更したモデルは`defaultModel`として保存されます。

```json
{
  "providers": [
    { "id": "codex", "label": "Codex", "models": ["gpt-5.6-sol", "gpt-5.6-terra"] },
    { "id": "claude", "label": "Claude Code", "models": ["claude-opus-5", "claude-sonnet-5", "claude-sonnet-4-6", "claude-haiku-4-5"] },
    { "id": "copilot", "label": "GitHub Copilot", "models": ["auto", "claude-sonnet-4.6", "gpt-5.4"] }
  ]
}
```

```json
{
  "message": "テストを追加して",
  "model": "gpt-5",
  "timeoutSeconds": 1800
}
```

Runが終端状態になるとRun前後のcheckpointからChangeSetが更新されます。通常のテキスト変更はHunk単位、binary・rename・file mode変更はFile単位で返されます。

変更はRun完了時点ですでに利用できます。Web UIのChanges一覧から不要なHunk／File／Run全体だけをRun前checkpointへRestoreできます。現在内容がafter snapshotから変わっている場合は`409 checkpoint_conflict`となり、後続の利用者編集を上書きしません。

Run完了後もSessionと会話履歴は維持され、同じCodex thread／Claude Code session／Copilot sessionへ続けて指示を送れます。Sessionを閉じるとprivate checkpoint refをcleanupします。実行中のRunがある場合は`409 Conflict`となります。

health以外のHTTP APIには`Authorization: Bearer <token>`が必要です。起動時に生成されるtokenと実際のlisten addressは、データディレクトリ内の`runtime.json`へ保存されます。tokenはログへ出力されません。

WebSocket接続では、最初に`POST /api/v1/ws-tickets`で30秒間有効な一回限りのticketを取得します。接続時は次のsubprotocolを指定します。

```text
maatgen.v1
ticket.<ticket>
```

```text
WS /ws?sessionId={sessionId}&afterSequence={sequence}
```

Web UIはSession選択後にWebSocketでEventを受信します。切断時は0.5秒から最大10秒の指数backoffで再接続し、最後に受信したsequence以降を再取得します。再送されたEventはsequenceで重複排除されます。接続状態は画面右上に表示されます。

Session履歴は作成日時とSession IDによるkeyset paginationで25件ずつ読み込みます。レスポンスに`nextCursor`がある場合、同じ値を次のリクエストの`cursor`へ指定します。Web UIはManager接続失敗、認証失敗、Codex／Claude Code／Copilot CLI未導入を区別して対処方法を表示します。

空きportをOSに選択させる場合は次のように起動します。

```bash
npm run dev:manager -- --port 0
```

SQLite DBの保存先を変更する場合は`--data-dir`を指定します。未指定時はOSのユーザー設定ディレクトリ配下に`maatgen/manager.db`を作成します。

```bash
npm run dev:manager -- --data-dir ./.maatgen
```

runtime metadataの出力先と許可するブラウザOriginは、それぞれ`--runtime-file`、`--allowed-origins`で変更できます。

静的ファイルの配信先ディレクトリは`--static-dir`で明示的に指定できます。省略時は実行ファイルと同じ階層の`web/dist`、次いでカレントディレクトリの一つ上の`web/dist`（`npm run dev:manager`のようにリポジトリの`apps/agent-manager`をカレントディレクトリとして起動した場合は`apps/web/dist`）を自動検出します。いずれも見つからない場合はAPIのみを配信します。

## ドキュメント

- [設計書](./docs/coding-agent-design.md)
- [実装計画](./docs/implementation-plan.md)
