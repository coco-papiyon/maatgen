# Maatgen本番配置ガイド

## 概要

Maatgenは単一のAgent Managerバイナリと静的Web UIファイルで構成されます。本番環境では、これらを一つのディレクトリに配置して起動するだけです。

## リリースビルド

本番用のプラットフォーム別配布パッケージを作成します。リポジトリルートで以下を実行してください。

```bash
npm run build:release
```

このコマンドは以下を実行します：

1. Web UIをビルド（TypeScriptの型検査、Viteビルド）
2. Agent ManagerをビルドするためのWindowsなし設定（CGO_ENABLED=0）
3. 各プラットフォーム（Windows x64、Linux x64、macOS ARM64）用のバイナリを生成
4. Web UIの静的ファイルとバイナリを各プラットフォーム用のZIPにパッケージ
5. VS Code拡張機能（.vsix）をビルド

### 成果物

`artifacts/`ディレクトリに以下が生成されます：

```text
artifacts/
├─ maatgen-web-0.1.0-win32-x64.zip    # Windows x64
├─ maatgen-web-0.1.0-linux-x64.zip    # Linux x64
├─ maatgen-web-0.1.0-darwin-arm64.zip # macOS ARM64
└─ maatgen-0.1.0.vsix                  # VS Code拡張機能
```

## ディレクトリ構成

各ZIPには以下の構成が含まれます：

```text
maatgen-web-0.1.0-<platform>/
├─ agent-manager         # または agent-manager.exe
├─ web/
│  └─ dist/             # 静的ファイル（HTML, JS, CSS, 画像等）
├─ config/
│  └─ providers.json    # プロバイダー設定
└─ README.md            # このファイル
```

## 配置手順

### 1. ZIPを展開

適切なプラットフォーム用のZIPを展開してください。

### 2. バイナリと静的ファイルの配置

以下のいずれかの方法でAgent Managerを起動します：

#### 方法A：自動検出（推奨）

展開したディレクトリで直接起動するだけです。Agent Managerは同じディレクトリの`web/dist`を自動検出して配信します。

```bash
./agent-manager
```

またはWindows:

```cmd
agent-manager.exe
```

この場合、WebブラウザでAgent Manager起動時に表示されるアドレスを開いてください（通常は`http://127.0.0.1:3100/`）。

#### 方法B：明示的に指定

静的ファイルの配置先が異なる場合は`--static-dir`で指定します：

```bash
./agent-manager --static-dir /path/to/web/dist
```

#### 方法C：APIのみ提供

静的ファイルなしでAPIのみを提供する場合は、静的ディレクトリを配置しないでください。

```bash
./agent-manager
```

## 設定

### 一般的なオプション

Agent Managerの起動オプションは以下のとおりです：

```bash
agent-manager [OPTIONS]
```

主なオプション：

- `--host <host>`: バインドするホスト（既定：127.0.0.1）
- `--port <port>`: バインドするポート（既定：3100）
- `--data-dir <dir>`: データベースとランタイムデータの保存先（既定：OS設定ディレクトリ配下の`maatgen`）
- `--auth-token <token>`: 認証トークン（指定なしで自動生成）
- `--allowed-origins <origins>`: CORS許可オリジン（既定：`http://localhost:5173,http://127.0.0.1:5173`）
- `--config <path>`: ツール設定ファイルの相対パス（既定：`config/providers.json`）
- `--static-dir <dir>`: Web UI静的ファイルのディレクトリ

### プロバイダー設定

`config/providers.json`でCodex、Claude Code、GitHub Copilotのモデル一覧と既定モデルを設定します。

```json
{
  "providers": [
    {
      "id": "codex",
      "label": "Codex",
      "models": ["gpt-5.6-sol", "gpt-5.6-terra"],
      "defaultModel": "gpt-5.6-sol"
    },
    {
      "id": "claude",
      "label": "Claude Code",
      "models": ["claude-opus-5", "claude-sonnet-5"]
    },
    {
      "id": "copilot",
      "label": "GitHub Copilot",
      "models": ["auto", "claude-sonnet-4.6"]
    }
  ]
}
```

## 起動確認

Agent Managerが起動すると、ターミナルに以下のようなメッセージが表示されます：

```
2026-08-21 10:00:00.000+00:00 INFO agent manager listening address=127.0.0.1:3100 version=0.1.0 runtime_file=.../.config/maatgen/runtime.json config_file=config/providers.json static_dir=/path/to/web/dist
```

`static_dir`が表示されていることを確認してください。

ブラウザで表示されたアドレスを開き、Maatgen UIが正常に表示されることを確認します。

## ネットワーク設定

### リモートアクセス

デフォルトではローカルホスト（127.0.0.1）のみバインドします。リモートからアクセス可能にする場合は`--host 0.0.0.0`を指定します：

```bash
./agent-manager --host 0.0.0.0
```

### CORSオリジン

Webブラウザからアクセスする場合は`--allowed-origins`を適切に設定してください：

```bash
./agent-manager --allowed-origins "https://example.com,https://app.example.com"
```

## ランタイムメタデータ

Agent Manager起動時、以下の情報がランタイムメタデータファイルに保存されます：

- PID（プロセスID）
- バインドアドレスとポート
- 認証トークン
- バージョン
- スキーマバージョン
- 起動日時

既定では`~/.config/maatgen/runtime.json`に保存されます。別の場所に保存する場合は`--runtime-file`で指定します：

```bash
./agent-manager --runtime-file /var/lib/maatgen/runtime.json
```

このファイルは外部から読み取り可能な状態で保存されるため、認証トークンを含みます。ディレクトリのアクセス権を適切に制限してください。

## トラブルシューティング

### "no static Web UI assets found; serving API only"

静的ファイルディレクトリが見つかりません。以下を確認してください：

1. `web/dist`ディレクトリが存在するか
2. `--static-dir`が正しく指定されているか
3. ビルド時に`npm run build:release`が成功したか

### ポートが既に使用中

別のプロセスがポート3100を使用している場合は`--port`で別のポートを指定してください：

```bash
./agent-manager --port 3101
```

### 認証エラー

認証トークンが一致していません。以下を確認してください：

1. HTTPリクエストに`Authorization: Bearer <token>`ヘッダーが含まれているか
2. トークンが`runtime.json`と一致しているか

## VS Code拡張機能

VS Code拡張機能（maatgen-0.1.0.vsix）をインストールするには：

```bash
code --install-extension maatgen-0.1.0.vsix --force
```

拡張機能は起動時にAgent ManagerのURLと認証トークンを求めます。

## セキュリティに関する注意

- Agent Managerはリポジトリ内で任意のコマンドを実行可能です。信頼できる環境でのみ使用してください
- 認証トークンを安全に保管してください
- リモートアクセスを有効にする場合は、ファイアウォールやVPNで適切に保護してください
- `config/providers.json`にはプロバイダー情報が含まれます。本番環境では適切なアクセス制御を実施してください
