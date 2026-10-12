# Vendored pstack skills

Cloud agents on this repo can't invoke plugin skills (only `/setup-pstack` is registered there). So the skills an agent network needs are checked in under `.cursor/skills/`, and the two subagent definitions under `.cursor/agents/`. Every agent that clones the repo gets them.

## Source

- pstack: <https://github.com/cursor/plugins/tree/main/pstack> (plugin version 0.15.20, by Lauren Tan)
- cursor-team-kit: <https://github.com/cursor/plugins/tree/main/cursor-team-kit>, for the three skills pstack names as dependencies

Source commit: `1b6d44ff26d4e242f4e8f68c391e6d2f7ca16def`

## License

Both plugins are MIT licensed and allow vendoring if the notice travels with the copies. The notices are kept here as `PSTACK_LICENSE` (Copyright (c) 2026 Lauren Tan) and `CURSOR_TEAM_KIT_LICENSE` (Copyright (c) 2026 Cursor). `LICENSE.md` at the repo root lists them under third-party code. They are developer tooling and are not built into or shipped with the app.

## What is included

pstack skills (19), copied whole, including `references/`, `scripts/` and `playbooks/`:

`poteto-mode` (with its 23 playbooks, `references/bugbot-triage.md` and the `scripts/` for `watch-pr`, `orch`, `check-plan` and `worktree-audit`), `architect`, `arena`, `benchmark-checklist`, `blast-radius`, `figure-it-out`, `how`, `why`, `interrogate`, `no-comments`, `reflect`, `swarm`, `technical-writing`, `unslop`, `correct`, `tdd`, `create-verification-skill`, `maintain-verification-skill`, `show-me-your-work`.

pstack principle skills (24), all of the ones `poteto-mode` indexes: `principle-attack-the-premise`, `principle-boundary-discipline`, `principle-build-the-lever`, `principle-encode-lessons-in-structure`, `principle-exhaust-the-design-space`, `principle-experience-first`, `principle-explain-the-number`, `principle-fix-root-causes`, `principle-foundational-thinking`, `principle-guard-the-context-window`, `principle-laziness-protocol`, `principle-make-operations-idempotent`, `principle-migrate-callers-then-delete-legacy-apis`, `principle-minimize-reader-load`, `principle-model-the-domain`, `principle-never-block-on-the-human`, `principle-outcome-oriented-execution`, `principle-prove-it-works`, `principle-redesign-from-first-principles`, `principle-separate-before-serializing-shared-state`, `principle-sequence-verifiable-units`, `principle-subtract-before-you-add`, `principle-test-behavior-not-implementation`, `principle-type-system-discipline`.

cursor-team-kit skills (3): `deslop` (run before every commit), `control-ui` and `control-cli` (drive the real UI or CLI to verify).

Subagents in `.cursor/agents/`: `poteto-agent` (the delegate `poteto-mode` spawns for every playbook step) and `comment-sicko` (spawned by `no-comments`).

The set is the transitive closure of the skill names and relative links reachable from `poteto-mode`, `create-verification-skill`, `maintain-verification-skill`, `show-me-your-work`, `correct`, `unslop`, `tdd` and `interrogate`. Upstream's `SKILL.md` frontmatter is preserved unchanged.

## Not included, on purpose

- `setup-pstack`. It writes a per-user rule under `~/.cursor/`, and the platform already registers it.
- `create-skill`, `/loop`, and other Cursor built-ins that pstack mentions.
- Other pstack skills nothing above references (`bro`, `teach`, `recall`, `ketchup`, `poteto-help`, `automate-me`, `make-bot-ui`, `typescript-best-practices`) and the pstack `docs/` and `automations/`.
- The other cursor-team-kit skills.

## Local changes from upstream

Paths only. `scripts/sync-pstack.sh` reapplies both fixes and fails if any `pstack/skills/` path survives.

- Upstream paths assume pstack sits at a repo root (`pstack/skills/<name>/...`). They now read `.cursor/skills/<name>/...`. This touches `poteto-mode/playbooks/multi-phase-plan.md`, `autopilot-full.md` and `autopilot-stack.md`.
- Playbooks name helper scripts relative to the skill (`scripts/watch-pr/watch-pr`, `scripts/orch/orch.ts`, `scripts/worktree-audit.sh`). An agent running from the repo root would hit this repo's own `scripts/` instead, so they now read `.cursor/skills/poteto-mode/scripts/...`. This touches `babysit.md`, `shipping.md`, `orchestrate.md` and `worktree-cleanup.md`.
 
Skill-to-skill links are relative (`../principle-...`) and need no change because the flat layout matches upstream's.

## Model configuration without `~/.cursor/rules/pstack-models.mdc`

`/setup-pstack` writes per-role model choices to `~/.cursor/rules/pstack-models.mdc`. That user-level file doesn't exist on a cloud VM. Each skill that reads it (`poteto-mode`, `how`, `why`, `arena`, `architect`, `swarm`, `interrogate`, `reflect`) already says what to do when the rule or a role line is missing, so agents use these defaults:

- Code delegates: `grok-4.7-xhigh-fast`. The hardest changes, prose and judgment: `claude-opus-5-5-xhigh`.
- `swarm` workers: `grok-4.7-xhigh-fast`. `arena` runners, cross-judge pool and `interrogate` reviewers: one each of `claude-opus-5-5-xhigh` and `grok-4.7-xhigh-fast`.
- A role line of `auto` or `inherit-parent` runs on the parent chat's model (omit `model` on the `Task` call).

If a model in these defaults isn't available to the agent, the agent should use the closest available one and say so. To pin choices for every agent, commit a project rule at `.cursor/rules/pstack-models.mdc` with the same role lines. The skills read it by name, so the project copy works the same as the user one. Run `/setup-pstack` once locally and copy the file it writes.

## Runtime needs

- `node` runs `poteto-mode/scripts/check-plan.mjs`.
- `bun` runs the `watch-pr` and `orch` scripts and their tests. It isn't installed on the cloud image by default. Without it, `watch-pr` and `orch` can't run, so use the `gh` commands the playbooks name. The scripts install `commander` into `poteto-mode/scripts/node_modules/` on first run, which `.gitignore` excludes.

## Updating

```bash
scripts/sync-pstack.sh                # latest main
scripts/sync-pstack.sh <commit-sha>   # pin a commit
```

The script clones <https://github.com/cursor/plugins>, replaces each vendored folder wholesale, refreshes the two license files and the agents, reapplies the path fix and rewrites the `Source commit` line above. Review `git diff --stat`, commit, and open a PR. To add or drop a skill, edit the arrays at the top of the script and the lists above in the same PR. Don't hand-edit vendored files, because the next sync overwrites them. Fix upstream or add the change to the script.
