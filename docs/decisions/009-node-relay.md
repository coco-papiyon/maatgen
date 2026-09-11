# ADR-009: 上位/下位ノード間のリレー接続で離れたAgent Managerをプロキシ参照する

- Status: Proposed
- Date: 2026-09-11
- Owners: Agent Manager
- Related: [Coding Agent設計書](../coding-agent-design.md)

## Context

Agent Managerは`docs/coding-agent-design.md`のセキュリティ原則により、原則として`127.0.0.1`のみにbindし、外部インターフェースではlistenしない。この方針は維持したまま、あるユーザーが日常的に操作する端末（以下「上位ノード」）から、ネットワーク的に直接到達しにくい別の端末で動いているAgent Manager（以下「下位ノード」）のSession／Run／Usage／Changesを参照・操作したいという要求が生じた。

上位／下位はあくまで役割であり、OSやプラットフォームには依存しない。例えば「操作は使い慣れたPC上で行うが、実際のAgent CLI実行は別のマシン上で行う」というユースケースが典型だが、実装はOSを問わず同じ仕組みで動く。

Web UIは既にHTTP APIとWebSocketで一つのAgent Managerと通信しており（`packages/api-client/httpAgentApi.ts`、`apps/agent-manager/internal/server`）、ブラウザ向けのAPI・イベント形式はすでに確立している。下位ノードとの接続のためだけに別のプロトコルを新設すると、ブラウザ向けAPIと下位ノード向けAPIの二重管理が生じ、Agent Manager側・Web UI側の双方に将来の追従コストがかかる。

## Decision

### 1. 接続は下位ノードから上位ノードへのアウトバウンドで確立する

下位ノードは到達性が保証されないため、下位ノードが起動時に上位ノードへ**アウトバウンド接続**を行う（リバース接続）。

下位ノードの起動時フラグ例：

```text
--upstream-url ws://<上位ホスト>:3101/api/relay/connect
--node-id linux-dev
--node-name "Linux dev box"
```

接続断・上位ノード未起動時は指数バックオフで再接続を試みる。

**訂正（実装時に発見）**：下位ノードが別マシンで動く前提である以上、上位ノードの`--host 127.0.0.1`をそのまま使い回すことはできない。loopbackは同一マシンからしか到達できないため、Windows上の上位にLinux上の下位が接続することが物理的に不可能になる。

そこで、ブラウザ向けの既存HTTP/WebSocketサーバー（`--host`／`--port`、既定`127.0.0.1:3100`）はそのままloopback限定を維持し、**`/api/relay/connect`専用の、独立した第2のリスナー**を追加する。

```text
--relay-listen 0.0.0.0:3101   # 空文字なら下位ノード受け入れ機能自体を無効化（既定）
```

第2リスナーが公開するのは`/api/relay/connect`（`GET`、WebSocketアップグレード）だけの最小限のmuxであり、ブラウザ向けの`/api/v1/...`・`/ws`・静的ファイル配信は一切ここに載せない。これにより「Agent Managerは原則loopbackのみにbindする」という既存方針は、**ブラウザ向けAPIサーフェス**については変更されない。緩和されるのは、下位ノードからの着信だけを受け付ける単一エンドポイントに限られる。

`--relay-listen`を空のままにする（既定）と、この第2リスナー自体を起動しない。上位ノードとして使う意思がある利用者だけが明示的に指定する。Decision 6の認証未検証と合わせ、`--relay-listen`を指定する利用者は、そのネットワーク経路の安全性（VPN、プライベートネットワーク、ファイアウォールでの送信元制限など）を自分で担保する必要があることをドキュメント・起動時ログの両方で明示する。

### 2. ブラウザ↔Maatgenと下位↔上位のI/Fを共通化する

**新しいAPIプロトコルは作らない。** `/api/relay/connect`で確立したWebSocket接続を`net.Conn`へ変換し（`coder/websocket`の`websocket.NetConn`）、その上に[`hashicorp/yamux`](https://github.com/hashicorp/yamux)でストリーム多重化を1層だけ被せる。`yamux.Session`は`net.Listener`と互換の`Accept()`を持つため、下位ノードは次のように**既存のHTTPハンドラをそのまま**この多重化セッションに流し込む。

```go
// 下位ノード: ローカルloopbackへの通常のhttp.Serveに加えて、
// 同じhandlerをリレーセッションにもServeする。
go http.Serve(relaySession, sameHandlerAsLocalListener)
```

上位ノードは、ブラウザから`/api/nodes/{nodeId}/...`宛のリクエストを受けたとき、当該下位ノードの`yamux.Session`に対して`session.Open()`で新しいストリームを開き、`http.Transport.DialContext`をそのストリームに固定した`httputil.ReverseProxy`でプレフィックス（`/api/nodes/{nodeId}`）を剥がした**元のパスのまま**転送する。下位ノード側から見ると、ローカルloopbackから来たリクエストとリレー経由のリクエストは区別がつかない、同一のハンドラ・同一のパス・同一のペイロード形式で処理される。

WebSocket（`/ws?sessionId=...`）も同様に、上位ノードが新しいストリームを開いて下位ノードの`/ws`エンドポイントへ通常のWebSocketアップグレードを行い、ブラウザ側WebSocketとの間でフレームをそのまま双方向中継する（WS-to-WSリレー）。新しいイベント形式・新しいメッセージ種別は追加しない。

これにより、リレー接続に固有のプロトコルは「WebSocketの確立」と「ノード識別ヘッダー」の2点だけに縮小され、それ以外のAPI・イベント仕様はブラウザ向けと完全に共通のものを使う。

### 3. ノード識別はHTTPヘッダーで行い、新しいメッセージ形式を作らない

下位ノードは`/api/relay/connect`へのWebSocketアップグレード要求に、標準的なHTTPヘッダーとしてノード情報を載せる。

```text
X-Maatgen-Node-Id: node-7k2m9p
X-Maatgen-Node-Name: Linux dev box
X-Maatgen-Node-Token: (任意、今回は未検証)
```

アップグレード完了後は直ちにyamuxのバイトストリームとして扱われ、独自のJSONハンドシェイクやenvelope形式は導入しない。

`X-Maatgen-Node-Id`の値は、後述（Decision 3.1）のとおり上位ノードのUIが発行する場合と、下位ノードの起動フラグで利用者が自由に指定する場合の両方を許可する。上位は未知の`nodeId`が接続してきても拒否せず、新規ノードとしてそのまま登録する。

### 3.1 ノードは上位ノードのWeb UIから追加できる（下位ノードでのCLIフラグ手打ちも引き続き可能）

上位ノードのWeb UIに「ノードを追加」操作を設ける。CLIフラグを手で組み立てなくても、UIから下位ノードの接続用コマンドを発行できるようにする。

1. 利用者が「ノードを追加」を押し、表示名（例：`Linux dev box`）を入力する。
2. 上位は新しい`nodeId`（短いランダムスラグ、例：`node-7k2m9p`）と`nodeToken`（ランダムトークン）を生成し、`POST /api/nodes`でレジストリに`status: pending`として追加する。
3. 上位は、その下位ノードでそのまま実行できる起動コマンドを生成して画面に表示する。

   ```text
   agent-manager --upstream-url ws://<上位ホスト>:3101/api/relay/connect \
     --node-id node-7k2m9p --node-name "Linux dev box" \
     --node-token <生成されたトークン>
   ```

4. 利用者はこのコマンドを下位ノードの端末にコピーして実行する。下位が`node-7k2m9p`として接続してくると、pendingエントリが`connected`へ遷移し、UIへ反映される。

UI経由での追加は**利便性のためのオプション**であり必須ではない。Decision 1の自動登録動作（下位ノードが起動フラグで任意の`node-id`／`node-name`を指定し、事前登録なしにそのまま接続する）は変更しない。上位UIで先に追加していない`nodeId`が接続してきた場合も、`pending`を経由せず直接`connected`として登録する。UI追加は「コマンドを組み立てる手間を省く・トークンを自動生成する」ための導線であり、登録経路を1本化するものではない。

### 4. 上位ノードはノードレジストリのみを持ち、下位ノードのデータを複製しない

新パッケージ`internal/relay`に、下位ノードのエントリ（`nodeId`／`name`／`status`／`token`／接続中なら`*yamux.Session`）をメモリ上のレジストリとして持つ。`status`は`pending`（UIで追加されたがまだ未接続）／`connected`／`disconnected`（過去に接続歴があるが現在切断中）の3値とする。接続確立で`connected`へ、切断で`disconnected`へ遷移させる。上位ノードはSession／Run／Usageなどのデータを自分のSQLiteへ複製・同期しない。あくまでリクエスト都度、下位ノードへ透過的に転送する**プロキシ**であり、上位ノードが単独の情報源にはならない（下位ノードが落ちている間、そのノードの情報は参照できない）。

`GET /api/nodes`は、ローカル自身（`local`固定ID）と、上記3状態いずれかの下位ノード一覧（id／name／status／最終接続時刻）を返す。`POST /api/nodes`はDecision 3.1のUI追加用に`pending`エントリを新規作成し、`nodeId`／`nodeToken`／起動コマンド文字列を返す。`DELETE /api/nodes/{nodeId}`は`pending`または`disconnected`のエントリの履歴を消す（`connected`に対しては`409 node_connected`を返し削除しない）。

### 5. Web UIは単一ノード切替方式とし、複数ノードの一括表示は行わない

下位ノードの情報表示は、**一度に1ノードだけを表示する切替方式**とする。複数ノードのSessionを一つの一覧に集約する方式は今回の対象外とする。

理由：

- 元のユースケースが「使い慣れた端末から、特定のリモート環境（実行専用ノード）を参照・操作したい」という**1対1のアクセス**であり、多数ノードを俯瞰するフリート監視ではない。
- 一括表示にすると、上位ノードが全下位ノードをファンアウト取得・マージするAPIや、一部ノード不調時のフォールバック表示など、実装・UIとも複雑度が増す（Session IDの衝突自体は5.1のとおり問題にならない）。
- 切替方式であれば、[Decision 2](#2-ブラウザmaatgenと下位上位のifを共通化する)のプロキシ機構をそのまま使い、`HttpAgentApi`のbase URLを`/api/nodes/{nodeId}`に差し替えるだけでSession一覧・Chat・Diff・Checkpoint UIなどの既存実装を無改造で再利用できる。
- 一括表示は、この切替方式の上にUI側だけで後から追加できる（プロキシ機構自体の変更は不要）。将来ノード数が増えて俯瞰ニーズが出た場合は別ADRとして再検討する。

#### 5.1 将来の「Sessionだけ全ノード横断表示」に向けた拡張ポイント

将来、Chat／Diff／Usageなどはノードごとのまま、**Session一覧だけ**は全ノード横断で見たいという要望が見込まれている。今回はこれを実装しないが、後から**Decision 1〜4（接続確立・共通I/F・ノード識別・ノードレジストリ）を一切変更せずに**追加できるよう、次を設計上の前提として確定させておく。

- **Session IDは追加の名前空間なしにノード横断で衝突しない**：`session.generateID()`は128bitの暗号論的乱数（`crypto/rand`由来の32文字hex、`session_`prefix付き）であり、衝突確率は無視できる。将来ノードをまたいでSessionを一意に扱う際も、`nodeId`との複合を必須にする必要はない（UIでのキー付けには`${nodeId}:${session.id}`を推奨するが、これは衝突対策ではなく単なる描画・ルーティング上の便宜）。この不変条件（Session IDの生成方式を連番などへ変更しない）を将来にわたって維持する。
- **集約は新しい合成レイヤーとして追加し、下位ノードのSchema／APIには触れない**：全ノード横断一覧は、上位ノードが`GET /api/nodes`で得られる`connected`ノード一覧に対し、既存の`GET /api/nodes/{nodeId}/api/sessions`（Decision 2のプロキシ、下位の`GET /api/sessions`をそのまま中継）を並行にファンアウトし、結果を`createdAt`でマージソートするだけで実現できる。新しいエンドポイント案：`GET /api/sessions/all`。
- **ノード情報の付与は集約結果にだけ行い、`protocol.AgentSession`本体は変更しない**：`packages/protocol`に`AgentSession`を包む新しい型（例：`NodeScopedSession = AgentSession & { nodeId: string; nodeName: string }`）を追加し、これは集約エンドポイントのレスポンス専用とする。下位ノード自身のSession一覧APIや`local`単体表示は今まで通り`AgentSession`のみを返す。
- **一部ノードの失敗を許容する**：ファンアウト中に特定ノードが切断・タイムアウトした場合でも、他ノードの結果は返す（該当ノードは`partial`扱いとしてUIへ「一部ノードから取得できませんでした」を表示する）。1ノードの不調が全体を止めないことを、集約APIの必須要件として先に決めておく。
- **一覧からSessionを選ぶと、Decision 5の単一ノード切替（ノードセレクタ＋`HttpAgentApi`のbase URL切替）へ委譲する**：集約ビューはあくまで「どのノードのどのSessionを見るか」を選ぶランチャーであり、Chat／Diff／Usageを複数ノード分同時に描画する独自UIは持たない。これにより集約ビュー追加時もChat以下の既存実装は無改造のままでよい。
- **リアルタイム更新は最初はポーリングでよい**：`GET /api/nodes`と同じ間隔で`GET /api/sessions/all`を再取得すれば足り、複数下位ノードのイベントを1本のWebSocketへ統合する仕組み（EventBroker側の変更）は、実装時点で本当に必要になってから追加する。

上記5点を守る限り、集約Session一覧は将来「UIとAPIの追加だけ」で実現でき、今回のリレー機構本体（下位→上位接続、yamux多重化、共通I/F、ノードレジストリ）の再設計は不要になる。

#### 画面設計

ヘッダーに現在の操作対象ノードを示すセレクタを追加する。既存のAgent／Model選択と並ぶ位置に置く。

```text
┌─────────────────────────────────────────────┐
│ Coding Agent      Node: [● Local ▼]         │
│                                               │
│ Agent: [Codex ▼]   Model: [Default ▼]       │
│ ...(既存のMain Chat/Session Historyがそのまま)│
└─────────────────────────────────────────────┘
```

セレクタを開いた状態：

```text
Node: [● Local ▼]
┌───────────────────────────────┐
│ ● Local                       │  ← 上位ノード自身（既定選択）
│ ● linux-dev                   │  ← 接続中の下位ノード
│ ○ build-box (切断中)          │  ← 過去に接続歴があるが現在未接続
│ ◐ node-7k2m9p (登録待ち)      │  ← UIで追加したがまだ未接続
│ ─────────────────────────    │
│ ＋ ノードを追加                │
└───────────────────────────────┘
```

- `●`＝接続中（緑）、`○`＝切断中（グレー）、`◐`＝登録待ち（黄、Decision 3.1でUIから追加した直後の状態）。`GET /api/nodes`のレスポンスをポーリングまたはSSE/WSで反映する。
- 「＋ ノードを追加」はセレクタの最下部に常設し、押すとDecision 3.1の追加ダイアログを開く。
- 切断中ノードも選択自体は可能だが、Session一覧やChatは取得できないため「このノードは現在オフラインです」という空状態を表示し、Prompt送信・Run開始は無効化する。
- ノードを切り替えると、中央のMain Chat／Session History／Usage／Changesタブはすべて選択中ノードのデータへ入れ替わる（既存のSession切替と同じ扱いで、React/Vueの状態は選択ノードIDをキーに保持する）。
- 選択中に対象ノードが切断された場合（下位ノードのプロセス終了・ネットワーク断）は、実行中のRunがあればイベントストリームが途切れた旨をChatにシステムメッセージとして表示し、セレクタのアイコンを切断中表示へ切り替える。再接続を検知したら自動的に最新状態を再取得する。
- ノード選択状態はURLクエリ（例：`?node=linux-dev`）に反映し、リロードや共有リンクでも同じノードを指す画面を開けるようにする。
- VS Code版はノードセレクタを持たない（Extensionは自分のAgent Managerのみを対象とする、[Scope and non-goals](#scope-and-non-goals)参照）。

#### 既存UI（`apps/web/src/App.vue`）への具体的な組み込み

現行実装は`header.topbar`（brand＋`topbar-status`）、`aside.sidebar`（Session一覧＋新規Session作成フォーム）、`main.conversation`（`conversation-header`＋Chat）という構成である。ノードセレクタはこの構造に対して次のように組み込む。

**topbar**：`brand`と`topbar-status`の間にノードセレクタを配置する。`topbar-status`内の`stream-state`（イベントストリーム接続状態を示す既存の丸インジケータ）は、選択中ノードが下位ノードの場合に限り、ローカルWS再接続だけでなくリレー接続の断（下位ノードの切断）も表す状態として拡張する（ラベル例：「ノード切断中」）。新しい表示要素を増やすのではなく、既存の`stream-state`が指す対象を「選択中ノードまでの経路」に一般化する。

**sidebar / 新規Session作成フォーム**：`Workspace path`はノードのファイルシステム上のパスであり、Windows機とLinux機では書式が異なる（`C:/path/to/workspace` と `/home/user/project`）。そのため次の状態はすべて**選択中ノードIDをキーにして分離**する。

- `workspaceHistory`（Workspace pathの入力履歴、localStorage）
- `newSessionProvider`（最後に選んだProvider）
- `providers`一覧（下位ノードにインストール済みのCLIが上位と異なる場合があるため、選択中ノードの`GET /api/providers`相当を都度取得する。プロキシ経由のため追加APIは不要）

ノードを切り替えた直後は、新規Session作成フォームがそのノード用の履歴・Provider一覧へ入れ替わったことが視覚的にわかるよう、フォームを一瞬フェード/クリアしてから再入力する（別ノードの履歴が混在して見えることを防ぐ）。

**conversation-header**：現在`eyebrow`に表示している「DIRECT REPOSITORY SESSION」「LIMITED DIRECTORY SESSION」の末尾に、選択中ノードがLocal以外のときだけノード名を追記する。

```text
DIRECT REPOSITORY SESSION · linux-dev
/home/dev/project
```

AGENTS.mdの原則どおりAgentは対象Working Treeを直接変更するため、「今どのマシンの実ファイルを変更しているか」をChat入力欄の直前で常に視認できることを優先する（Chatへの一時的なシステムメッセージではなく、常時表示の`eyebrow`／`path`に統合する）。

**session-item（Session一覧の各行）**：ノードバッジは付けない。単一ノード切替方式（Decision 5）により、一覧は常に選択中ノードのSessionのみで構成されるため、行ごとの出所表示は不要。

#### ノードの追加（Decision 3.1のUI）

セレクタ最下部の「＋ ノードを追加」を押すと、次のダイアログを表示する。

```text
┌─── ノードを追加 ─────────────────────────────┐
│ 表示名                                        │
│ [ Linux dev box                            ]  │
│                                                │
│               [ キャンセル ]  [ 追加 ]        │
└────────────────────────────────────────────┘
```

「追加」を押すと`POST /api/nodes`が呼ばれ、`nodeId`／`nodeToken`が生成されてpending状態のノードが作成される。続けて起動コマンドを表示するダイアログへ遷移する。

```text
┌─── linux-dev の接続コマンド ─────────────────────────────┐
│ 下記コマンドをこのノードの端末で実行してください。            │
│                                                          │
│ ┌──────────────────────────────────────────┐ [コピー]   │
│ │ agent-manager --upstream-url ws://...     │            │
│ │   --node-id node-7k2m9p --node-name ...   │            │
│ │   --node-token ●●●●●●●●●●●●●●●●●●●●     │            │
│ └──────────────────────────────────────────┘            │
│                                                          │
│ 接続を待っています…（自動的に閉じます）        [閉じる]   │
└──────────────────────────────────────────────────────┘
```

- 「コピー」ボタンは既存のRun結果コピー機能（`apps/web/src/App.vue`の`copyEventText`、`navigator.clipboard.writeText`＋コピー直後に1.5秒だけラベルを切り替える実装）と同じパターンを再利用する。新しいコピーUIコンポーネントは作らない。
- ダイアログは開いたまま`GET /api/nodes`をポーリングし、対象`nodeId`が`connected`に遷移したら自動的に閉じてセレクタの選択をそのノードへ切り替える。利用者が先にダイアログを閉じても、pendingエントリと発行済みトークンはレジストリに残り、後から同じコマンドで接続できる。
- トークンは初回表示時のみ平文で見せ、ダイアログを閉じた後は再表示できない（レジストリには保持するが、UIから再度取得するAPIは提供しない）。コマンドをコピーし忘れた場合は一度ノードを削除して追加し直す。

#### ノードの削除

接続履歴だけが残り実体が二度と接続してこない「切断中」ノード、および接続コマンドを控え忘れた「登録待ち」ノードがセレクタに残り続けると一覧が肥大化するため、`pending`／`disconnected`状態の行にのみ「履歴から削除」操作を用意する。これは`DELETE /api/nodes/{nodeId}`で上位ノードのメモリ上レジストリからエントリを消すだけであり、下位ノード側には一切影響しない（同じnode-idを使ったコマンドを新たに発行し直せば再登録できる）。`connected`状態のノードには削除操作を出さない。

### 6. 認証は今回スキップし、将来有効化できる形だけ残す

`X-Maatgen-Node-Token`ヘッダーのスロットだけ用意し、上位ノード側での検証は行わない（下位→上位間のネットワーク経路自体の安全性が別途担保されている前提）。将来トークン検証を有効化する際も、ヘッダーを見るだけで済み、プロトコル変更は不要。

## Scope and non-goals

対象は、下位ノードから上位ノードへのアウトバウンド接続確立、yamuxによるストリーム多重化、既存HTTP/WebSocket APIのプロキシ転送、ノードレジストリ、Web UIのノードセレクタ、Web UIからのノード追加（`POST /api/nodes`によるpendingノード生成と起動コマンド表示）とする。

次は対象外とする。

- 発行したノードトークンの実際の検証（生成・表示はDecision 3.1で行うが、上位ノード側での照合は行わない）。
- 上位ノードによる下位ノードのデータの永続化・キャッシュ・オフライン時の参照。
- 複数の上位ノードを跨いだノードの多段リレー（下位→上位→さらに上位）。
- 下位ノード側でのUIの無効化・制限（下位ノードは従来どおり自分自身のローカルWeb UIとしても動作し続けてよい）。
- 下位ノードが複数の上位ノードへ同時接続する構成。
- 複数下位ノードのSessionを一つの一覧へ集約表示する機能そのものの実装（単一ノード切替方式を採用。ただし将来この集約を追加する際にDecision 1〜4の再設計が不要になるよう、拡張ポイントを[Decision 5.1](#51-将来のsessionだけ全ノード横断表示に向けた拡張ポイント)として確定させてある）。
- VS Code拡張へのノードセレクタ追加（対象はWeb版のみ。VS Code拡張は引き続き自分自身のAgent Managerだけを扱う）。

## Consequences

### Positive

- ブラウザ向けAPIとノード間リレーのAPIが完全に同一であるため、`server.go`のハンドラやProtocol型を二重に持つ必要がなく、将来のAPI追加・変更がリレー経由でも自動的に有効になる。
- 上位ノード・下位ノードとも新しいTCPポートを開かず、既存のセキュリティ方針（loopback限定）を変更しない。
- 上位ノードがデータを複製しないため、同期不整合（上位と下位でSession状態がずれる）を構造的に回避できる。

### Negative

- 新しい依存ライブラリ（`hashicorp/yamux`）が増える。
- 上位ノードは常にリクエストのたびに下位ノードへ往復するため、下位ノードが切断中または高レイテンシの経路にある場合、リモートノード参照時の体感速度が直接接続より劣化する。
- 認証を後回しにしているため、`/api/relay/connect`が到達可能なネットワークにいる第三者が任意のnode-idを騙って接続できてしまう。現段階ではネットワーク経路の安全性に依存しており、経路が信頼できない環境では利用できない。
- `--relay-listen`を指定すると、上位ノードのその1エンドポイントだけは非loopbackで着信を受け付けるため、コード上は新規リスナー・新規ポートが増える（既存のブラウザ向けAPIサーフェスは無影響）。オプトイン（既定は無効）であることと、経路の安全性が利用者の責任である点をログとドキュメントの両方で明示する。

## Implementation notes

実装時は次の順序で進める。

1. `apps/agent-manager/go.mod`へ`hashicorp/yamux`を追加する。
2. `internal/relay`パッケージ（下位用`RunClient`：`--upstream-url`／`--node-id`／`--node-name`／`--node-token`のヘッダー付きダイヤル、`websocket.NetConn`＋`yamux.Client`、再接続バックオフループ、既存ハンドラをそのまま`http.Serve(session, handler)`する／上位用`ConnectHandler`：`yamux.Server`＋ノードレジストリへの登録／`Registry`：pending・connected・disconnectedのノード管理／`NewReverseProxy`：`httputil.ReverseProxy`＋ノード固有`DialContext`。`ReverseProxy`はUpgradeリクエストを自動でトンネルするため、`/ws`用の別実装は不要）を追加する。
3. `cmd/agent-manager/main.go`に、下位ノードとして動く`--upstream-url`等のフラグと`relay.RunClient`の起動、上位ノードとして動く`--relay-listen`フラグと`/api/relay/connect`専用の第2`net.Listener`／`http.Server`を追加する。
4. `internal/server`に`RelayController`インターフェースと`registerRelayRoutes`（`GET /api/nodes`、`POST /api/nodes`、`DELETE /api/nodes/{nodeId}`、`/api/nodes/{nodeId}/`以下のリバースプロキシ）を追加し、`New()`から呼び出す。
5. Web UIに、`GET /api/nodes`をポーリングしてノード一覧・接続状態（`connected`／`disconnected`／`pending`）を表示するヘッダーのノードセレクタ、選択ノードIDに連動した`HttpAgentApi`のbase URL切り替え、URLクエリ（`?node=`）との同期、切断中ノード選択時の空状態表示を追加する。
6. Web UIに「ノードを追加」ダイアログ（表示名入力→`POST /api/nodes`→起動コマンド表示、既存の`copyEventText`と同じコピーボタンパターンの流用、`connected`遷移時の自動クローズ）を追加する。
8. 下位ノード未接続／切断時のエラー応答（`404 node_not_found`等）と再接続時のレジストリ復帰をテストする。
