# maatgen

VS CodeおよびWebブラウザからCodex CLIを実行・監視し、変更をレビューするためのCoding Agent Managerです。

現在はPhase 0として、共通ProtocolとAgent Managerの最小HTTP基盤を実装しています。

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

## Agent Manager

```bash
go run ./apps/agent-manager/cmd/agent-manager
```

標準では`127.0.0.1:3100`で起動し、次のhealth endpointを提供します。

```text
GET http://127.0.0.1:3100/api/v1/health
```

空きportをOSに選択させる場合は次のように起動します。

```bash
go run ./apps/agent-manager/cmd/agent-manager --port 0
```

## ドキュメント

- [設計書](./docs/coding-agent-design.md)
- [実装計画](./docs/implementation-plan.md)
