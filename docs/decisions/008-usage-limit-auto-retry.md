# ADR-008: 使用量上限に達したRunを自動的に検知・待機・再開する

- Status: Accepted
- Date: 2026-08-26
- Owners: Agent Manager
- Related: [Coding Agent設計書](../coding-agent-design.md)、[実装計画](../implementation-plan.md)

## Context

Claude Code、Codex、GitHub CopilotのCLIは、いずれもアカウント単位の使用量上限（セッション、5時間、週次など）を持ち、上限に達するとRunの途中でエラー終了する。Claude Codeでは典型的に"You've hit your session limit"のような文言でCLIが停止する。現状、この停止はほかの失敗と同様に単に`Run`を`failed`にするだけで、利用者が使用量の回復を確認してから手動で同じSessionに続きのプロンプトを送る必要がある。

Agent Managerには既に、プロバイダごとのCLIから使用量ウィンドウ（`ProviderUsageWindow`: 使用率、残量、リセット表示）を取得する`agent.UsageReader`（`GetUsage`）と、それをプロバイダ名でまとめて呼び出す`providerusage.Service`が実装済みである。また、GitHub監視から起動するジョブについては、`githuboutbox.Dispatcher`がRun開始前に使用量を確認し、枯渇していれば起動を見送って次回のポーリングTickで再評価する先例がある（`internal/githuboutbox/dispatcher.go`の`checkProviderUsageReady`）。ただしこれはRun開始前のガードに過ぎず、実行中に上限へ到達して停止したRunを検知して自動的に再開する仕組みは無い。

## Decision

### 1. 検知はCLI出力行に対する広めのキーワード一致で行う

プロバイダのJSONLプロトコルは、使用量上限による停止を他のエラーと区別できる機械可読なコードを常に持つとは限らない（例: Claude Codeが平文の非JSON行を出して終了する場合、既存の`ParseLine`は「不正なJSONL行」として`malformed`イベントに倒すだけで、実際の文言は`RunFailed`イベントのmessageには載らない）。厳密なプロトコル別パーサ拡張は、各CLIの正確な出力形式をこの実装時点で実機検証できないため、リスクが高い。

そこで、`internal/agent.LooksLikeUsageLimitMessage(line string) bool`という、プロバイダに依存しない大小文字非依存のキーワード一致関数を追加し、`run.Service.execute`がRunの標準出力・標準エラー出力の**すべての行**に対してこれを適用する。一致した行が観測され、かつRunが最終的に`failed`で終了した場合に「使用量上限による停止」として扱う。

キーワードは`usage limit`、`session limit`、`you've hit your`、`you have hit your`、`rate limit exceeded`、`quota exceeded`とする。単独の`limit reached`は含めない。Claude Codeには使用量上限とは無関係な"turn limit reached"（`error_max_turns`、1ターンあたりのツール呼び出し上限）が存在し、これを誤って使用量上限として検知しないためである。

**既知の制約**: この一致は実際のCLI出力を実機確認せずに設計したものであり、将来CLIの文言が変わった場合、あるいはCodex／Copilotがここに列挙していない文言で上限到達を報告した場合は検知されない。誤検知（無関係なエラーメッセージにたまたまキーワードが含まれる）の可能性もあるが、影響は「本来不要な自動リトライが1回だけ追加で行われる」に留まるため許容する。

### 2. 待機と回復判定は既存の使用量チェック機能をポーリングで再利用する

`ProviderUsageWindow.ResetLabel`はプロバイダごとに書式が異なり、Codexは絶対時刻（RFC3339）を格納できる一方、Claude Codeは人間向けの文字列（例: "4pm (Asia/Tokyo)"）しか返さない。正確な回復時刻を全プロバイダで一様に算出することはできないため、`githuboutbox.Dispatcher`と同じ方式——`providerusage.Service.GetProviderUsage`を定期的に呼び出し、すべてのウィンドウの`RemainingPercent`が0を超えるまで待つ——を採用する。

新しい`internal/usageretry`パッケージが、保留中のRun（後述）を対象にこのポーリングを行うバックグラウンドループを持つ。使用量チェックAPIの呼び出し自体が失敗した場合は、既存の`githuboutbox`と同じ方針（使用量監視はオプショナルであり、取得失敗はRunの実行を妨げない）に倣い「回復済み」とみなしてリトライを試みる。1回のみのリトライ上限があるため、誤って早期にリトライしても実害は「1回分の早すぎる再試行」に留まる。

### 3. 再開は同一Session・同一スレッドへの新しいRunとして行う

Agent Managerの既存の設計（AGENTS.md: "A Session remains active after a Run. The next prompt resumes the same Codex thread from the current Working Tree and creates a new before checkpoint."）により、`run.Service.StartRun`を同じSession IDに対して呼び出すだけで、CLIは記録済みのスレッドID（`AgentThreadID`）を使って同じ会話を再開する。したがって、自動リトライも通常の「続きのプロンプトを送る」操作と全く同じ経路（`StartRun`）を使う。専用の再開APIや、GitHub監視のような別のAgent実行経路は追加しない。

再開時に送信するプロンプトは固定文言とする:

```
使用量上限のため中断していた処理を再開してください。中断前の指示の続きを行ってください。
```

この文言は通常のユーザープロンプトと同じく`user_prompt`イベントとして記録される。新しいSessionEvent種別は追加しない（後述）。

### 4. リトライは1回のみとし、Run間の親子関係で制御する

`protocol.AgentRun`に`AutoRetryOfRunID *string`を追加する。自動リトライで開始された新しいRunは、失敗した元のRunのIDをこのフィールドに保持する。`run.Service.execute`は、「このRun自身が既に`AutoRetryOfRunID`を持つ場合（＝自動リトライによって開始されたRunである場合）は、たとえ使用量上限で再び失敗しても新たな保留状態を作らない」というガードにより、同一の停止に対して自動リトライが連鎖しないことを保証する。セッション単位のフラグ（「このセッションは既に1回自動リトライした」）ではなく、Run単位の親子関係を採用したのは、同じSessionが後日（例えば翌日）再び上限に達した場合に、まったく別の停止として改めて1回のリトライ機会を持てるようにするためである。

使用量上限で停止したRunは、`UsageLimitRetryPendingAt`（保留開始時刻、nullable）を持つ。`internal/usageretry`のTickはこの列がNULLでないRunを列挙し、使用量の回復を確認できたら`StartRun`を呼び、成功・失敗いずれの結果でも保留状態を解除する（`ErrRepositoryBusy`のときだけ、リポジトリの実行ロックが空くのを待つために保留を維持する）。

### 5. 新しいSessionEvent種別は追加しない

`SessionEvent.type`はWeb／VS Code両方でTypeScriptの厳密な文字列リテラル型として生成されており、新しい種別を追加するとプロトコルのスキーマ・生成コード・両フロントエンドの更新が必要になる。本機能の受入条件（検知・待機・再開・再開指示の送信が「確認できる」こと）は、既存の`run_failed`（検知）と、自動リトライRunに付随する既存の`user_prompt`／`run_started`（再開指示の送信）、および`AgentRun.UsageLimitRetryPendingAt`／`AutoRetryOfRunID`という新しい永続フィールド（APIレスポンスとログで確認可能）で満たせるため、UIの表示上の作り込みは本実装の対象外とする。

## Scope and non-goals

対象は、Runの標準出力／標準エラー出力に対するキーワードベースの検知、使用量チェック機能のポーリングによる回復待機、同一Session・同一スレッドでの自動再開、Run単位でのリトライ上限（1回）の制御とする。

次は対象外とする。

- 使用量上限検知専用の新しいSessionEvent種別・UI表示（Web／VS Code）。
- Claude／Codex／Copilot個々のCLI出力形式を実機検証したうえでの厳密なプロトコルレベル検知。
- 使用量ウィンドウの正確な回復時刻に基づくスケジューリング（`ResetLabel`のプロバイダ間書式差異により、ポーリングでの再評価に留める）。
- 使用量上限以外の理由で失敗したRunの自動リトライ。
- 2回以上の自動リトライ、またはリトライ間隔・上限回数を利用者が設定する機能。

## Consequences

### Positive

- 使用量上限による一時的な停止から、利用者の手動操作なしに同じSession・同じ文脈で処理を継続できる。
- 既存の`UsageReader`／`providerusage.Service`／`StartRun`をそのまま再利用するため、プロバイダ固有の新しい実行経路を増やさない。
- 新しいSessionEvent種別を追加しないため、Protocolスキーマ・Web・VS Code拡張への波及を避けられる。

### Negative

- キーワード一致による検知は、CLIの文言変更や未知の表現に対して頑健ではない。
- Claude Codeの`ResetLabel`が正確な時刻を提供しないため、待機はポーリングに頼らざるを得ず、回復直後から実際にリトライされるまでに最大でポーリング間隔分の遅延が生じる。
- 誤検知によって、使用量上限とは無関係な失敗の直後に1回分の不要な自動リトライが発生する可能性がある。

## Implementation notes

実装時は次の順序で進める。

1. `internal/agent.LooksLikeUsageLimitMessage`を追加する。
2. `protocol.AgentRun`／`protocol.SendMessageRequest`に`AutoRetryOfRunID`、`protocol.AgentRun`に`UsageLimitRetryPendingAt`を追加し、SQLiteマイグレーションと`store.go`のCRUDを更新する。
3. `run.Service.execute`に検知ロジックと`UsageLimitRetryPendingAt`の設定を追加する。
4. `internal/usageretry.Service`を追加し、`cmd/agent-manager/main.go`で`providerusage.Service`・`run.Service`・SQLite storeを注入して起動する。
5. Run失敗時の検知、使用量枯渇中の待機、回復後の自動再開、1回のみのリトライ上限をテストする。
