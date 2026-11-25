# 0005: huma v2 on the standard library mux for the API

Status: accepted, 2026-10-18

## Context

The dashboard needs a typed TypeScript client and agents benefit from a machine-readable contract. Options: hand-written net/http handlers, a spec-first generator (oapi-codegen), or a code-first framework that emits OpenAPI (huma).

## Decision

Routes are registered with huma v2 through the humago adapter on `net/http.ServeMux`. Request and response types are the `internal/apitypes` structs. huma serves `/api/v1/openapi.json`, validates input, and emits RFC 9457 problem bodies.

## Consequences

One Go definition produces the spec, the validation and the docs. The TypeScript client is generated from the spec in phase 2. huma's struct tags shape the schema, so apitypes carries `doc`, `enum` and limit tags. Replacing huma later means rewriting registration, not the types.
