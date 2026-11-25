# 0008: Stories and decisions are card kinds; one global project per org

Status: accepted, 2026-10-31

## Context

Decisions lived in their own table with their own numbers, a status lifecycle and links, and a proposed project decision was put to a reviewer through a mirrored question card. The mirror needed two-way text sync and refusals on the card to behave like one record. Org-wide decisions and notes were rows with a null project id, so every list, access check and reference resolver branched on it. Work larger than a card had no structure: a parent could be any card at any depth, nothing said when a group of cards was finished, and what a piece of work changed in the contract was never written down at the moment it closed.

## Decision

- A decision is a card of kind `decision`. Proposed is ready, accepted is done (the acceptor is recorded), rejected, revoked and superseded are dropped with that reason. The `decisions` and `decision_links` tables and the proposal-card mirror are removed. Supersession and "applies to" are card link kinds.
- A story is a card kind. It is the only kind that may be a parent, there are two levels, and the server enforces them. A story closes only when every child is done or dropped and the request carries a fold: the requirements document the result went into, or none. The fold is stamped on the story and its children. A closed story is closed: no new children, no cards moving in or out, no reopening. Dropping a story with open children needs an explicit cascade. A done card with no parent is folded on its own and listed until then.
- Every org has exactly one global project, created with the org, readable by any member of any project in the org and writable at member or above. Org-wide decisions, notes and cards live there. `project_id` is NOT NULL everywhere; cards link across projects only through the global project.

The behaviour is specified in `docs/requirements/board.md` and `docs/requirements/decisions.md`.

## Consequences

One numbering, one link model, one queue and one event vocabulary cover tasks, questions, stories and decisions; the `decision.*` events are gone. A decision's wording (proposed, accepted and so on) is derived from status and drop reason by each client. Closing a story is a deliberate act with a documentation step attached, so the requirements documents are updated by the people who did the work. Moving a story's cards after it closes, or reopening it, is refused; fix-ups are new stories. Existing databases are dropped and recreated, since the schema was edited in place before any production deployment. Project-only agents can read the global project but not write to it. Stories claimed by mistake, cascade drops as duplicate and a few dashboard gaps are listed as known defects in the requirements documents.

## Alternatives rejected

- Keep the decisions table and the proposal-card mirror. The mirror was the seam: text sync, frozen-card refusals and a second number for one thing.
- Nullable project ids for org-wide items. Every scope rule would carry a null branch, and org-wide tasks and questions would still be impossible.
- Implicit stories, inferred from any card that has children. The server could not enforce closing, and clients could not tell a story from a task that happened to get a child.
- Folding on a timer or a sweep. It turns a judgment about the contract into a chore, and a done story could exist with no fold.
