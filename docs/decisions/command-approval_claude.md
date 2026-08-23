# ADR: Claude Agent SDKを用いたクロスプラットフォームAgent実行方式

## Status

Proposed

## Context

Maatgenでは、ClaudeをCoding Agentとして利用し、コード調査、ファイル編集、コマンド実行、テスト、再修正などのAgent動作そのものはClaudeに任せる。

Maatgen自身はAgentの行動計画を制御せず、主に以下を担当する。

* Claudeが要求したツール操作の監視
* コマンド実行の許可・拒否
* ファイル操作の許可・拒否
* 危険度判定
* ユーザーへの承認要求
* Agentの操作履歴・トークン利用量等の記録

また、MaatgenはWindowsおよびLinuxの両方で動作させる必要がある。

現在、ClaudeをMaatgenから利用する方式として、主に以下が考えられる。

1. Claude Code CLIを子プロセスとして起動する
2. Claude Code CLIをPTY経由で起動する
3. Claude Agent SDK/APIを利用してAgentを実行する
4. Maatgen自身でAgent loopを実装し、Claude Messages APIを利用する

Maatgenでは、Agentの判断やAgent loopをClaude側に任せたい。そのため、Maatgen自身がAgent loopを実装する方式は避ける。

さらに、CLIをPTY経由で制御する場合、WindowsとLinuxではPTYの実装が異なる。

* Linux: pseudo terminal（PTY）
* Windows: ConPTY

この差異をMaatgen側で吸収する必要があり、CLIの出力形式や対話仕様の変更にも影響を受ける。

## Decision

Claude Agent SDK/APIをAgent実行の基本方式として採用する。

ClaudeにAgentとしての行動判断を任せ、MaatgenはAgent Runtimeの外側でPermission、Policy、Auditを担当する。

基本構成を以下とする。

```text
                    ┌──────────────────────┐
                    │      VS Code UI      │
                    └──────────┬───────────┘
                               │
                               ▼
                    ┌──────────────────────┐
                    │       Maatgen        │
                    │                      │
                    │ Permission Manager   │
                    │ Policy Engine        │
                    │ Audit Logger         │
                    │ Session Manager      │
                    └──────────┬───────────┘
                               │
                               ▼
                    ┌──────────────────────┐
                    │ Claude Agent Runtime │
                    │  Agent SDK / API     │
                    └──────────┬───────────┘
                               │
                  ┌────────────┼────────────┐
                  │            │            │
                  ▼            ▼            ▼
               Files          Bash        MCP
```

Agentの内部ループはClaudeに任せる。

```text
Claude

調査
 ↓
計画
 ↓
ファイル参照
 ↓
修正
 ↓
テスト
 ↓
結果確認
 ↓
必要なら再修正
```

Maatgenはこのループを制御しない。

一方、副作用を伴う操作についてはMaatgenが制御する。

```text
Claude
   │
   │ Bash("npm test")
   ▼
Maatgen Permission Manager
   │
   ├─ 許可ルール一致 → Allow
   │
   ├─ 拒否ルール一致 → Deny
   │
   ├─ AI危険度判定
   │
   └─ ユーザー承認
          │
          ▼
      Allow / Deny
          │
          ▼
        Claude
```

## Cross-platform Design

MaatgenのAgent制御層では、Windows/Linux固有処理を可能な限り持たない。

以下の共通インターフェースを定義する。

```text
AgentRuntime

start()
sendPrompt()
approveTool()
denyTool()
cancel()
resume()
getUsage()
getEvents()
```

Claude固有の処理はAdapterとして実装する。

```text
AgentRuntime
     │
     ├── ClaudeAgentSdkRuntime
     │
     ├── ClaudeCliRuntime
     │
     ├── CodexRuntime
     │
     └── CopilotRuntime
```

Maatgen本体は、AgentがSDK経由かCLI経由かを意識しない。

## Command Execution

コマンド実行はOS依存となるため、Agent RuntimeとOS Command Runtimeを分離する。

```text
Claude
  │
  ▼
Tool Request
  │
  ▼
Maatgen Policy
  │
  ▼
Command Runtime
  │
  ├── WindowsCommandRuntime
  │      ├─ powershell.exe
  │      └─ cmd.exe
  │
  └── LinuxCommandRuntime
         ├─ /bin/bash
         └─ /bin/sh
```

Claudeから要求されたコマンド文字列を、そのまま特定OSのシェル構文として扱うのではなく、実行環境の情報をClaudeに渡した上で、そのOSに適したコマンドをClaudeに生成させる。

例えばAgent session開始時に以下の情報を渡す。

```text
OS: Windows
Shell: PowerShell
Working Directory: C:\src\project
```

または、

```text
OS: Linux
Shell: bash
Working Directory: /home/user/project
```

## File Access

ファイルアクセスのPolicy判定では、Windows/Linuxのパス表現の違いを吸収する。

内部ではパスを正規化して扱う。

例:

```text
Windows

C:\src\maatgen\frontend\src\App.vue
```

```text
Linux

/home/user/maatgen/frontend/src/App.vue
```

Policy Engineに渡す前に以下を行う。

* `.` / `..` の解決
* Symbolic Linkの考慮
* Windows drive letterの正規化
* Path separatorの正規化
* 大文字・小文字の扱い
* workspace外へのescape判定

特にWindowsではパスが原則case-insensitiveであるのに対し、Linuxではcase-sensitiveである点を考慮する。

## Permission Model

ツール実行要求を以下のような共通形式に変換する。

```json
{
  "agent": "claude",
  "tool": "bash",
  "operation": "execute",
  "command": "npm test",
  "cwd": "/workspace/project"
}
```

ファイル操作の場合、

```json
{
  "agent": "claude",
  "tool": "file",
  "operation": "write",
  "path": "/workspace/project/src/main.ts"
}
```

この共通形式に変換した後、Policy Engineで判定する。

```text
Tool Request
     │
     ▼
Normalize
     │
     ▼
Static Policy
     │
     ├─ Allow
     ├─ Deny
     └─ Unknown
          │
          ▼
    Risk Evaluation
          │
          ├─ Low → Allow
          ├─ High → User confirmation
          └─ Critical → Deny
```

これにより、Claude、Codex、Copilot等のAgentごとのツール表現の違いをMaatgen側で吸収する。

## CLI Fallback

Claude Agent SDK/APIで実現できない機能が存在する可能性を考慮し、CLI RuntimeもAdapterとして残す。

```text
AgentRuntime
    │
    ├── ClaudeAgentSdkRuntime   ← Primary
    │
    └── ClaudeCliRuntime        ← Fallback
```

CLI RuntimeではOSごとにPTY Adapterを実装する。

```text
ClaudeCliRuntime
       │
       ▼
TerminalAdapter
       │
       ├── UnixPtyAdapter
       │
       └── WindowsConPtyAdapter
```

ただし、CLI出力の解析や対話制御はClaude Code CLIの仕様変更の影響を受けやすいため、Agent SDK/APIが利用可能な機能についてはCLIを使用しない。

## Alternatives Considered

### Claude Code CLI + PTY

#### Advantages

* Claude Codeそのものを利用できる
* Claude CodeのAgent能力をそのまま利用できる
* ユーザーがCLIで利用する動作に近い

#### Disadvantages

* WindowsとLinuxでPTY実装が異なる
* WindowsではConPTY対応が必要
* CLIのUI出力を解析する必要が生じる可能性がある
* Claude CodeのCLI仕様変更に影響される
* Permission制御の確実性を担保しにくい
* プロセス管理が複雑になる

このためPrimary方式としては採用しない。

### Messages API + 独自Agent loop

#### Advantages

* Maatgenがすべての挙動を制御できる
* Agent依存を減らせる

#### Disadvantages

* Agent loopをMaatgen自身で実装する必要がある
* Claude Code/Claude Agentが持つAgentロジックを再実装することになる
* Tool retry、context管理、planning等の実装が必要
* Maatgenの責務が大きくなる

「Agentの動作はClaudeに任せる」というMaatgenの方針に合わないため採用しない。

### Claude Agent SDK/API

#### Advantages

* Agent loopをClaude側に任せられる
* MaatgenはPermission/Policy/Auditに集中できる
* Windows/Linux差異を小さくできる
* PTYへの依存を減らせる
* 構造化されたTool Requestを扱いやすい
* 将来的に他AgentもAdapterとして追加しやすい

#### Disadvantages

* Claude SDK/APIの仕様に依存する
* Claude Code CLIと完全に同じAgent挙動になるとは限らない
* SDKで公開されていないClaude Code固有機能を利用できない可能性がある

以上からPrimary方式として採用する。

## Consequences

Maatgenの責務は以下になる。

```text
Maatgen
│
├── Agent abstraction
│
├── Permission
│
├── Policy
│
├── Risk evaluation
│
├── Audit
│
├── Usage tracking
└── UI
```

Claudeの責務は以下になる。

```text
Claude
│
├── Planning
├── Reasoning
├── Code investigation
├── Tool selection
├── Code modification
├── Test execution
└── Retry / Re-planning
```

これにより、

**「何をするか」はClaude**

**「それを実行してよいか」はMaatgen**

という責務分離を行う。

また、MaatgenのAgent abstractionを共通化することで、将来的に以下のAgentを同一UI・同一Policy Engineで扱えるようにする。

```text
Maatgen

    ├── Claude
    ├── Codex
    ├── GitHub Copilot
    └── Other Agents
```

## Summary

Claude Agent SDK/APIをPrimary Agent Runtimeとする。

Agent loopや行動判断はClaudeに任せ、MaatgenはAgentの行動そのものを制御しない。

MaatgenはClaudeが要求する外部操作に対するPermission Enforcement Pointとして動作する。

OS固有処理はAdapter層に閉じ込め、MaatgenのPolicy、Audit、Agent管理部分はWindows/Linux共通実装とする。

Claude Code CLIはSDK/APIで不足する機能のFallbackとしてのみ利用し、必要な場合はLinux PTYとWindows ConPTYをTerminalAdapterで抽象化する。
