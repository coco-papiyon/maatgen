# ADR-006: コマンド承認を設定・AI診断・利用者確認の三段階で行う

- Status: Implemented
- Date: 2026-08-16
- Owners: Agent Manager / Agent Adapter / Web / VS Code Extension
- Related: [Coding Agent設計書](../coding-agent-design.md)、[実装計画](../implementation-plan.md)

## Context

現在のCodex Adapterは`--ask-for-approval never`、Copilot Adapterは`--allow-all --no-ask-user`を指定し、Agent CLI内のコマンド承認待ちを発生させていない。Agent ManagerはCommand Eventを記録するが、コマンド実行前の可否判定には関与していない。

Working Treeの変更についてはRun前後のCheckpointとRestoreで回復可能にしているが、外部通信、Workspace外への書き込み、プロセス操作、資格情報の利用などはCheckpointだけでは回復できない。このため、Agentが要求したコマンドを実行前に評価し、設定、AI診断、利用者確認の順に承認する仕組みが必要である。

このADRの「承認」はコマンド実行の承認を指す。Agentによるソース変更をAccept／Rejectする操作は再導入せず、変更の取り消しには既存のCheckpoint／Restoreを使用する。

## Decision

Agent Managerに`Approval Coordinator`を置き、Agent Adapterが受け取った実行前の承認要求を非同期の状態機械として処理する。HTTP request、WebSocket配信、Providerのread loopを承認待ちでブロックしない。判定順序は次のとおりとする。

```text
Agentがコマンド実行を要求
  │
  ▼
1. 設定済み許可ルール
  │ 全segmentが一致 → 承認
  │ 一つでも不一致
  ▼
2. AI Diagnostic Reviewer
  │ risk <= autoApproveMaxRisk → 承認
  │ 超過・不明・失敗
  ▼
3. 利用者確認
  ├─ 1回だけ許可
  ├─ Session内で許可
  ├─ 永続的に許可
  └─ 不許可
```

Approval CoordinatorはAgent Manager内のコンポーネントとし、独立した常駐OSプロセスにはしない。AI診断だけは、承認待ちのメインAgentとデッドロックしないよう、同じProviderの軽量モデルを使う短命で隔離された別の推論実行とする。

初期実装はCodexを対象とする。Provider共通の承認契約は定義するが、Copilot固有の実装はCodexの承認フローが完成するまで追加しない。

## 非同期実行モデル

実装は`C:\data\dev\github\korocon\internal\daemon\daemon.go`と`C:\data\dev\github\korocon\docs\design.md`のresident job／承認処理を参考にする。特に次の原則を採用する。

- API入力とAgent turn実行を別goroutineにする。
- Provider sessionのturnは単一workerが順番に実行し、同じSessionに複数のactive turnを作らない。
- Providerからのserver requestは専用read loopで受信し、承認decisionをchannel経由で待つ。
- 複数の手動承認要求は到着順に直列化する。
- Process全体のContext、RunごとのContext、approval requestごとのContextを分け、Cancelを下位へ伝播する。
- 終了時はWaitGroupでworkerとProvider I/O goroutineの終了を待つ。

Maatgenでは既存の`StartRun`動作を維持する。`POST /api/v1/sessions/{id}/messages`はRunを`queued`で永続化し、workerへ投入した時点で`202 Accepted`とRunを返す。before Checkpoint作成、Provider起動、AI turn、承認、after Checkpoint作成はHTTP request lifecycleから切り離して実行する。

```text
HTTP / WebSocket goroutine
  ├─ StartRun → Runを永続化 → 202 Accepted
  ├─ approval decision API
  └─ Event配信

Session Run worker（Sessionごとに最大1）
  └─ Run state machine
       queued → starting → running
                             │
                             ├─ waiting_for_approval
                             │        │ decision
                             │        └──────────→ running
                             └─ completed / failed / cancelled

Provider connection
  ├─ read pump  ── notification／request → Adapter
  └─ write pump ← response／turn request

Approval Coordinator
  ├─ rule matcher
  ├─ diagnostic worker ── separate lightweight Agent
  └─ human decision channel
```

### Run worker

初期実装では同じSessionへのRun queueを導入せず、active Runがある間の追加Runは従来どおり`409 Conflict`とする。異なるSessionのRunは、それぞれ独立したworker goroutineで並行実行できる。将来queueを導入する場合も、同じSession内はFIFOかつ同時実行数1とする。

Run workerはRun状態の唯一の書き手とする。Approval CoordinatorやAdapterは状態を直接更新せず、typed eventをworkerへ返す。これにより`running`、`waiting_for_approval`、terminal stateの競合更新を防ぐ。

### Provider read／write pump

双方向Provider接続は、stdoutを読んでその場でhandlerを完了させる逐次実装にしない。

- read pumpはJSON-RPC／JSONLを継続的に読み、response、notification、server requestへ振り分ける。
- server requestにはProvider request IDを付け、pending request mapへ登録する。
- command approval requestはApproval Coordinatorへ送った後、read pumpは次のmessageの受信を継続する。
- decision受信後、write pumpへresponseを送る。stdout reader goroutineから直接stdinへ書かない。
- write pumpはProviderへの全書込を直列化し、JSON frameの混在を防ぐ。
- process終了時はpending requestをすべてerrorで完了させる。

Providerが1 turn中に複数のapproval requestを送る可能性を考慮し、pending request mapは複数件を保持できる。ただし人へ表示する要求はSession単位のFIFO approval queueで一件ずつactiveにする。設定またはAIで自動承認できる要求は、人のqueueを待たず個別に完了できる。

### 非同期AI診断

AI Diagnostic ReviewerをメインRun worker上で同期実行しない。Approval Coordinatorは診断jobを専用worker poolへ投入し、結果をchannelで受け取る。メインAgentのProvider connectionは承認response待ちのまま維持されるが、ManagerのAPI、Event配信、他SessionのRunは動作を続ける。

診断worker poolの既定同時実行数は1、queue上限は設定可能な小さい値とする。queue満杯、診断timeout、Manager終了時はAI判定不能として人の確認へ進む。診断AgentはメインAgentのworker、Session、Thread、approval queueを共有しない。

### 人の応答と再接続

human approvalはメモリ内channelだけに依存させない。承認要求をSQLiteへ`pending`で保存してからEventを配信する。Web／VS Codeが切断してもRunとProvider processは待機を続け、再接続時にpending要求をAPIから復元できる。

decision APIはSQLite上のpending状態を条件付き更新し、更新に成功した一つの応答だけをRun workerのdecision channelへ送る。応答Eventの送信に失敗してもpollingで未配送decisionを回収できるようにする。同じ要求への二重応答は`409 Conflict`とする。

Manager processの再起動後は、以前のProvider processへresponseを返せない。このため起動時recoveryでは、非terminal Runを`failed`、pending approvalを`expired`としてafter snapshotの取得を試みる。保存済みpending要求だけから新しいProvider processでコマンドを実行してはならない。

### Cancelとshutdown

Run Cancelは実行中、AI診断中、人の承認待ちのいずれでも受け付ける。Run Contextのcancelにより次を順に行う。

1. pending diagnostic jobをcancelする。
2. pending human approvalを`cancelled`へ更新する。
3. Providerへ拒否またはcancel requestを送れる場合は送る。
4. 猶予時間後も終了しなければProvider process treeを停止する。
5. terminal stateとafter Checkpointを保存する。

Manager shutdownでは新規Run受付を停止し、active Runをcancelして、設定時間までWaitGroupを待つ。timeout後もAPI handlerを待たせ続けない。

## 1. ProviderとAdapterの契約

承認はコマンド実行前に停止できなければ意味がない。AdapterはProviderから承認要求を受け取り、Managerの応答をProviderへ返せることを必須条件とする。

```go
type CommandApprovalRequest struct {
    ID          string
    SessionID   string
    RunID       string
    Command     string
    Shell       string
    WorkingDir  string
    Explanation string
}

type CommandApprovalDecision struct {
    Approved bool
    Reason   string
}
```

Adapterのapproval callbackはProvider read loop上で長時間blockさせず、requestを登録してfuture相当のresponse channelを返す形とする。Approval Coordinatorはそのchannelを非同期に完了させる。

現在のように承認を無効化した一方向JSONL実行は、この契約を満たさない。Codexでは実行前のapproval request／responseを扱えるCLI提供のプロトコルをAdapter内で使用する。利用可能なCodex CLIバージョンで双方向承認が扱えない場合、PTYの画面文字列解析で代替せず、そのRunを開始不可として明示的に報告する。

Providerがすでにコマンドを実行した後の`command_started` Eventは監査情報であり、承認要求として扱わない。承認応答前に実行されたコマンドはProvider契約違反としてRunを停止する。

## 2. 設定による許可

設定ファイルはProvider設定とは独立した`commandApproval`セクションを持つ。

```json
{
  "commandApproval": {
    "enabled": true,
    "autoApproveMaxRisk": "low",
    "humanResponseTimeoutSeconds": 600,
    "reviewer": {
      "provider": "same-as-run",
      "modelByProvider": {
        "codex": "<configured-lightweight-model>"
      },
      "timeoutSeconds": 15
    },
    "allowedCommands": [
      { "argv": ["git", "status", "*"] },
      { "argv": ["go", "test", "*"] },
      { "argv": ["npm", "test", "*"] }
    ]
  }
}
```

モデル名や料金はコードへ固定しない。`modelByProvider`はProviderのモデル一覧に存在するモデルだけを受け付ける。未設定時はProvider設定で`diagnosticModel`として指定された軽量モデルを使用し、それもなければAI診断をスキップして利用者確認へ進む。

### 2.1 コマンドの分解

コマンドを正規表現や単純な文字列splitでは分解しない。承認要求に記録されたShellに対応するParserを使用し、少なくとも次の制御演算子で実行単位のsegmentへ分解する。

- pipeline: `|`、`|&`
- conditional: `||`、`&&`
- sequence/background: `;`、改行、`&`
- grouping: `(...)`、PowerShellのscript block
- nested execution: `$()`、バッククォート、process substitution
- redirection先にコマンド評価が含まれるShell構文

引用符内やエスケープ済みの区切り文字は分割しない。Shellを特定できない、構文解析に失敗する、実行ファイル名が変数展開・alias・関数・`eval`／`Invoke-Expression`などで静的に確定できない場合は、設定による承認を行わない。

例：

```text
git status && npm test | tee result.txt
```

は概念上、`git status`、`npm test`、`tee result.txt`の3 segmentとして評価する。3つすべてが許可ルールに一致した場合だけ、元のコマンド全体を承認する。一つでも不一致なら、設定段階ではコマンド全体を承認しない。

### 2.2 許可ルールの照合

許可ルールは未加工のコマンド文字列ではなく、Parserが得たargvに対して照合する。

- argv要素は完全一致を基本とする。
- `*`は0個以上のargv要素に一致する。文字列途中のglobや正規表現としては扱わない。
- Windowsの実行ファイル拡張子とパス区切りは正規化するが、引数値の大文字小文字は変更しない。
- 実行ファイルがパス指定の場合は解決後の実体も記録し、同名別バイナリへの置換を監査できるようにする。
- Shell builtin、PowerShell cmdlet、script fileは種別を保持して照合する。
- ルール自体に環境変数展開やcommand substitutionを許可しない。

`["git", "*"]`のような広いルールは作成可能だが、永続保存時にUIで警告する。`git status`の許可から`git reset`を推論して許可することはない。

## 3. AIによる許可

設定で全segmentを承認できなかった場合、コマンド全体と解析済みsegmentを`AI Diagnostic Reviewer`へ渡す。

### 3.1 実行方式

- Providerは通常のRunと同じProviderを使用する。
- モデルは設定された軽量モデルを使用する。
- メインAgentとは別の短命な推論実行とし、会話Threadを共有しない。
- Reviewer自身にはWorkspace書き込み、コマンド実行、Tool Callを許可しない。Providerがinference-only実行を提供しない場合はread-only sandboxとapproval neverを使い、Reviewerの出力以外を破棄する。
- Reviewerから発生した承認要求を再帰的に審査しない。発生した場合はReviewer失敗として利用者確認へ進む。
- Prompt injectionを避けるため、Agentの説明文やコマンドは明示的なデータ領域として渡し、命令として連結しない。

Reviewerへ渡す情報は、redact後のコマンド、argv segment、cwdがWorkspace内か、redirect先、Runの目的の短い要約とする。環境変数の値、Secret、ファイル内容、過去の会話全文は渡さない。

### 3.2 危険度

危険度は4段階とする。

| Level | ID | 例 |
|---|---|---|
| 0 | `safe` | Workspace内の読取、version確認、静的解析 |
| 1 | `low` | Workspace内のbuild／test、復元可能な生成物やソース変更 |
| 2 | `high` | 外部通信、package install、Workspace外書込、プロセス制御、広い削除 |
| 3 | `critical` | 資格情報送信、権限昇格、永続的なシステム変更、回復困難な削除 |

Reviewerは次のJSONだけを返す。

```json
{
  "risk": "high",
  "confidence": 0.91,
  "summary": "外部registryからpackageを取得する",
  "factors": ["network", "dependency-install"],
  "segmentRisks": [
    { "index": 0, "risk": "high", "reason": "network access and package installation" }
  ]
}
```

全segmentの最大riskが`autoApproveMaxRisk`以下で、構造化出力がSchemaに適合し、`confidence`が設定値（既定0.8）以上の場合だけ承認する。タイムアウト、Provider error、不正JSON、未知のrisk、低confidenceは不承認ではなく「AI判定不能」として利用者確認へ送る。AIだけを根拠に`critical`を自動承認できる設定は許可しない。

AIの判定は監査ログへ保存するが、同一文字列の将来のコマンドを自動的に許可するルールには変換しない。

## 4. 人による許可

設定およびAIで承認されなかった場合、Runを`waiting_for_approval`状態にしてWebとVS Codeへ承認要求を配信する。両画面の選択肢と意味を一致させる。

表示内容：

- 元のコマンドと解析済みsegment
- cwd、Shell、要求元Agent、Session／Run
- 設定に一致しなかったsegment
- AIのrisk、summary、factors、判定不能理由
- redirect、外部通信、Workspace外パスなどの検出情報

選択肢：

| 選択 | 動作 |
|---|---|
| 許可（1回） | このapproval request IDとcommand digestだけを許可する |
| 許可（Session内） | 選択した許可ルールを現在のSessionのメモリに追加して許可する |
| 許可（永続） | 選択した許可ルールを設定ファイルへ保存して許可する |
| 不許可 | Providerへ拒否を返し、Agentに代替手段の検討を許す |

一定時間応答がない場合は拒否する。RunのCancel、SessionのClose、Managerの終了でも保留中の要求を拒否する。同じRunに複数の承認要求がある場合は要求順に処理し、UIには現在処理可能な要求を明示する。

### 4.1 コマンドの一部を許可ルールへ追加

利用者はParserが生成したsegmentを選び、次のいずれかとしてSessionまたは永続ルールへ追加できる。

- 完全一致: `go test ./internal/run`
- 引数prefixと残りのwildcard: `go test *`
- 実行ファイル以下をすべて許可: `go *`（強い警告を表示）

自由入力も可能にするが、raw substringではなくargv rule editorとして入力させ、保存前に再parseする。どのsegmentに一致するかをpreviewし、現在のコマンドに一致しないルールはこの承認操作から保存できない。複合コマンド全体を一つの文字列patternとして保存しない。

永続保存はManagerが設定Schemaを検証した後、一時ファイルとatomic renameで行う。Secretらしい値を含む引数は`*`へ置換するまで永続保存できない。保存に失敗した場合は永続許可として扱わず、利用者へ再選択を求める。

## 5. TOCTOUと実行の結び付け

承認対象は表示文字列だけでなく、次を正規化して作成したdigestへ結び付ける。

- command bytes
- ShellとShell version
- cwd
- 解析済みargvとredirect
- 解決できた実行ファイルpath
- Session ID、Run ID、approval request ID

Providerへ承認を返した後にコマンド、cwd、実行ファイルが変わった場合は承認を無効にして再審査する。Session許可・永続許可はルールとして再評価し、以前のdecision digestを使い回さない。

## 6. Protocol、API、永続化

共通Protocolへ次を追加する。

- Run status: `waiting_for_approval`
- Event: `command_approval_requested`
- Event: `command_approval_decided`
- Event data: command、segments、risk、decision、scope、source、redacted rationale

Agent Manager APIは少なくとも次を提供する。

```text
GET  /api/v1/sessions/{sessionId}/approvals?status=pending
POST /api/v1/sessions/{sessionId}/approvals/{approvalId}/decision
```

decision APIはCSRF相当の接続token、Session／Run整合性、pending状態、command digestを検証する。同一requestへの二重応答は`409 Conflict`とする。

SQLiteにはapproval request、AI assessment、最終decision、decision source、scope、作成・応答時刻、redact済みcommand、digestを保存する。永続ルールの正本は設定ファイルとし、SQLiteは監査履歴だけを持つ。Secretや未加工の環境変数値は保存しない。

## 7. Failure policy

承認系はfail closedとする。

- Parser failure → 設定承認しない
- AI failure／timeout → 利用者確認
- UI未接続／利用者timeout → 拒否
- 永続設定の保存失敗 → 永続許可しない
- Providerへdecisionを返せない → Run失敗
- 承認前に実行を検出 → Run停止、重大な診断Eventを記録

AI診断機能を無効にした場合も、設定で許可されないコマンドは利用者確認へ進む。承認機能全体を無効にする設定は開発用途に限定し、起動時とUIへ明示する。

## 8. Security and privacy

- 許可ルールは最小権限を推奨し、広いwildcardには警告を出す。
- AI診断の結果は助言であり、設定済みの禁止ルールを上書きしない。将来deny ruleを追加した場合はallow ruleやAI判定より先に評価する。
- AIへ送る前とログ保存前に既存のSecret Maskingを適用する。
- コマンド出力は承認判定へ送信しない。
- symlink／junctionを含むWorkspace内外判定は実pathで行う。
- 承認UIはHTMLとしてcommandを解釈せずtextとして表示する。
- Reviewerの利用量、モデル、costを通常Runと区別して記録する。

## 9. Consequences

### Positive

- 頻出する安全なコマンドは待ち時間とAI costなしで実行できる。
- 未登録コマンドも軽量モデルで一次判定できる。
- 高riskまたは不確実な操作だけを利用者へ確認できる。
- 一時・Session・永続の許可範囲を利用者が選べる。
- すべての判断根拠を監査できる。

### Negative

- 現在の一方向CLI JSONL実行から、双方向の承認要求を扱える実行方式へ変更が必要になる。
- Shellごとの正確なParserと、nested commandを含む安全な照合が必要になる。
- AI診断の追加latencyとcostが発生する。
- WebとVS Codeの両方に承認待ちUIと再接続処理が必要になる。
- Provider接続にread pump、write pump、pending request map、Session workerの並行制御が必要になる。
- 軽量モデルの判定は完全ではないため、人の確認とfail-closed運用が不可欠になる。

## 10. Alternatives considered

### 常に利用者へ確認する

安全だが、build／testなど頻出コマンドでもRunが停止し、非同期利用が難しいため採用しない。

### AIだけで承認する

Prompt injection、誤判定、Provider障害時の扱いが不十分で、永続的な利用者方針も表現できないため採用しない。

### CLIの`allow-all`／approval neverを継続する

Checkpointで戻せない外部副作用を実行前に制御できないため採用しない。

### raw文字列のglob／正規表現だけで判定する

quote、nested command、pipeline、Shell展開による回避が容易なため採用しない。

### 承認専用の常駐サービスを別プロセスにする

初期段階では運用、認証、障害点が増える。Approval CoordinatorはManager内に置き、AI診断だけを隔離した短命実行にする。

## 11. Implementation order

1. Koroconのapp-server Session、read/write処理、server request handlerを参照し、Codexの双方向approval transportをSpikeする。
2. 非同期Provider connection（read pump、write pump、pending request map）とSession Run workerを実装する。
3. Protocol、Run status、SQLite、Approval Coordinatorを追加する。
4. Shell Parserとargv allow rule matcherを実装し、bypass testを追加する。
5. WebとVS Codeへ人による承認UIとpending復元を追加する。
6. 非同期AI Diagnostic Reviewer workerとrisk Schemaを追加する。
7. Session ruleと永続設定editorを追加する。
8. timeout、cancel、再接続、Manager再起動、二重応答、TOCTOUの統合テストを追加する。
9. Codexの承認フロー完成後に、他Providerのtransport対応可否を個別ADRまたは追補で決定する。

## 12. Required tests

- quote内の`|`、`&&`、`;`を誤分割しない
- pipeline、conditional、sequenceの一部だけが許可された場合に全体を承認しない
- `$()`、バッククォート、PowerShell script block内のnested commandを検出する
- wildcardがargv境界を越えて意図せず一致しない
- parse不能、AI timeout、不正JSON、低confidenceが人の確認へ進む
- `critical`をAIが自動承認しない
- 1回、Session、永続のscopeが混ざらない
- command digest変更後に以前のdecisionを再利用しない
- UI再接続後にpending requestを復元する
- HTTPのRun開始がAgent完了や承認待ちを待たず`202 Accepted`で返る
- 別SessionのRunとAPI／Event配信が一つの承認待ちで停止しない
- Provider read pumpが承認decision待ち中もnotificationを処理できる
- 複数の手動承認要求を到着順に表示し、request IDを取り違えない
- diagnostic workerのqueue満杯やcancelでgoroutine leakを起こさない
- Cancel／timeout／Manager終了時にpending requestを拒否する
- Manager再起動時にorphan Runとpending approvalをfail／expireし、コマンドを再実行しない
- 承認前にProviderがコマンドを実行しない
- 永続設定保存失敗時にコマンドを許可しない

## 13. Review triggers

次の場合にこのADRを見直す。

- Provider CLIのapproval protocolまたはsandbox仕様が変更された
- Shell Parserで安全に扱えない新しいShellをサポートする
- AI risk判定のfalse positive／false negativeが許容範囲を超えた
- Agent Managerをlocalhost以外へ公開する
- 組織単位の集中policy、署名済みpolicy、deny ruleが必要になった

## 14. Implementation note

2026-08-16にCodex向けの初期実装を完了した。`commandApproval`設定、argvルール照合、短命なCodex Diagnostic Reviewer、SQLite上のpending approval、decision API、Web／VS Codeの確認UI、Codex App Serverの双方向request／responseを実装した。CopilotはこのADRの方針どおり従来transportのままとし、Codex運用後に別途対応可否を判断する。

初期実装のshell parserは、安全に静的評価できるquoteと制御演算子だけを許可する保守的なparserである。subshell、script block、redirectionなどの動的構文は設定承認せず、AIまたは利用者確認へ送る。実行ファイルの実体digestとshell別AST parserは、Providerが承認対象のdigestを返せるようになった時点で強化する。
