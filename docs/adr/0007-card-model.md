# 0007: Card model: kinds, statuses, parents, leases

Status: accepted, 2026-10-18

## Context

Agents reason best over a small state machine, humans want nuance in reporting, and two agents on two machines must never work the same card.

## Decision

Cards have kind `task` or `question`. Statuses: backlog, ready, working, blocked, done, dropped. `dropped` requires a reason (obsolete, duplicate, wontfix); duplicates link to the survivor. Parent and child cards are allowed at any depth by the schema; the skill teaches two levels. Claims carry a lease (default 30 minutes, per project) renewed implicitly by any authenticated call from the claimant; expired claims return the card to ready via a sweeper and emit an event. Every write carries the version it read and fails with 409 on mismatch.

## Consequences

Six statuses, two terminal. Reporting breaks dropped cards down by reason. Agents never manage leases by hand. A crashed agent cannot hold a card for more than one lease. Clients must re-read on 409.

## Amendments

- 2026-10-30: the status `blocked` was renamed `waiting` before any release. The current contract is in `docs/requirements/board.md`, `docs/requirements/decisions.md` and `docs/api.md`.
- 2026-10-31: kinds `story` and `decision` were added, with their own transition rules, by [0008](0008-stories-decisions-as-cards-global-project.md). A parent must now be a story.
