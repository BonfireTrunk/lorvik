# Requirements

One document per feature area. Together they must be enough to rebuild Lorvik as it is today without reading the history. They describe the current contract; the history stays in the board, the decision records and git.

## What goes where

- A requirements document holds what the system does now and the few choices someone might undo by mistake.
- A decision record (on the board, `lorvik decisions`) holds a disputed choice with its context and the rejected alternative. The requirements document carries only the choice's current implications and a one-line reason.
- Cards hold work; comments hold progress. Neither is documentation.
- Notes hold runbooks and gotchas: knowledge with a purpose of its own that no requirements document covers.

## Sections, in this order

1. **Purpose**: two or three lines.
2. **Behaviour**: flows, states and transitions, edge cases, refusals with their HTTP status and error code, limits and defaults.
3. **Rules**: permissions, data scoping, audit, locking and ordering, idempotency, where personal data may go.
4. **Data**: the tables involved and the meaning of each code value and short JSON key. Explain them; do not copy the DDL, which lives in `internal/store/migrations/`.
5. **API**: method, path, permission, the shape of the body and the answer, special answers, and which screens and CLI commands use it.
6. **Jobs**: what triggers each background job, what it does, and why running it twice is safe. "None" when the area has none.
7. **Why it is this way**: only choices someone might undo by mistake, each with the rejected alternative and the concrete reason, one to three lines. Link the decision record when one exists.

## Rules for writing them

- Label three things separately and never mix them: implemented behaviour, intended changes not yet built, known defects. Intended changes name their card.
- Cross-cutting rules such as authorization, versioning and error format have one home. Other documents point to it; they do not restate it.
- Reference the authoritative source for shapes: the migration for columns, `internal/apitypes` for wire types, `docs/api.md` for the route table. Explain semantics here.
- Write for a reader who has the code and the tests but no history. Every sentence should be checkable against one of them.
- Keep it current: a card that changes behaviour updates the document before it is closed, and the `done` summary names the document. A card that changes no contract says so.
