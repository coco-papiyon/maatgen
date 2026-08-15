# Maatgen Agent Instructions

## Product direction

- The initial product targets Codex CLI only. Do not add Claude Code or GitHub Copilot behavior until the Codex implementation is complete.
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

- Keep Codex-specific process and JSONL behavior inside the Codex adapter.
- Keep the Extension thin; repository mutation and checkpoint logic belong in Agent Manager.
- When modifying frontend behavior or presentation, update and verify both the Web version and the VS Code version. Keep their user-facing behavior and shared Session／Run／Usage／ChangeSet concepts aligned, adapting only the surface-specific layout or interaction as needed.
- Update `docs/coding-agent-design.md` and `docs/implementation-plan.md` when implementation details change the design.
- Prefer additive tests for checkpoint creation, direct repository execution, restore conflict detection, and same-Session resume.

## Verification

- Run the relevant TypeScript tests/typecheck and Go tests after changes.
- Do not use destructive Git commands against the user's repository unless the task explicitly requires them.
