# Team ownership

This document names who is Responsible, Accountable, Consulted, and Informed (RACI) for product, code, review, operations, and launch on Thyme & Place.

## Roles

The standing roles are Wade (owner), PM Farmer, Architect Farmer, UX Review Farmer, DevOps Farmer, LiveOps Farmer, QA Farmer, and one feature farmer per epic. Almanac Farmer is the first feature farmer and owns epic #74. A later feature farmer follows the Feature X row for its epic.

Feature farmers own their work full stack, including e2e tests. There is no separate front-end or manager role.

## How a feature moves

1. Wade proposes a feature in the Farmer Leads room.
2. Architect Farmer shapes the technical solution, including limits and growth.
3. PM Farmer breaks it into tickets for the feature farmer.
4. The feature farmer builds it, full stack and e2e.
5. PM, Architect, and QA share the definition of done and sign off the pull request.
6. Wade merges.

| Role | Short name |
|---|---|
| Wade | Wade |
| PM Farmer | PM |
| Architect Farmer | Arch |
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
| Feature proposals | Wade | Wade | Arch, PM, QA | All roles |
| Technical shaping (limits, growth, data model) | Arch | Arch | PM, QA, DevOps, LiveOps | Wade |
| Tickets, backlog, priorities, and epics | PM | PM | Arch, QA, UX | Wade |
| Product calls while Wade is away | PM | PM | Arch, QA | Wade |
| Architecture and security review (CSP, passkeys, secrets, migrations) | Arch | Arch | PM, DevOps, LiveOps | Wade |
| Feature X build (code, front end, e2e) | Feature X Farmer | Feature X Farmer | Arch, UX, QA | PM, Wade |
| Keeping own pull requests mergeable (rebase, green CI, re-run governance after sign-offs, retarget stacked PRs) | Feature X Farmer | Feature X Farmer | DevOps | PM |
| Definition of done and pull request sign-off | PM, Arch, QA | Wade | UX | All roles |
| UX, accessibility, and visual checks (can block sign-off) | UX | UX | Feature X Farmer, PM, QA | Wade |
| Manual screen-reader and device pass | Wade or a human tester | UX | Feature X Farmer | PM |
| CI/CD, build and release tooling, supply chain, Dependabot, and janitorial work | DevOps | DevOps | Arch, LiveOps | PM |
| Production monitoring, alerting, and first triage | LiveOps | LiveOps | DevOps, Arch | Wade, PM |
| Production deploys, backups, rollback, incidents, and capacity | LiveOps | Wade | DevOps, Arch | PM |
| Accounts, DNS, and secrets | Wade | Wade | LiveOps | PM |
| QA (test plans, e2e coverage map, release check against the launch gate) | QA | QA | UX, Feature X Farmer, DevOps, LiveOps, PM | Wade |
| Merges | Wade. DevOps for docs-only PRs and Dependabot patch or minor bumps after full sign-off and green CI. | Wade | PM, Arch, QA | All roles |
| Launch | LiveOps | Wade | All roles | All roles |

UX Review Farmer writes the script for the manual screen-reader and device pass.

## Gates

- Architect Farmer reviews every pull request that touches migrations, package boundaries, cross-module contracts, or security headers.
- QA Farmer signs off the release before launch. The launch gate is issue #54.
- Wade approves every merge except the docs-only and Dependabot patch or minor merges delegated to DevOps Farmer. Major bumps, Docker base images, CI workflows, `cmd/prcheck`, code changes, and rule files (AGENTS.md, WAYS-OF-WORKING, `.github/pr-signoffs`) stay with Wade.
