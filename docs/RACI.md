# Team ownership

This document names who is Responsible, Accountable, Consulted, and Informed (RACI) for product, code, review, operations, and launch on Thyme & Place.

## Roles

The standing roles are Wade (owner), Farmer Manager, PM Farmer, Architect Farmer, Front-end Farmer, UX Review Farmer, DevOps Farmer, LiveOps Farmer, and QA Farmer.

A feature farmer owns one epic. Almanac Farmer is the first feature farmer and owns epic #74. A later feature farmer follows the Feature X row for that epic.

The matrix uses these short names.

| Role | Short name |
|---|---|
| Wade | Wade |
| Farmer Manager | Mgr |
| PM Farmer | PM |
| Architect Farmer | Arch |
| Front-end Farmer | Front-end |
| UX Review Farmer | UX |
| DevOps Farmer | DevOps |
| LiveOps Farmer | LiveOps |
| QA Farmer | QA |
| Feature farmer for the epic | Feature X Farmer |

## Legend

| Letter | Meaning |
|---|---|
| R | Responsible. Does the work. |
| A | Accountable. Signs off. One person per row. |
| C | Consulted. Gives input before the work finishes. |
| I | Informed. Receives the outcome. |

## Matrix

| Activity | R | A | C | I |
|---|---|---|---|---|
| Backlog, issues, priorities, and epics | PM | PM | Arch, UX, QA | Wade, Mgr |
| Architecture, design, and security review (CSP, passkeys, secrets) | Arch | Arch | Mgr, PM, DevOps, LiveOps | Wade |
| Coordination and delegation | Mgr | Mgr | PM | Wade |
| Backend code | coding agents, through Mgr | Mgr | Arch | PM |
| Project-level front-end (shared components, front-end e2e specs, CSP lint tests) | Front-end | Mgr | Arch, UX, QA | PM |
| UX, accessibility, and visual checks (can block sign-off) | UX | UX | Front-end, PM, QA | Mgr |
| Manual screen-reader and device pass | Wade or a human tester | UX | Front-end | Mgr |
| CI, images, build and release tooling, supply chain, and test gates in CI | DevOps | DevOps | LiveOps, Arch | Mgr |
| Production operations (deploys, backups, drills, monitoring, rollback, incidents, and capacity) | LiveOps | Wade | DevOps, Arch | Mgr |
| Accounts, DNS, and secrets | Wade | Wade | LiveOps | Mgr |
| QA (test plans, e2e coverage map, and release check against the launch gate) | QA | QA | UX, Front-end, DevOps, LiveOps, PM | Mgr |
| Feature X (epic, design, code, e2e, and sign-off) | Feature X Farmer | Feature X Farmer | Arch at the required kickoff design review. PM, UX, QA, Front-end, DevOps, and LiveOps as needed | Mgr, Wade |
| Merges and launch | Feature farmers open pull requests. Mgr requests the merge. | Wade | All roles | All roles |

UX Review Farmer writes the script for the manual screen-reader and device pass.

## Gates

- Architect Farmer reviews every pull request that touches migrations, package boundaries, or security headers.
- QA Farmer signs off the release before launch. The launch gate is issue #54.
- Wade approves every merge.
