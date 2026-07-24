---
name: efficient-subagent-delegation
description: Coordinates separate implementation and review agents while minimizing orchestrator token use. Apply when the current agent is asked to launch or coordinate subagents, delegate work to another agent, use parallel agents, or have one agent review another. Do not apply merely because the current chat model is Grok, GPT, or another model.
disable-model-invocation: true
---

# Efficient Subagent Orchestration

## Goal

Keep the current chat agent as the orchestrator. Move expensive exploration, implementation, review, and verification into user-designated free-model subagents, with one implementation owner per cohesive execution unit or bundle. Keep the orchestrator to coordination and a bounded integration check.

## 1. Decide Whether To Delegate

Implement directly when the task is already understood and normally affects at most five files, one subsystem, and one repository.

Delegate when work is broad, mechanical, multi-repository, exploration-heavy, or explicitly assigned to a free-model agent.

Do not delegate when the paid parent would need to reread most of the implementation to trust it. If uncertain, choose the path expected to use fewer paid-parent tokens.

## 2. Assign One Owner Per Execution Unit

Use the accepted plan's execution units or bundles as delegation boundaries:

- Assign exactly one implementation agent to each unit or bundle.
- Never assign several independent units to one implementation agent merely because they belong to the same request.
- Never split one cohesive bundle across multiple implementation agents.
- Independent units may run in parallel when they cannot edit overlapping files or contend for the same build/runtime resources; otherwise run them sequentially.
- The orchestrator remains responsible for ordering, conflict avoidance, handoffs, reviewer assignment, authorized commits, and final integration status. It does not become the implementation owner.

Each implementation agent owns its assigned unit end to end:

- Relevant rule discovery and codebase exploration.
- Implementation without committing.
- Focused correctness review, including authority, authentication, persistence, concurrency, resource ownership, reconnect behavior, and wire contracts when relevant.
- Formatting, focused tests, and authoritative compilation after its final mutation.
- Preservation of unrelated working-tree changes.

Tell it not to return raw diffs, full logs, secrets, or routine exploration notes.

Require this handoff, limited to roughly 40 lines:

```text
Outcome:
Changed files:
High-risk decisions checked:
Verification:
Unresolved blockers or risks:
```

## 3. Use A Fresh Free Reviewer When Needed

For security-sensitive, cross-repository, protocol, or user-requested review work, launch a fresh free-model reviewer for each completed execution unit. Give it the repository path, that unit's intended behavior, and its changed-file scope.

The reviewer returns only actionable correctness findings with file/line references. It must not restate the implementation or include full diffs.

Do not make the paid parent perform the deep review already assigned to the free reviewer.

## 4. Commit Completed Units

When the user has explicitly authorized commits for the delegated work:

- The orchestrator, never an implementation or review agent, creates the commit.
- Commit each execution unit or bundle immediately after its implementation, review, corrections, and verification are complete.
- Do not begin the next sequential unit until the completed unit is committed, so review, rollback, and cherry-pick boundaries stay aligned with the plan.
- Stage only that unit's files and preserve unrelated working-tree changes.
- Use the unit's proposed commit purpose, adjusted to accurately summarize the verified result.

If the user has not explicitly authorized commits, leave every unit uncommitted and report the proposed commit boundaries.

## 5. Paid-Parent Review Budget

The paid parent may:

1. Read `git status --short` and `git diff --stat` for each repository.
2. Check that unrelated and sensitive assets were preserved.
3. Inspect at most three targeted high-risk hunks selected from the handoff or reviewer findings.
4. Send one batched correction request.
5. Confirm the final concise verification result.

The paid parent must not:

- Dump or read the complete diff when more than five files or 500 changed lines are involved.
- Repeat broad searches performed by a trusted implementation/review agent.
- Resume an agent repeatedly for one small finding at a time.
- Re-run passing authoritative checks without evidence they became stale.

If the paid-parent review budget cannot establish confidence, continue autonomously by assigning the remaining investigation or review to a fresh free-model agent. Do not pause merely because the review budget is exhausted.

## 6. Correction And Stop Conditions

Prefer one correction round containing all known findings. If issues remain, use a fresh free-model agent to diagnose them and continue until verification passes or a definitive technical blocker is established.

After focused review and current verification pass, stop. Do not continue speculative re-review.

## Prompt Template

```text
Own this execution unit end to end: inspect applicable project rules, preserve unrelated changes,

implement without committing, perform focused correctness review, and run final authoritative
verification after the last mutation. Use concise targeted inspection; do not return raw diffs
or logs. Return only: Outcome, Changed files, High-risk decisions checked, Verification,
and Unresolved blockers or risks (about 40 lines maximum).
```
