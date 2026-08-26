---
name: resolve-github-pr-conflict
description: PR番号を引数に取り、対象PRのbaseブランチとのマージコンフリクトを検知・解消し、commit・pushして対応内容をPRコメントへ投稿する。コンフリクトが存在しない場合やコード上の意味の異なる変更が競合していて安全に自動解消できない場合は、変更を加えず状況を報告する。初回実装やレビュー指摘の修正には使用しない。
---

このスキルの正本は [../../../.claude/skills/resolve-github-pr-conflict/SKILL.md](../../../.claude/skills/resolve-github-pr-conflict/SKILL.md) にある。正本を最初から最後まで読み、その指示に従う。PR番号を引数として扱い、引数がない場合は作業を開始しない。
