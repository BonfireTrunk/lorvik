# Licensing

Lorvik is open source under two licenses, chosen per component.

| Component | Paths | License |
|---|---|---|
| Server | `cmd/lorvikd/`, `internal/server/`, `internal/lorvikd/`, `internal/store/`, domain packages under `internal/`, `web/`, `deploy/` | AGPL-3.0-only (`LICENSE`) |
| CLI and agent skill | `cmd/lorvik/`, `internal/cli/`, `internal/client/`, `internal/apitypes/`, `skills/` | Apache-2.0 (`LICENSE-APACHE`) |

Directories under the Apache-2.0 license carry their own `LICENSE` file. Anything not listed is AGPL-3.0-only.

Why the split: the server is AGPL so that anyone who runs a modified Lorvik service for others must share their changes. The CLI and skill are Apache-2.0 so they can be installed into any agent environment without license friction.

Contributions are made under the [BonfireTrunk Contributor Agreement v1](https://github.com/BonfireTrunk/.github/blob/cla-v1/CLA.md), shared by every public BonfireTrunk repository. Accept it by ticking its box in the pull request description; the `cla` check on each pull request stays red until the box is ticked.

SPDX identifiers: `AGPL-3.0-only` and `Apache-2.0`.
