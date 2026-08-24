# ADR-007: ローカルリポジトリを起点にGitHubを監視し、条件一致時にAgent Runを起動する

- Status: Proposed
- Date: 2026-08-23
- Owners: Agent Manager / Web / VS Code Extension
- Related: [Coding Agent設計書](../coding-agent-design.md)、[実装計画](../implementation-plan.md)、[ADR-006](./006-command-approval.md)

## Context

Maatgenは、ユーザーが開いているローカルGitリポジトリに対してCoding Agentを実行する。リポジトリに関連するGitHubのIssueやPull Request（PR）の変化を定期的に確認し、利用者が指定した条件に一致した場合に、対象リポジトリを作業ディレクトリとするAgent Runを自動的に開始できるようにする。

初期の代表的なユースケースは次のとおりである。

1. 担当者が自分のIssueを作成し、GitHub Projects上のステータスを`Ready`にしたら、設計を実施する。
2. PRが作成されたら、レビューを実施する。

GitHub上のIssue、PR、ProjectsはAPIや権限によって取得可能な範囲が異なる。特にProjectsの情報取得に失敗しても、IssueやPR本体の監視まで停止させるべきではない。また、同じイベントを複数回取得するポーリングでは、同じプロンプトが繰り返し実行される危険がある。

監視から起動するRunも、通常のRunと同じWorking Tree、Session、Checkpoint、Usage、Event、コマンド承認のモデルに従わせる必要がある。監視機能専用のAgent実行経路や、Accept／Reject方式を追加してはならない。

## Decision

### 1. GitHub対象はローカルリポジトリのremoteから決定する

監視設定はローカルリポジトリに紐づく。Agent Managerは対象リポジトリのGit remoteを読み取り、GitHubのrepository URLから`host`、`owner`、`repository`を解決する。

- HTTPS URL（例: `https://github.com/octo-org/example.git`）とSSH URL（例: `git@github.com:octo-org/example.git`）を扱う。
- 末尾の`.git`、URLの末尾スラッシュ、SSHの別名表記を正規化する。
- `github.com`以外のホストは、GitHub Enterprise設定として明示的に許可されたホストだけを対象にできる。未設定のホストはGitHub対象外とする。
- remoteは既定で`origin`を優先し、`origin`がGitHubでない場合はGitHubを指すremoteが一つだけならそれを使用する。
- GitHubを指すremoteが複数あり、対象を一意に決定できない場合は自動監視を開始せず、設定画面でremoteを選択させる。
- remoteの追加・変更やリポジトリの移動を検出した場合、監視対象を再解決し、対象が変わるまでは前のrepositoryへ問い合わせない。

解決したrepository情報は、監視設定と現在の対象の表示に使用する。GitHub APIのリクエストでは、remote URLに含まれる資格情報を使用せず、ログにも保存しない。

### 2. 初期実装は定期ポーリングとし、GitHub APIアクセスをAgent Managerに集約する

初期実装ではWebhookを受け付けず、Agent Managerが設定された間隔でGitHub APIをポーリングする。ポーリング間隔、認証方式、APIのレート制限、最終取得時刻はAgent Managerが管理する。

GitHub固有のHTTP、REST／GraphQL、ページング、認証エラー、レート制限、レスポンス変換はAgent Manager内のGitHub adapterに閉じ込める。WebやVS Code ExtensionがGitHub APIを直接呼び出してはならない。

GitHubデータの取得経路は次の二つに分離する。

1. **監視取得**: Repository Monitorが定期取得し、差分検出に必要な正規化観測状態、監視イベント、重複排除キーだけをMaatgen DBへ保存する。
2. **画面表示取得**: Web版でIssue一覧、PR一覧、Issue詳細、PR詳細を開いた時点でGitHub APIから取得し、レスポンスを画面へ返す。取得したIssue／PR表示情報はMaatgen DBへ保存せず、監視取得の観測状態も一覧・詳細表示のデータソースにしない。

二つの経路は認証とGitHub adapterを共有できるが、永続モデルと用途を共有しない。画面表示取得が監視の観測基準点やイベント判定を更新してはならず、監視取得の成功・失敗も画面表示取得の結果によって上書きしない。

取得する基本情報は次のとおりとする。

- Issue: number、title、body、state、author、assignees、labels、milestones、作成・更新時刻、URL
- PR: number、title、body、state、draft、author、assignees、labels、base／head、作成・更新時刻、URL
- Issueに紐づくProjectsの情報: project、field、field value（特にStatus）

IssueとPRは同じGitHub itemとして正規化し、`kind`（`issue`または`pull_request`）で区別する。PRはIssue API上の表現とPR固有情報の両方を保持する。

監視取得ではProjectsの取得をIssue本体の取得と分離する。Projectsの権限不足、未導入、API未対応、部分的なGraphQLエラーなどが発生した場合は、観測状態にProject情報の欠損を記録し、Issue／PR本体の監視を継続する。ProjectのStatusを条件に含む監視は、Project情報が欠損している間は一致扱いにせず、次回取得で再評価する。画面表示取得でProjectsを取得できない場合は、そのレスポンス内で欠損状態を示し、Issue／PR本体は表示する。

認証情報は既存の安全な設定機構へ保存し、表示・ログ・Promptへの自動埋め込みを禁止する。APIが返した本文やコメントは不可信な外部入力として扱い、Promptに含める場合も、監視設定で明示したテンプレートへのデータとして渡す。テンプレート展開時は外部データを構造化されたデータブロックとして区切り、外部データ内の指示をMaatgenの実行指示として扱わない。Issue／PRのbody、コメントは既定ではPromptへ含めず、ルールで明示的に選択した場合だけ含める。

### 3. Repository監視設定と発火ルールを分離して表現する

GitHub APIからの取得はリポジトリ単位で行い、発火条件とPromptはリポジトリごとのルールとして複数作成できる。ポーリングをルールごとに実行して同じデータを重複取得しないよう、Repository Monitor設定とTrigger Ruleを分離する。

Repository Monitor設定は少なくとも次の項目を持つ。

```ts
interface GitHubRepositoryMonitor {
  id: string;
  repositoryId: string;
  remoteName?: string;
  enabled: boolean;
  pollIntervalSeconds: number;
  lastSyncedAt?: string;
  nextSyncAt?: string;
  lastError?: string;
}
```

Trigger Ruleは少なくとも次の項目を持つ。

```ts
interface GitHubTriggerRule {
  id: string;
  repositoryId: string;
  enabled: boolean;
  eventKinds: Array<'issue' | 'pull_request'>;
  filters: GitHubMonitorFilters;
  promptTemplate: string;
  provider: 'codex' | 'claude' | 'copilot';
  model?: string;
  reasoningEffort?: string;
  concurrencyPolicy: 'skip' | 'coalesce';
  createdAt: string;
  updatedAt: string;
}
```

`filters`は固定の一条件だけにせず、次の項目を組み合わせられる構造とする。初期UIで全項目を公開する必要はないが、Protocolと永続モデルでは拡張可能な形を確保する。

- Issue／PRの種別
- action（作成、更新、オープン、クローズ、再オープン等）
- number、title、body、author
- assignee、label、milestone
- state、draft
- base／head branch
- Project、Project field、field value（例: Status = `Ready`）
- 作成・更新時刻の範囲

条件の意味は「取得した最新状態」ではなく、前回観測状態との差分を含む監視イベントに対して評価する。例えば`Status = Ready`は、現在値が`Ready`であり、初回同期で既にReadyだった項目を無条件に遡って実行しない。初回同期は観測基準点を作るだけとし、既存項目を大量起動しない。

### 4. 条件一致時は通常のAgent Runとしてプロンプトを実行する

Trigger Ruleに一致した場合、Agent Managerは対象リポジトリに対して新しい通常のAgent Sessionを作成し、そのSessionでRunを起動する。自動実行された処理を既存の手動Sessionや他の監視ルールのSessionへ混在させない。作成されたSessionには、監視ルール、検出したGitHub item、発火イベント、`triggerSource: github_monitor`、選択したProvider／modelへの関連を保存し、通常のSessionと同じライフサイクル、履歴、Checkpoint、Usage、ChangeSet、Restoreの扱いを適用する。

Promptは利用者が設定したテンプレートから生成する。テンプレートには、少なくともrepository、kind、number、title、URL、author、assignees、labels、Project情報、検出actionを構造化データとして展開できるようにする。テンプレートの未定義変数は空値とし、本文全体や機密情報を暗黙に追加しない。

自動起動されたRunも通常のRunと同じく、次を実行する。

1. Run開始前のWorking Tree checkpointを作成する。
2. 対象リポジトリをcwdとして、選択されたProviderのAgentを起動する。
3. Usage、Event、ChangeSet、after snapshotを記録する。
4. 失敗、キャンセル、タイムアウトを含むすべてのterminal stateでafter snapshotを作成する。

監視機能はコマンド承認をバイパスしない。Providerごとの実行引数と承認方式は各Adapterに残し、Codexの承認は[ADR-006](./006-command-approval.md)に従う。監視ルールの有効化は、任意コマンドの無条件許可を意味しない。

自動Runが承認待ちになった場合は、通常の`waiting_for_approval`としてMaatgen DBに保存し、Web／VS Codeから確認できるようにする。承認待ちの既定タイムアウトは設定可能とし、期限切れ時はコマンドを拒否してRunを`failed`または`cancelled`へ遷移させ、監視イベントにタイムアウト理由を記録する。Manager再起動後も、Provider processを復元できない承認要求は再実行せず、該当Runを失敗として確定する。

### 5. 同一リポジトリのAgent実行を直列化する

Working Tree、Checkpoint、ChangeSet、Restoreはリポジトリ単位で共有されるため、同一リポジトリに対するAgent Runは同時に一つだけ実行する。自動Run同士だけでなく、手動Runと自動Runの組み合わせも対象とする。

Repository execution lockをAgent Managerに置き、Run開始前に取得する。ロック取得中の自動RunはTrigger Ruleの`concurrencyPolicy`に従う。`skip`では自動起動しないがイベントを`skipped`として履歴へ残し、後から手動実行できるようにする。`coalesce`では`repository + rule + item kind + item number`ごとに最新の一致イベントを一件だけ保留する。手動Runは既存のRun開始エラー契約に従い、ロック中は開始せず利用者へ通知する。ロック解放後の再評価は、保留したイベント以外を無制限にキューへ積まない。

### 6. 重複実行を永続的な監視配信キーで防止する

ポーリングは同じ項目を繰り返し返すため、`repository + item kind + number + event identity + rule id`を監視配信キーとして保存する。GitHubのevent IDが取得できる場合はそれを使い、取得できない状態変化については、観測したaction、更新時刻、状態のハッシュから安定したキーを作る。

配信キーの作成は一意制約付きのトランザクションで行う。キーの登録に成功した監視評価だけがRunを起動し、既に存在するキーは無視する。Run起動の直前にManagerが停止しても、再起動時に未処理キーとRunの対応を復元できるよう、監視配信とRunの関連を永続化する。

監視イベントとRunの作成はOutbox方式で扱う。監視イベントを保存するトランザクション内で、発火結果を`queued`として記録し、別のdispatcherが未処理イベントからSession／Runを作成する。作成後に`session_created`、`run_started`へ進めるため、Manager停止によって「重複排除キーだけが存在してRunがない」状態を作らない。

自動実行の状態はMaatgenのSQLiteデータベースを正とする。少なくとも、監視ルール、差分検出に必要な最小限の正規化観測状態、監視イベント、重複排除キー、発火結果、起動したRunとの関連、最後のエラーと時刻をMaatgen側に保存する。この観測状態は監視専用であり、Issue／PR一覧・詳細の表示モデルや永続キャッシュとして利用しない。監視イベントには`detected`、`matched`、`queued`、`session_created`、`run_started`、`skipped`、`completed`、`failed`、`cancelled`を使用し、Runの状態とは別に管理する。Manager再起動後も監視履歴と実行状態を復元できるようにする。

GitHubは監視対象データの取得元に限定し、自動実行の状態をGitHubへ反映しない。MaatgenはIssue／PRのラベル、コメント、Project field、ステータス、その他のGitHub上の属性を変更してはならない。したがって、実行済みかどうかの判定や再実行防止にGitHubラベルを利用せず、前述のMaatgenデータベース上の状態と配信キーだけを利用する。手動のGitHub変更によって取得データが変化した場合は、次回ポーリング時に新しい観測イベントとして評価する。

Repository execution lock取得中にTrigger Ruleのイベントが発生した場合の扱いは、ルールごとに`skip`または`coalesce`を選べるようにし、既定値は`coalesce`とする。`coalesce`ではIssue／PRごとの最新イベントを保持し、実行中Runの完了後に再評価するため、別のIssue／PRのイベントが互いを上書きしない。

利用者が`skip`を明示的に選んだ場合も、イベント自体は削除せず、理由、適用ルール、item identity、Prompt生成に必要な監視時データをイベント履歴へ保存する。イベント履歴に「このイベントを実行」を設け、選択すると元イベントを`replayOfEventId`で参照する新しいOutbox配信を作成し、新しい通常Sessionとして実行する。同じ元イベントの手動実行は操作ごとに別のreplay IDを持ち、自動実行の重複排除キーを変更しない。Repository execution lock取得中は実行を開始せず、その旨を表示するため、`skip`されたイベントが永久に回収不能になることはない。

`coalesce`の保留件数にはrepository単位の設定可能な上限を設ける。上限到達時は古いイベントを黙って破棄せず`skipped`へ遷移させ、イベント履歴から手動実行できる状態を維持する。無制限のRunキューは初期実装の対象外とする。

イベントはポーリング間に観測した状態差分を単位とする。複数回のGitHub変更が一つのポーリング間隔に含まれる場合、GitHubのイベント履歴を取得できない初期実装では、最新状態への一つの状態差分にまとめる。イベントには変更前後の正規化状態ハッシュ、検出action、観測時刻を保存する。GitHub由来のevent IDが取得できない場合も、これらを含む配信キーで再評価を防止する。

Issue／PR一覧は選択中のローカルrepositoryを対象とする。複数repositoryを横断した一覧は初期実装では提供せず、共通ナビゲーションのGitHub監視領域にはrepository selectorを置く。repositoryを切り替えた場合は、一覧、イベント履歴、監視ルールを選択repositoryのデータへ切り替える。

### 7. 監視設定はWeb版で管理し、自動実行SessionはWeb版とVS Code版から参照する

監視設定の作成、編集、有効化・無効化、手動同期、直近の取得状態、エラー、イベント履歴はWeb版から提供する。初期実装ではVS Code版に監視設定の作成・編集画面を設けない。

自動実行で作成されたSessionは、通常のSessionと同じ共通Protocol／APIで公開する。Web版とVS Code版は、そのSessionの履歴、Run、Usage、Event、ChangeSet、実行状態を参照できる。surfaceに応じて配置と操作方法は適応させるが、自動実行Sessionだけを専用の別モデルや別表示にしない。

Web版の監視設定画面には次を表示する。

- 解決されたGitHub host、owner、repository、remote
- GitHub認証状態とProjects取得状態
- イベント種別とフィルタ条件
- 条件一致時に実行するPromptテンプレート
- ポーリング間隔と有効／無効
- 最終同期時刻、次回同期予定、直近エラー
- 直近の一致イベント、起動したRun、重複排除・skipの結果

保存前に、解決済みrepository、フィルタ構文、Promptテンプレートを検証する。Project条件を設定した場合は、Project情報が取得できない可能性と、その間は発火しないことをUIで示す。

#### Web版の画面構成

監視機能は、既存のSession／Run画面から独立したWeb版の「GitHub監視」画面として提供する。画面は、対象リポジトリの状態、Issue／PR一覧、監視ルール、イベント履歴を確認できる構成とする。

1. **対象リポジトリ領域**
   - 現在のローカルリポジトリとGit remote
   - 解決されたGitHub host、owner、repository
   - remoteが未解決・複数候補・変更済みの場合の警告と選択操作
   - GitHub認証状態、Projects取得可否、最終同期時刻
   - Repository Monitorのポーリング間隔と有効／無効
   - `今すぐ同期`操作と、同期中・レート制限・取得エラーの表示

2. **監視ルール一覧／編集領域**
   - ルール名、対象イベント、主なフィルタ、Prompt概要、enabled状態
   - ルールの新規作成、編集、複製、有効化・無効化、削除
   - 編集フォームの項目は、イベント種別、action、担当者、label、state、draft、branch、Project／Status、Provider、model、reasoning effort、同時実行方針、Promptテンプレートとする
   - 保存時に必須条件、フィルタ値、Provider／model、Promptテンプレートを検証する
   - Project条件を設定した場合、Projects情報が取得できない間は発火しないことをフィルタ欄の近くに表示する

3. **イベント履歴／状態領域**
   - 検出したIssue／PR、適用ルール、条件評価結果、Maatgen内の自動実行状態
   - `detected`、`matched`、`queued`、`session_created`、`run_started`、`skipped`、`completed`、`failed`、`cancelled`の状態
   - 起動した新規SessionとRunへのリンク
   - 重複排除、同時実行中のskip／coalesce、Projects情報欠損、APIエラーの理由
   - `skipped`イベントの「このイベントを実行」操作と、手動実行元イベントへの関連
   - 自動実行SessionをWeb版の通常Session詳細画面へ遷移する操作

4. **Issue一覧／詳細画面**
   - 画面を開いた時点でGitHub APIから取得したIssueを一覧表示し、取得結果はMaatgen DBへ保存しない
   - number、title、state、author、assignees、labels、Project／Status、更新時刻、画面取得時刻を表示する
   - title、number、assignee、label、state、Project／Status、更新時刻で絞り込めるようにする
   - フィルタは、GitHub風の複数条件検索テキストと、条件別のテキスト入力／プルダウンを併用する
   - 一覧の行またはカードをクリックするとIssue詳細画面を開く
   - 詳細画面ではGitHub APIからその時点のIssue本体とProject情報を取得し、Maatgen DBに別途保存された監視イベント、適用ルール、条件評価結果、関連Session／Runをrepositoryとitem identityで関連付けて表示する
   - GitHub上のIssueを開くリンクを提供するが、表示データの取得はMaatgen API経由とする

5. **PR一覧／詳細画面**
   - 画面を開いた時点でGitHub APIから取得したPRを一覧表示し、取得結果はMaatgen DBへ保存しない
   - number、title、state、draft、author、assignees、labels、base／head branch、更新時刻、画面取得時刻を表示する
   - title、number、author、assignee、label、state、draft、branch、更新時刻で絞り込めるようにする
   - 一覧の行またはカードをクリックするとPR詳細画面を開く
   - 詳細画面ではGitHub APIからその時点のPR本体を取得し、Maatgen DBに別途保存された監視イベント、適用ルール、条件評価結果、関連Session／Runをrepositoryとitem identityで関連付けて表示する
   - GitHub上のPRを開くリンクを提供するが、GitHubへのコメント、ラベル変更、レビュー投稿などの書き戻しは行わない

画面上の保存・同期・有効化操作はAgent ManagerのAPIを通じて行う。ブラウザからGitHub APIへ直接アクセスせず、認証情報やremote URLに含まれる資格情報を画面へ表示しない。自動実行Sessionの詳細画面では、通常Sessionと同じConversation、Run、Usage、Event、ChangeSetを表示する。

Issue／PR一覧と詳細は表示要求ごとにAgent ManagerがGitHub APIから取得する。GitHub APIへアクセスできない場合は保存済みデータへフォールバックせず、取得エラー、認証状態、レート制限情報を表示する。同じ画面を開いている間の一時的なメモリキャッシュは許可するが、再起動後に利用する永続キャッシュは作らない。Project情報など一部だけ取得できない場合は、そのレスポンスで欠損状態を表示し、Issue／PR本体の表示を隠さない。

監視イベントと関連SessionはMaatgen DBに保持するため、GitHubからIssue／PR本体を取得できない場合でもイベント履歴から参照できる。ただし、その画面では監視時に記録したitem number、titleの要約、URL、イベント差分だけを履歴情報として表示し、現在のIssue／PR詳細であるかのように扱わない。

#### Issue一覧のフィルタ方式

Issue一覧では、利用者の習熟度と操作の発見性を両立するため、次の方式を比較した。

| 方式 | 長所 | 短所 |
| --- | --- | --- |
| 単一の検索テキスト | GitHub利用者に馴染み、`assignee:me state:open label:bug`のような複数条件を短く入力できる | 構文を知らない利用者には分かりにくく、入力ミスの説明も必要になる |
| 条件別のテキスト／プルダウン | 条件が見えるため発見性と入力検証に優れる | 条件が増えると画面が長くなり、複雑な組み合わせを表現しにくい |
| 併用 | 簡単な操作と高度な検索の両方に対応できる | 2つの入力方式を同じ条件モデルへ同期する必要がある |

採用方式は併用とする。Issue一覧の上部に検索テキスト欄を置き、その下に現在の条件をチップとして表示する。よく使う条件は補助コントロール（state、assignee、label、Project／Status、sort）から追加・変更でき、補助コントロールで作った条件は検索テキストへ反映する。検索テキストを直接編集した場合も、解釈できる条件はチップと補助コントロールへ反映する。

初期検索構文は次の小さなサブセットから開始する。

- `state:open`、`state:closed`
- `assignee:<login>`、`assignee:me`
- `author:<login>`
- `label:<name>`（空白を含む値は引用符で囲む）
- `project:<name>`、`status:<value>`
- `is:assigned`、`is:unassigned`
- `text:<word>`または構文外の文字列によるtitle／bodyの全文検索
- 条件の前に`-`を付けた否定条件（例: `-label:wontfix`）

空白で区切った条件はANDとして評価する。OR、括弧、GitHub固有の未対応構文は初期実装では扱わず、入力欄に構文エラーと未対応条件を表示する。大文字・小文字、login、label、Project名の比較規則は内部の構造化フィルタで統一する。

`assignee:me`はGitHub APIで認証された利用者のloginへ解決する。認証利用者を取得できない場合は、`me`を全件一致や空値として扱わず、フィルタエラーとして表示する。title／bodyの全文検索はGitHub APIの検索機能を利用する。

検索テキストと補助コントロールはParserで`GitHubIssueFilters`へ変換し、一覧APIは構造化されたフィルタを受け取る。Agent ManagerはGitHub検索へ渡せる条件をAPI queryへ変換し、Project／Statusなど追加取得が必要な条件はGraphQLで補完してから結果を返す。画面表示用文字列をGitHub queryへ無検証で連結しない。これにより、検索欄と個別コントロールで結果が一致し、監視DBの観測データには依存しない。

フィルタを変更したときは短いdebounce後にGitHub APIから一覧を再取得する。検索条件はURLのquery parameterにも反映し、一覧から詳細へ移動して戻った場合やURLを共有した場合に復元できる。`すべて解除`で検索テキスト、条件チップ、補助コントロールを初期状態へ戻す。該当件数、Project情報欠損件数、画面取得時刻を一覧上部に表示する。

VS Code版には監視ルール編集画面を追加しない。Session一覧やSession詳細から、自動実行されたSessionを通常のSessionと同じように検索・選択し、Run、Usage、Event、ChangeSetを参照できるようにする。

#### 共通ナビゲーションと画面遷移

現状のSession画面にある`Usage／Changes／Source`はSession詳細内のタブであり、アプリ全体を移動する共通ナビゲーションではない。GitHub監視の追加に合わせて、Web版のアプリシェルにヘッダーまたは左サイドバーの共通ナビゲーションを追加する。

共通ナビゲーションは次の項目を持つ。

- **Session**: 既存のSession一覧へ遷移する
- **GitHub監視**: GitHub監視トップへ遷移する
  - **イベント履歴**: 対象リポジトリ、同期状態、監視ルール、イベント履歴
  - **Issue**: 画面表示時にGitHubから取得するIssue一覧
  - **PR**: 画面表示時にGitHubから取得するPR一覧
  - **設定**: 監視ルールの作成・編集

Issue一覧、PR一覧、監視設定は、GitHub監視トップを経由しなくても共通ナビゲーションから直接開けるようにする。監視設定はSession画面のモーダルやRun画面内の専用タブにはせず、独立した画面として扱う。

```text
共通ナビゲーション
  ├─ Session
  │    └─ Session一覧 / Session詳細
  └─ GitHub監視
       ├─ イベント履歴
       ├─ Issue一覧
       ├─ PR一覧
       └─ 設定

Session一覧 / Session詳細
        │ 共通ナビゲーション「GitHub監視 > イベント履歴」
        ▼
GitHub監視トップ（対象リポジトリ・ルール一覧・イベント履歴）
        │ 「新しいルール」「編集」
        ▼
監視ルール設定
        │ 保存／キャンセル
        ▼
GitHub監視トップ

GitHub監視トップ
        │ Issue一覧／PR一覧の選択
        ▼
Issue一覧／PR一覧
        │ Issue／PRをクリック
        ▼
Issue詳細／PR詳細
        │ 関連Session／Runリンク
        ▼
通常のSession詳細

GitHub監視トップ
        │ イベント履歴のSession／Runリンク
        ▼
通常のSession詳細
```

- 既存のSession一覧、Session詳細、Usage、ChangeSet画面から、アプリシェルの共通ナビゲーションでSessionとGitHub監視を相互に切り替えられる。
- 共通ナビゲーションの「GitHub監視 > Issue」「GitHub監視 > PR」「GitHub監視 > 設定」から、それぞれの画面へ直接遷移できる。
- GitHub監視トップから「Issue一覧」または「PR一覧」へ遷移し、一覧の項目をクリックすると、それぞれの詳細画面へ遷移する。
- Issue／PR詳細画面から関連Session／Runを選択すると、通常のSession詳細画面へ遷移する。
- 監視トップから「Session一覧へ戻る」を選ぶと、遷移前のSession一覧へ戻る。ブラウザの戻る操作でも直前画面へ戻れるようにする。
- ルール編集後は監視トップへ戻り、保存したルールを一覧の先頭または元の位置で表示する。キャンセル時は変更を保存せず、編集開始元の監視トップへ戻る。
- イベント履歴から開くSession詳細は、新しい別画面ではなく通常のSession詳細画面を使用する。戻る操作では監視トップのイベント履歴とスクロール位置を復元する。
- GitHub監視トップを直接開いた場合、対象リポジトリを解決できなければリポジトリ選択・remote選択の案内を最初に表示する。
- VS Code版では共通ナビゲーションに監視設定入口を追加せず、自動実行Sessionへのリンクを通常のSession一覧・Session詳細の導線で扱う。設定変更が必要な場合はWeb版の監視画面を開く案内を表示する。

## Scope and non-goals

初期実装の対象は、監視用のIssue／PR取得、IssueのProjects情報取得、Maatgen DBへの監視用観測状態保存、画面表示時にGitHubから取得して永続化しないIssue／PR一覧・詳細画面、保存可能なフィルタとPromptテンプレート、定期ポーリング、重複排除、通常Agent Session／Runの起動、Web版の監視設定画面、Web版／VS Code版からの自動実行Session参照とする。

次は初期実装の対象外とする。

- GitHub Webhookの受信・公開エンドポイント
- コメント投稿、ラベル変更、Project更新などGitHubへの書き戻し（自動実行状態の反映を含む）
- 複数GitHub repositoryをまたぐ一つのルール
- Project情報が取得できない場合の推測によるStatus判定
- 外部イベントごとの無制限なRunキュー
- 監視専用のAgent実行APIや、通常Runと異なる権限モデル

## Consequences

### Positive

- ローカルリポジトリとGitHub repositoryの対応を利用者が二重入力せずに設定できる。
- Projectsの権限やAPI障害があっても、Issue／PR監視を継続できる。
- 監視から起動した処理も通常のSession／Run／Checkpoint／Restoreの監査と回復に統合できる。
- ルールごとのPromptとフィルタを保存でき、設計・レビューなどの定型作業を再利用できる。

### Negative

- ポーリング間隔に応じた遅延と、GitHub APIレート制限への配慮が必要になる。
- GitHub Projectsの取得には追加権限やGraphQL処理が必要で、部分的なデータ欠損をUIと評価系が扱う必要がある。
- 自動起動されたRunによるWorking Tree変更があるため、Checkpoint、同時実行制御、履歴表示が重要になる。
- Issue／PR本文をPromptに渡す場合、外部入力によるPrompt Injectionや意図しない指示の混入に注意が必要になる。

## Implementation notes

実装時は次の順序で進める。

1. Git remote解決とGitHub item／Projectの正規化契約を追加する。
2. GitHub adapter、認証、監視取得と画面表示取得の分離、ページング、レート制限、部分失敗を実装する。
3. Repository Monitor設定、Trigger Rule、観測状態、配信キー、監視Run関連をSQLiteへ追加する。
4. 条件評価とPromptテンプレート展開を実装し、初回同期・重複・Project欠損をテストする。
5. Repository execution lock、Outbox dispatcher、通常Run起動との統合、承認待ちタイムアウト、停止・再起動時の復元を実装する。
6. Web版の設定画面、GitHubから都度取得するIssue／PR一覧・詳細画面、Web版／VS Code版からの自動実行Session参照を追加し、手動同期とイベント履歴を検証する。

最低限、remoteの形式違い、複数remote、Project取得失敗、初回同期、同一イベントの再取得、画面表示取得が監視状態を更新しないこと、Issue／PR表示データをDBへ保存しないこと、Outbox途中停止からの復元、同一repositoryでの手動／自動Run競合、`coalesce`のitem単位集約、`skipped`イベントの手動実行、承認待ちタイムアウト、Run失敗後のafter snapshotをテストする。
