-- name: CreateOrg :one
INSERT INTO orgs (id, name, status, settings, created_at, updated_at)
VALUES (@id, @name, @status, @settings, @created_at, @created_at)
RETURNING *;

-- name: GetOrg :one
SELECT * FROM orgs WHERE id = @id;

-- name: UpdateOrg :one
UPDATE orgs SET name = @name, status = @status, settings = @settings, updated_at = @updated_at
WHERE id = @id
RETURNING *;

-- name: CreateUser :one
INSERT INTO users (id, login, name, email, avatar_url, locale, timezone, status, agent_join, created_at, updated_at)
VALUES (@id, @login, @name, @email, @avatar_url, @locale, @timezone, @status, @agent_join, @created_at, @created_at)
RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = @id;

-- name: LockUser :one
-- FOR UPDATE serializes deleting an account with what writes the user back
-- or hangs a sign-in on it (GitHub sign-in and link).
SELECT * FROM users WHERE id = @id FOR UPDATE;

-- name: GetUsers :many
SELECT * FROM users WHERE id = ANY(@ids::bigint[]);

-- name: UpdateUser :one
UPDATE users
SET login = @login, name = @name, email = @email, avatar_url = @avatar_url,
    locale = @locale, timezone = @timezone, status = @status, agent_join = @agent_join, updated_at = @updated_at
WHERE id = @id
RETURNING *;

-- name: CreateIdentity :one
INSERT INTO identities (id, user_id, provider, provider_id, secret, login, last_login_at, created_at)
VALUES (@id, @user_id, @provider, @provider_id, @secret, @login, @last_login_at, @created_at)
RETURNING *;

-- name: GetIdentity :one
SELECT * FROM identities WHERE provider = @provider AND provider_id = @provider_id;

-- name: ListIdentities :many
SELECT * FROM identities WHERE user_id = @user_id ORDER BY created_at, id;

-- name: UpdateIdentity :exec
UPDATE identities SET secret = @secret, login = @login, last_login_at = @last_login_at
WHERE id = @id;

-- name: DeleteIdentity :execrows
DELETE FROM identities WHERE id = @id AND user_id = @user_id;

-- name: PutMembership :one
INSERT INTO memberships (org_id, actor_kind, actor_id, role, status, rights, granted_by_user_id, created_at, updated_at)
VALUES (@org_id, @actor_kind, @actor_id, @role, @status, @rights, @granted_by_user_id, @created_at, @updated_at)
ON CONFLICT (org_id, actor_kind, actor_id) DO UPDATE
    SET role = EXCLUDED.role, status = EXCLUDED.status, rights = EXCLUDED.rights, granted_by_user_id = EXCLUDED.granted_by_user_id,
        updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: GetMembership :one
SELECT * FROM memberships
WHERE org_id = @org_id AND actor_kind = @actor_kind AND actor_id = @actor_id;

-- name: LockMembership :one
-- FOR UPDATE serializes the decisions that read a membership and write it
-- back (sign-in, invite, grant, removal) inside a transaction.
SELECT * FROM memberships
WHERE org_id = @org_id AND actor_kind = @actor_kind AND actor_id = @actor_id FOR UPDATE;

-- name: LockOrgOwners :many
-- The org's active owners, people only and only those whose account is
-- active, each membership row locked in id order, so two owners leaving at
-- once see each other's leave and one of them is refused.
SELECT m.* FROM memberships m
JOIN users u ON u.id = m.actor_id
WHERE m.org_id = @org_id AND m.actor_kind = 1 AND m.role = @role AND m.status > 0 AND u.status > 0
ORDER BY m.actor_id FOR UPDATE OF m;

-- name: CountOrgPeople :one
-- The org's people with an active membership on an active account.
SELECT count(*) FROM memberships m
JOIN users u ON u.id = m.actor_id
WHERE m.org_id = @org_id AND m.actor_kind = 1 AND m.status > 0 AND u.status > 0;

-- name: CountOrgContents :one
-- What closing the org takes out of reach: its projects, not counting the
-- global one every org has, and the cards of all of them.
SELECT (SELECT count(*) FROM projects p WHERE p.org_id = @org_id AND p.scope <> @global_scope)::bigint AS projects,
       (SELECT count(*) FROM cards c WHERE c.project_id IN (SELECT p.id FROM projects p WHERE p.org_id = @org_id))::bigint AS cards;

-- name: ListMembershipsForActor :many
SELECT * FROM memberships
WHERE actor_kind = @actor_kind AND actor_id = @actor_id AND status > 0
ORDER BY created_at, org_id;

-- name: ListOpenMembershipsForActor :many
-- Alive rows and the pending ones: invitations and requests to join.
SELECT * FROM memberships
WHERE actor_kind = @actor_kind AND actor_id = @actor_id AND status >= 0
ORDER BY created_at, org_id;

-- name: PutProjectMember :one
INSERT INTO project_members (project_id, actor_kind, actor_id, role, status, rights, granted_by_user_id, created_at, updated_at)
VALUES (@project_id, @actor_kind, @actor_id, @role, @status, @rights, @granted_by_user_id, @created_at, @updated_at)
ON CONFLICT (project_id, actor_kind, actor_id) DO UPDATE
    SET role = EXCLUDED.role, status = EXCLUDED.status, rights = EXCLUDED.rights, granted_by_user_id = EXCLUDED.granted_by_user_id,
        updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: GetProjectMember :one
SELECT * FROM project_members
WHERE project_id = @project_id AND actor_kind = @actor_kind AND actor_id = @actor_id;

-- name: ListProjectMembersForActor :many
SELECT pm.* FROM project_members pm
JOIN projects p ON p.id = pm.project_id
WHERE pm.actor_kind = @actor_kind AND pm.actor_id = @actor_id AND pm.status > 0
  AND p.org_id = @org_id
ORDER BY pm.project_id;

-- name: HasProjectMembership :one
SELECT EXISTS (
    SELECT 1 FROM project_members pm
    JOIN projects p ON p.id = pm.project_id
    WHERE pm.actor_kind = @actor_kind AND pm.actor_id = @actor_id AND pm.status > 0
      AND p.org_id = @org_id
);

-- name: CreateAgent :one
INSERT INTO agents (id, org_id, name, harness, machine, created_by_user_id, status, agent_join, created_at, updated_at)
VALUES (@id, @org_id, @name, @harness, @machine, @created_by_user_id, @status, @agent_join, @created_at, @created_at)
RETURNING *;

-- name: GetAgent :one
SELECT * FROM agents WHERE id = @id;

-- name: LockAgent :one
-- FOR UPDATE serializes the decisions that read an agent's status and write
-- it back (approve, revoke, restore, remove) or act on it (minting a token).
SELECT * FROM agents WHERE id = @id FOR UPDATE;

-- name: GetAgents :many
SELECT * FROM agents WHERE id = ANY(@ids::bigint[]);

-- name: ListAgents :many
SELECT * FROM agents
WHERE org_id = @org_id AND (@include_inactive::boolean OR status >= 0)
ORDER BY status DESC, created_at, id;

-- name: UpdateAgent :one
UPDATE agents SET name = @name, harness = @harness, machine = @machine, status = @status, agent_join = @agent_join,
    updated_at = @updated_at
WHERE id = @id
RETURNING *;

-- name: CreateToken :one
INSERT INTO tokens (id, org_id, actor_kind, actor_id, name, prefix, hash, scope, status, expires_at, created_at)
VALUES (@id, @org_id, @actor_kind, @actor_id, @name, @prefix, @hash, @scope, @status, @expires_at, @created_at)
RETURNING *;

-- name: GetTokenByHash :one
SELECT * FROM tokens WHERE hash = @hash;

-- name: GetToken :one
SELECT * FROM tokens WHERE id = @id;

-- name: LockToken :one
-- FOR UPDATE serializes rotation, revocation and reuse handling of one token,
-- so none of them writes over another's decision from a stale read.
SELECT * FROM tokens WHERE id = @id FOR UPDATE;

-- name: UpdateToken :exec
UPDATE tokens SET status = @status, expires_at = @expires_at, replaced_by = @replaced_by
WHERE id = @id;

-- name: TouchToken :exec
UPDATE tokens SET last_used_at = sqlc.arg(at)::timestamptz
WHERE id = sqlc.arg(id)
  AND (last_used_at IS NULL OR last_used_at < sqlc.arg(at)::timestamptz - make_interval(mins => 1));

-- name: RevokeTokensForActor :execrows
UPDATE tokens SET status = @status
WHERE org_id = @org_id AND actor_id = @actor_id AND actor_kind = @actor_kind AND status > 0;

-- name: ListOrgs :many
SELECT * FROM orgs ORDER BY id;

-- name: GetAgentJoinRule :one
SELECT * FROM agent_join_rules WHERE project_id = @project_id AND user_id = @user_id;

-- name: PutAgentJoinRule :one
INSERT INTO agent_join_rules (project_id, user_id, agent_join, updated_at)
VALUES (@project_id, @user_id, @agent_join, @updated_at)
ON CONFLICT (project_id, user_id) DO UPDATE
    SET agent_join = EXCLUDED.agent_join, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: DeleteAgentJoinRule :exec
DELETE FROM agent_join_rules WHERE project_id = @project_id AND user_id = @user_id;

-- name: ListAgentRequests :many
-- An agent's request to join a project is a pending row for the agent,
-- decided by its owner, the row's granted_by_user_id.
SELECT pm.* FROM project_members pm
JOIN projects p ON p.id = pm.project_id
WHERE pm.status = 0 AND pm.actor_kind = 2 AND pm.granted_by_user_id = @owner_id::bigint AND p.org_id = @org_id
ORDER BY pm.updated_at, pm.project_id, pm.actor_id;

-- name: TouchAgentSession :exec
-- Records that a chat used the agent's token. A row seen again within the
-- window is left alone, so a busy session does not write on every request.
-- A chat first seen here has no name yet.
INSERT INTO agent_sessions (agent_id, session, name, created_at, last_seen_at)
VALUES (@agent_id, @session, NULL, @now, @now)
ON CONFLICT (agent_id, session) DO UPDATE SET last_seen_at = EXCLUDED.last_seen_at
WHERE agent_sessions.last_seen_at < @stale_before;

-- name: TrimAgentSessions :exec
-- Keeps the agent's most recently seen chats, keep_rows of them, and every
-- chat that holds a live grant (status > 0, which also lets the probe use
-- agent_session_grants_agent_idx), so an agent token that sends ever new
-- labels cannot grow the table without bound and no duty is lost.
DELETE FROM agent_sessions s
WHERE s.agent_id = @agent_id
  AND s.session IN (
    SELECT k.session FROM agent_sessions k WHERE k.agent_id = @agent_id
    ORDER BY k.last_seen_at DESC, k.session
    OFFSET @keep_rows)
  AND NOT EXISTS (SELECT 1 FROM agent_session_grants g WHERE g.agent_id = s.agent_id AND g.session = s.session AND g.status > 0);

-- name: SetAgentSessionName :exec
-- Names a chat, creating its row when the chat was never seen; a null name
-- takes the name away. A name equal to the stored one writes nothing. The
-- comparison is IS DISTINCT FROM because either side may be null, and <>
-- against a null is never true.
INSERT INTO agent_sessions (agent_id, session, name, created_at, last_seen_at)
VALUES (@agent_id, @session, sqlc.narg('name'), @now, @now)
ON CONFLICT (agent_id, session) DO UPDATE SET name = EXCLUDED.name
WHERE agent_sessions.name IS DISTINCT FROM EXCLUDED.name;

-- name: ListAgentSessions :many
SELECT * FROM agent_sessions WHERE agent_id = @agent_id
ORDER BY last_seen_at DESC, session
LIMIT @row_limit;

-- name: ListAgentSessionsByLabel :many
SELECT * FROM agent_sessions WHERE agent_id = @agent_id AND session = ANY(@sessions::text[])
ORDER BY last_seen_at DESC, session;

-- name: PutAgentSessionGrant :one
INSERT INTO agent_session_grants (project_id, agent_id, session, rights, granted_by_user_id, ends_at, instructions, status, created_at, updated_at)
VALUES (@project_id, @agent_id, @session, @rights, @granted_by_user_id, @ends_at, @instructions, @status, @created_at, @updated_at)
ON CONFLICT (project_id, agent_id, session) DO UPDATE
    SET rights = EXCLUDED.rights, granted_by_user_id = EXCLUDED.granted_by_user_id, ends_at = EXCLUDED.ends_at,
        instructions = EXCLUDED.instructions, status = EXCLUDED.status, updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: GetAgentSessionGrant :one
SELECT * FROM agent_session_grants
WHERE project_id = @project_id AND agent_id = @agent_id AND session = @session;

-- name: ListAgentSessionGrants :many
SELECT * FROM agent_session_grants
WHERE agent_id = @agent_id AND status > 0
ORDER BY project_id, session;

-- name: EndAgentSessionGrant :one
-- Takes back a grant only while it is still alive and past its end, so one
-- extended or taken back meanwhile is left alone.
UPDATE agent_session_grants SET status = @status, updated_at = @updated_at
WHERE project_id = @project_id AND agent_id = @agent_id AND session = @session
  AND status > 0 AND ends_at <= @before::timestamptz
RETURNING *;

-- name: ListEndedAgentSessionGrants :many
-- Grants past their end whose row was not ended yet.
SELECT * FROM agent_session_grants
WHERE status > 0 AND ends_at <= @before::timestamptz
ORDER BY ends_at LIMIT @max_rows;
