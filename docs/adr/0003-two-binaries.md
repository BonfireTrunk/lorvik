# 0003: Two binaries, lorvik and lorvikd

Status: accepted, 2026-10-18

## Context

Self-hosters like one binary with an embedded UI. Agents install the CLI into many sandboxes and want a small, dependency-free download. The SaaS API image should not carry CLI code.

## Decision

`lorvik` is the CLI: HTTP client only, no database code. `lorvikd` is the server: API, migrations, sweeper, embedded dashboard by default, `-tags noui` for API-only images.

## Consequences

Self-hosters run one server binary plus Postgres. The CLI binary stays around 7 MB and never links pgx. Two GoReleaser builds instead of one.
