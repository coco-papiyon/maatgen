# Maatgen Agent Instructions

## Product direction

- Codex CLI, GitHub Copilot CLI, and Claude Code CLI are all supported providers. Keep each CLI's process arguments, output parsing, and usage accounting inside its own adapter.
- Command approval (ADR-006) is Codex-only. Copilot runs with `--allow-all` and Claude Code with `--permission-mode bypassPermissions`.
- Agent runs modify the user's target repository Working Tree directly. Do not create or use a Git Worktree for the new design.
- Changes are immediately usable; there is no Accept/Reject approval step.
- Before every Run, create a checkpoint of the current Working Tree. After every terminal Run state, create an after snapshot and show the diff.
- Restore is the only change-reversal operation. Restore File, Hunk, or the entire Run to the Run's before checkpoint, with conflict detection that never overwrites later user edits.
- A Session remains active after a Run. The next prompt resumes the same Codex thread from the current Working Tree and creates a new before checkpoint.

## Compatibility policy

- Do not preserve backward compatibility with the former Worktree or Accept/Reject design.
- Do not add legacy modes, compatibility endpoints, dual schema paths, adapters, or migration shims for the old design.
- Replace old Worktree, Review, Accept, and Reject code and data models with the direct Working Tree, Checkpoint, ChangeSet, and Restore design.
- Existing local development data may be recreated or reset as part of this replacement. Do not add read-only legacy-session handling unless explicitly requested.

## Checkpoint rules

- Use Git plumbing with a Manager-owned temporary index and private refs under `refs/maatgen/checkpoints/<sessionId>/<runId>/`.
- Checkpoints include tracked files and untracked non-ignored files, including file mode, symlink, binary, add, delete, and rename state. Ignored files are outside the restore guarantee.
- Creating a checkpoint must not modify the user's index, HEAD, branch, or Working Tree.
- If the current content differs from the recorded after snapshot, return a checkpoint conflict and do not overwrite it.
- Capture after snapshots for completed, failed, cancelled, and timed-out Runs.

## Implementation boundaries

- Keep provider-specific process and JSONL behavior inside that provider's adapter.
- Keep the Extension thin; repository mutation and checkpoint logic belong in Agent Manager.
- When modifying frontend behavior or presentation, update and verify both the Web version and the VS Code version. Keep their user-facing behavior and shared Session／Run／Usage／ChangeSet concepts aligned, adapting only the surface-specific layout or interaction as needed.
- Update `docs/coding-agent-design.md` and `docs/implementation-plan.md` when implementation details change the design.
- Prefer additive tests for checkpoint creation, direct repository execution, restore conflict detection, and same-Session resume.

## Verification

- Run the relevant TypeScript tests/typecheck and Go tests after changes.
- Do not use destructive Git commands against the user's repository unless the task explicitly requires them.

## Pricing and cost updates

- Model pricing is external data. Do not hard-code a new token price in a parser or UI.
- The Agent Manager refreshes pricing at startup from the official sources:
  - OpenAI model comparison: `https://developers.openai.com/api/docs/models/compare`
  - GitHub Copilot model pricing: `https://docs.github.com/en/copilot/reference/copilot-billing/models-and-pricing`
- Claude Code has no pricing source because its CLI prices each Run itself. Store `result.total_cost_usd` as reported and never recalculate it from the rate table. A cost that a provider CLI already reported is authoritative; the rate table is only for providers that report none.
- Retrieved rates are retained in the SQLite `model_pricing` table with provider, model, input/cached-input/cache-write/output rates per one million tokens, source URL, and retrieval time. A failed refresh must not delete the last known rate.
- Codex cost is calculated from recorded token usage and the retained model rate. Cached input tokens are charged at the cached-input rate and subtracted from ordinary input tokens. Copilot cost is calculated from recorded AI credits; one Copilot AI credit is USD 0.01. Do not use Copilot's `assistant.usage.cost` as a currency amount because it is a model multiplier. Claude Code cost comes from the CLI's `result.total_cost_usd`.
- `assistant.usage.model` is the authoritative actual model for Copilot, including when the requested model is `auto`; for CLI JSONL versions that omit `assistant.usage`, retain `assistant.message.data.model` as a fallback. If the CLI only emits `result.usage.premiumRequests`, retain that value as the Copilot usage quantity because no token-level AIU is present in that output format.

### Updating pricing behavior

1. Add or rename models in `apps/agent-manager/config/providers.json` first. Startup refresh only requests rates for configured models.
2. Verify the official source table and update the extractor in `apps/agent-manager/internal/pricing/pricing.go` only when the source markup or model naming changes. Add a fixture test for each new table shape.
3. Keep the source URL and retrieval timestamp in the stored row. Do not overwrite existing rows when a fetch or parse fails.
4. Run the Agent Manager once to refresh rates, or use `go -C apps/agent-manager run ./cmd/agent-manager --data-dir <data-dir> --config config/providers.json --backfill-costs` from the repository root to refresh rates and recalculate historical rows. Copilot rows with AI credits do not require a model; historical Codex rows without a stored model use the provider's current default model, or its first configured model when no default is set. Then run Go tests and Web/VS Code typechecks/tests. Inspect the `model_pricing` rows and a completed Run's `costUsd` before release.
5. If a provider changes the billing unit or currency, update the pricing package, Protocol schema, SQLite migration, both UIs, and `docs/coding-agent-design.md` / `docs/implementation-plan.md` together.
