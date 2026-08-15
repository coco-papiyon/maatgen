# maatgen

VS CodeおよびWebブラウザからCodex CLIを実行・監視し、変更のDiff確認とRestoreを行うCoding Agent Managerです。

現在はCodex Adapter、Session／Run管理、対象Repositoryの直接編集、Checkpoint／Diff／Restore、Web UI、VS Code拡張機能を実装しています。

## 必要な環境

- Node.js 20.19以上
- Corepack
- Go 1.22.5以上
- Git

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

Web版の本番ビルド、Webパッケージの作成、VSCode版のビルドをまとめて実行するには、リポジトリルートで次を実行します。

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

Web版をローカルで確認する場合は、Agent Managerを起動したうえで開発サーバーを使用します。

```bash
npm run dev
```

ブラウザで`http://127.0.0.1:5173/`を開いてください。本番配置では`apps/web/dist`を静的ファイルとしてWebサーバーへ配置します。

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

VSCode版のビルドだけを実行する場合は次のコマンドです。

```bash
npm --prefix apps/vscode-extension run build
```

GitHub Actionsでは、Web／Protocolの生成差分・型検査・テスト・ビルドと、Ubuntu／Windows／macOSでのGoテスト・ビルドを実行します。CIはfake Codex CLIを使用し、実Codexは起動しません。

実Codexのリリース前確認では、`codex --version`が成功する環境で`npm run dev`を起動し、cleanな検証用repositoryを使ってSession作成、Prompt実行、Diff表示、Accept／Reject、worktree cleanupまでを手動確認します。

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
POST http://127.0.0.1:3100/api/v1/sessions/{sessionId}/changes/{changeId}/accept
POST http://127.0.0.1:3100/api/v1/sessions/{sessionId}/changes/{changeId}/reject
POST http://127.0.0.1:3100/api/v1/sessions/{sessionId}/changes/accept-all
POST http://127.0.0.1:3100/api/v1/sessions/{sessionId}/changes/reject-all
POST http://127.0.0.1:3100/api/v1/runs/{runId}/cancel
POST http://127.0.0.1:3100/api/v1/ws-tickets
```

Session作成時はGitリポジトリのcleanなWorking Treeを指定します。Managerは`HEAD`を固定したdetached worktreeをデータディレクトリの`worktrees/<sessionId>`へ作成します。

```json
{
  "agent": "codex",
  "workspace": "C:/path/to/repository"
}
```

CodexへPromptを送るとRunが非同期で開始されます。同じSessionで実行中のRunがある場合は`409 Conflict`になります。`model`と`timeoutSeconds`は省略可能です。

Providerとモデル候補は`GET /api/v1/providers`で取得できます。現時点のProviderはCodexのみです。起動時にexperimentalな`codex debug models`を5秒の上限で実行し、利用できる場合はCodexが返したモデル一覧を使用します。コマンドが未対応、Codexが未導入、未認証、タイムアウト、または応答を解釈できない場合はManagerの設定ファイルへフォールバックします。既定の相対パスは`config/providers.json`で、通常はAgent Manager実行ファイルのあるディレクトリを基準に解決します。`go run`で起動した場合はGoが作成する一時実行ファイルではなく、起動時のカレントディレクトリを基準にします。`--config`で別の相対パスへ変更できます。Web UIで変更したモデルは`defaultModel`として同じ設定ファイルへ保存され、次回起動時に引き継がれます。外部ファイルがない場合は組み込みの既定値を使用します。

```json
{
  "providers": [
    { "id": "codex", "label": "Codex", "models": ["gpt-5.6-sol", "gpt-5.6-terra"] }
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

Runが正常終了すると、Session開始時の`baseCommit`とAgent worktreeの実差分からChangeSetが更新されます。通常のテキスト変更はHunk単位、binary・rename・file mode変更はFile単位で返されます。

Web UIのChanges一覧から差分詳細を開き、HunkまたはFile単位でAccept／Rejectできます。未Reviewの変更はAccept All／Reject Allで一括処理できます。Accept時にメインWorking Treeが想定状態から変更されていた場合は`409 Conflict`となり、外部変更を上書きしません。一括Acceptが途中で競合した場合はその時点で停止し、確定済みのReview状態は保持されます。

Reviewの確定後もSessionと会話履歴は維持され、同じCodex threadへ続けて指示を送れます。Sessionを明示的に閉じるとAgent worktreeが削除されます。実行中のRunがある場合は`409 Conflict`となります。削除に失敗した場合はSessionへcleanup状態が保存され、close APIまたは同じReview操作を再送するとcleanupを再試行します。

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

Session履歴は作成日時とSession IDによるkeyset paginationで25件ずつ読み込みます。レスポンスに`nextCursor`がある場合、同じ値を次のリクエストの`cursor`へ指定します。Web UIはManagerへ接続できない場合、認証に失敗した場合、Codex CLIを利用できない場合を区別して対処方法を表示します。

空きportをOSに選択させる場合は次のように起動します。

```bash
npm run dev:manager -- --port 0
```

SQLite DBの保存先を変更する場合は`--data-dir`を指定します。未指定時はOSのユーザー設定ディレクトリ配下に`maatgen/manager.db`を作成します。

```bash
npm run dev:manager -- --data-dir ./.maatgen
```

runtime metadataの出力先と許可するブラウザOriginは、それぞれ`--runtime-file`、`--allowed-origins`で変更できます。

## ドキュメント

- [設計書](./docs/coding-agent-design.md)
- [実装計画](./docs/implementation-plan.md)
