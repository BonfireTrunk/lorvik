# 0002: Server plus Postgres, never local-only files

Status: accepted, 2026-10-18

## Context

The owner runs agents on several machines at once. A per-repo file store cannot coordinate claims between them, and a hosted SaaS is the planned destination.

## Decision

Lorvik state lives in Postgres behind the `lorvikd` HTTP API. The only local state is `.lorvik.toml` binding a checkout to a project. Postgres only: no SQLite, no embedded database.

## Consequences

Claims, leases and optimistic versioning become possible. Self-hosters must run Postgres, so a compose file is provided. Offline use is not supported. Supporting a second engine would double every query and migration, so it is out of scope.
