---
name: fix-github-pr
description: PR番号を引数に取り、必要ならcleanな作業ツリーをPRのheadブランチへ自動切替してレビュー指摘を修正し、対応状況をPRコメントへ投稿してPRに再レビューラベルを設定する。初回実装や読み取り専用レビューには使用しない。
---

このスキルの正本は [../../../.claude/skills/fix-github-pr/SKILL.md](../../../.claude/skills/fix-github-pr/SKILL.md) にある。正本を最初から最後まで読み、その指示に従う。PR番号を引数として扱い、引数がない場合は作業を開始しない。
