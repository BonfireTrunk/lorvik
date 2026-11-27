# 0004: AGPL server, Apache-2.0 CLI and skill, CLA required

Status: accepted, 2026-10-18; agreement amended 2026-11-09

## Context

The project is open source and the owner also runs a hosted SaaS. A permissive license on the server would let a cloud vendor host a competing service with private changes. A copyleft license on the CLI and skill would add friction to installing them in agent sandboxes. Without a CLA the owner could never relicense contributions.

## Decision

Server-side code is AGPL-3.0-only. `cmd/lorvik`, `internal/cli`, `internal/client`, `internal/apitypes` and `skills/` are Apache-2.0. Every contributor accepts a contributor agreement. Until 2026-11-09 that was an Apache ICLA style agreement in Lorvik's own `CLA.md`; since the move to the BonfireTrunk org it is the [BonfireTrunk Contributor Agreement v1](https://github.com/BonfireTrunk/.github/blob/cla-v1/CLA.md), shared by every public org repository and enforced by the org's `cla` check on each pull request. The org agreement keeps the right to relicense and is wider than the old text: contributions may be licensed on any terms, proprietary included, with no promise of a particular licence, and it is accepted per pull request, not once per contributor.

## Consequences

Hosted competitors must publish server changes. Agents install the CLI under a permissive license. Future relicensing of new versions stays possible; already published versions remain AGPL forever and may be forked. Some contributors decline to sign CLAs.
