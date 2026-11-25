# 0006: Decisions are a first-class entity; notes instead of a wiki

Status: accepted, 2026-10-18

## Context

The first idea was a per-project wiki. Code documentation already lives in repos as `docs/` and `AGENTS.md`, reviewed in pull requests, and a server-side wiki would split the source of truth. What agents actually lack across sessions is why choices were made and where the last session stopped.

## Decision

Decisions are their own table in ADR shape (context, decision, consequences) with a status lifecycle, a supersede chain, links to cards, and promotion from an answered question card. Notes exist at org level, project level, and as comments attached to cards. There is no wiki; in-repo `docs/` remains the home for code documentation.

## Consequences

The decisions log writes itself from answered questions. Comments survive sessions and machines. Cross-repo knowledge has a home at org level. A `notes export` to git can come later if wanted.

## Amendments

- 2026-10-31: decisions are no longer their own table. They are cards of kind `decision`, and org-wide decisions and notes live in the global project. See `0008-stories-decisions-as-cards-global-project.md` and `docs/requirements/decisions.md`. The reasoning above about a first-class decision log, notes instead of a wiki and in-repo `docs/` still holds.
