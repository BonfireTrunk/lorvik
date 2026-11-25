-- name: InsertCard :one
INSERT INTO cards (id, org_id, project_id, number, parent_id, kind, status, drop_reason, priority,
                   created_by_kind, created_by_id, assignee_kind, assignee_id, answer_relayed, for_person,
                   answered_by_kind, answered_by_id, answered_at, closed_at,
                   version, page_bytes, created_at, updated_at, title, body, question_choices, answer, branch, lane)
VALUES (@id, @org_id, @project_id, @number, @parent_id, @kind, @status, @drop_reason, @priority,
        @created_by_kind, @created_by_id, @assignee_kind, @assignee_id, @answer_relayed, @for_person,
        @answered_by_kind, @answered_by_id, @answered_at, @closed_at,
        1, @page_bytes, @created_at, @created_at, @title, @body, @question_choices, @answer, @branch, @lane)
RETURNING *;

-- name: GetCard :one
SELECT * FROM cards WHERE id = @id;

-- name: GetCards :many
SELECT * FROM cards WHERE id = ANY(@ids::bigint[]);

-- name: GetCardByNumber :one
SELECT * FROM cards WHERE project_id = @project_id AND number = @number;

-- name: NextReadyCard :one
-- Walks cards_board_idx for (project, ready) in priority-then-number order and
-- stops at the first task with no claim, no open blocker, no dropped parent and
-- no assignee other than the asking actor, and no working card or live lock in
-- its lane.
SELECT c.* FROM cards c
WHERE c.project_id = @project_id
  AND c.status = @ready_status
  AND c.kind = @task_kind
  AND NOT EXISTS (SELECT 1 FROM claims cl WHERE cl.card_id = c.id)
  AND (c.assignee_id IS NULL OR (c.assignee_kind = @actor_kind AND c.assignee_id = @actor_id))
  AND NOT EXISTS (
      SELECT 1 FROM card_links l
      JOIN cards b ON b.id = l.from_card_id
      WHERE l.to_card_id = c.id AND l.kind = @blocks_kind AND b.status >= 0)
  AND (c.parent_id IS NULL OR NOT EXISTS (
      SELECT 1 FROM cards p WHERE p.id = c.parent_id AND p.status = @dropped_status))
  AND (c.lane IS NULL OR (
      NOT EXISTS (SELECT 1 FROM cards w WHERE w.project_id = c.project_id AND w.status = @working_status AND w.lane = c.lane)
      AND NOT EXISTS (SELECT 1 FROM locks k WHERE k.org_id = c.org_id AND k.name = c.lane AND k.expires_at > sqlc.arg(now)::timestamptz)))
ORDER BY c.priority DESC, c.number
LIMIT 1;

-- name: CountCardsOnBusyLanes :many
-- The same candidates as NextReadyCard, but only those held back by a working
-- card or live lock in their lane, counted per lane.
SELECT c.lane::text AS lane, count(*) AS cards FROM cards c
WHERE c.project_id = @project_id
  AND c.status = @ready_status
  AND c.kind = @task_kind
  AND NOT EXISTS (SELECT 1 FROM claims cl WHERE cl.card_id = c.id)
  AND (c.assignee_id IS NULL OR (c.assignee_kind = @actor_kind AND c.assignee_id = @actor_id))
  AND NOT EXISTS (
      SELECT 1 FROM card_links l
      JOIN cards b ON b.id = l.from_card_id
      WHERE l.to_card_id = c.id AND l.kind = @blocks_kind AND b.status >= 0)
  AND (c.parent_id IS NULL OR NOT EXISTS (
      SELECT 1 FROM cards p WHERE p.id = c.parent_id AND p.status = @dropped_status))
  AND c.lane IS NOT NULL
  AND (EXISTS (SELECT 1 FROM cards w WHERE w.project_id = c.project_id AND w.status = @working_status AND w.lane = c.lane)
       OR EXISTS (SELECT 1 FROM locks k WHERE k.org_id = c.org_id AND k.name = c.lane AND k.expires_at > sqlc.arg(now)::timestamptz))
GROUP BY c.lane
ORDER BY c.lane;

-- name: UpdateCard :one
UPDATE cards
SET status = @status, drop_reason = @drop_reason, priority = @priority, parent_id = @parent_id,
    assignee_kind = @assignee_kind, assignee_id = @assignee_id, answer_relayed = @answer_relayed, for_person = @for_person,
    answered_by_kind = @answered_by_kind, answered_by_id = @answered_by_id, answered_at = @answered_at,
    behalf_user_id = @behalf_user_id, behalf_agent_id = @behalf_agent_id, behalf_kept_at = @behalf_kept_at,
    behalf_reason = @behalf_reason, behalf_session = @behalf_session,
    closed_at = @closed_at, updated_at = @updated_at,
    title = @title, body = @body, page_bytes = @page_bytes, question_choices = @question_choices, answer = @answer,
    branch = @branch, lane = @lane,
    folded_into = @folded_into, folded_at = @folded_at,
    folded_by_kind = @folded_by_kind, folded_by_id = @folded_by_id,
    closing_note = @closing_note,
    version = version + 1
WHERE id = @id AND version = @expected_version
RETURNING *;

-- name: SnapshotCardRevision :execrows
-- The page column is NULL unless this edit changes the page; see card_revisions.
INSERT INTO card_revisions (id, card_id, version, updated_by_kind, updated_by_id, created_at, title, body, page)
SELECT @revision_id, c.id, c.version, @updated_by_kind, @updated_by_id, @created_at, c.title, c.body,
       CASE WHEN sqlc.arg(page_changed)::boolean THEN COALESCE(p.html, '') END
FROM cards c LEFT JOIN card_pages p ON p.card_id = c.id WHERE c.id = @id AND c.version = @expected_version
ON CONFLICT (card_id, version) DO NOTHING;

-- name: ListCardRevisions :many
-- Without bodies or pages: a list names versions, GetCardRevision reads one.
SELECT id, card_id, version, updated_by_kind, updated_by_id, created_at, title FROM card_revisions
WHERE card_id = @card_id AND (sqlc.arg('before')::bigint = 0 OR version < sqlc.arg('before')::bigint)
ORDER BY version DESC
LIMIT @max_rows;

-- name: GetCardRevision :one
-- page is the page at that version: the first kept page at or after it, else the current one.
SELECT r.id, r.card_id, r.version, r.updated_by_kind, r.updated_by_id, r.created_at, r.title, r.body,
       COALESCE(
           (SELECT k.page FROM card_revisions k WHERE k.card_id = r.card_id AND k.version >= r.version AND k.page IS NOT NULL
            ORDER BY k.version LIMIT 1),
           (SELECT p.html FROM card_pages p WHERE p.card_id = r.card_id),
           '')::text AS page
FROM card_revisions r WHERE r.card_id = @card_id AND r.version = @version;

-- name: PutCardPage :exec
INSERT INTO card_pages (card_id, html, text) VALUES (@card_id, @html, @text)
ON CONFLICT (card_id) DO UPDATE SET html = EXCLUDED.html, text = EXCLUDED.text;

-- name: DeleteCardPage :exec
DELETE FROM card_pages WHERE card_id = @card_id;

-- name: GetCardPage :one
SELECT html FROM card_pages WHERE card_id = @card_id;

-- name: CardExists :one
SELECT EXISTS (SELECT 1 FROM cards WHERE id = @id);

-- name: ListChildren :many
SELECT * FROM cards
WHERE parent_id = @parent_id
ORDER BY priority DESC, number;

-- name: ListFirstChildren :many
SELECT * FROM cards
WHERE parent_id = @parent_id
ORDER BY priority DESC, number
LIMIT @max_rows;

-- name: CountChildrenByStatus :many
SELECT status, count(*)::bigint AS n FROM cards
WHERE parent_id = @parent_id GROUP BY status;

-- name: ListOpenCardsAssignedTo :many
-- Run when an actor loses a role or is revoked, which is rare enough that
-- no index on the assignee is kept for it.
SELECT * FROM cards
WHERE org_id = @org_id AND assignee_kind = @assignee_kind AND assignee_id = @assignee_id AND status >= 0
ORDER BY id;

-- name: GetClaim :one
SELECT * FROM claims WHERE card_id = @card_id;

-- name: GetClaims :many
SELECT * FROM claims WHERE card_id = ANY(@card_ids::bigint[]);

-- name: PutClaim :exec
INSERT INTO claims (card_id, org_id, project_id, actor_kind, actor_id, session, expires_at, created_at, lease_seconds)
VALUES (@card_id, @org_id, @project_id, @actor_kind, @actor_id, @session, @expires_at, @created_at, @lease_seconds)
ON CONFLICT (card_id) DO UPDATE
    SET actor_kind = EXCLUDED.actor_kind, actor_id = EXCLUDED.actor_id, session = EXCLUDED.session,
        expires_at = EXCLUDED.expires_at, created_at = EXCLUDED.created_at, lease_seconds = EXCLUDED.lease_seconds;

-- name: DeleteClaim :exec
DELETE FROM claims WHERE card_id = @card_id;

-- name: ListClaimsByActor :many
-- Expired rows are included until the sweeper removes them; the lease is
-- the caller's to judge against its own clock.
SELECT * FROM claims
WHERE org_id = @org_id AND actor_kind = @actor_kind AND actor_id = @actor_id
ORDER BY expires_at, card_id;

-- name: RenewClaims :many
-- Only rows inside the second half of their lease are touched; expires_at is
-- not indexed, so the update is heap-only. any_session is false for an
-- agent, whose claims belong to the session that took them. The lease is the
-- one the holder stated for the claim, else the project's.
UPDATE claims c
SET expires_at = GREATEST(c.expires_at, make_interval(secs => COALESCE(c.lease_seconds, p.lease_seconds)) + sqlc.arg(now)::timestamptz)
FROM projects p
WHERE p.id = c.project_id
  AND c.org_id = @org_id AND c.actor_kind = @actor_kind AND c.actor_id = @actor_id
  AND (sqlc.arg(any_session)::boolean OR c.session = sqlc.arg(session))
  AND c.expires_at > sqlc.arg(now)::timestamptz
  AND c.expires_at - make_interval(secs => COALESCE(c.lease_seconds, p.lease_seconds) / 2.0) < sqlc.arg(now)::timestamptz
RETURNING c.*;

-- name: ListEndingClaims :many
-- Live claims whose last call is due and whose current expiry has had none.
-- It is due as domain.Claim.LastCallAt says: notice_seconds before the end,
-- or a quarter of the lease when that is shorter.
SELECT c.* FROM claims c
JOIN projects p ON p.id = c.project_id
WHERE c.expires_at > sqlc.arg(now)::timestamptz
  AND c.expires_at - make_interval(secs => LEAST(sqlc.arg(notice_seconds)::float8, COALESCE(c.lease_seconds, p.lease_seconds) / 4.0)) <= sqlc.arg(now)::timestamptz
  AND c.ending_announced_for IS DISTINCT FROM c.expires_at
ORDER BY c.expires_at LIMIT @max_rows;

-- name: MarkClaimEnding :execrows
-- A renewal moves expires_at, which arms the last call again; a claim
-- renewed, released or retaken since it was listed matches no row.
UPDATE claims SET ending_announced_for = expires_at
WHERE card_id = @card_id AND expires_at = sqlc.arg(expires_at)::timestamptz
  AND ending_announced_for IS DISTINCT FROM expires_at;

-- name: ListExpiredClaims :many
SELECT * FROM claims WHERE expires_at <= @before::timestamptz
ORDER BY expires_at LIMIT @max_rows;

-- name: LockExpiredClaim :one
-- The lock is taken before the card is written, the order every claim path
-- uses (claim row, then card row). Under the lock the expiry is checked
-- again: a claim renewed, released or retaken meanwhile is not returned.
SELECT * FROM claims WHERE card_id = @card_id AND expires_at <= @before::timestamptz FOR UPDATE;

-- name: AddCardLink :execrows
INSERT INTO card_links (from_card_id, to_card_id, kind, created_by_kind, created_by_id, created_at)
VALUES (@from_card_id, @to_card_id, @kind, @created_by_kind, @created_by_id, @created_at)
ON CONFLICT DO NOTHING;

-- name: RemoveCardLink :execrows
DELETE FROM card_links
WHERE from_card_id = @from_card_id AND to_card_id = @to_card_id AND kind = @kind;

-- name: ListCardLinks :many
SELECT * FROM card_links
WHERE from_card_id = @card_id OR to_card_id = @card_id
ORDER BY created_at, from_card_id, to_card_id, kind;

-- name: ListBlockers :many
SELECT b.* FROM cards b
WHERE b.status >= 0
  AND EXISTS (SELECT 1 FROM card_links l
              WHERE l.from_card_id = b.id AND l.to_card_id = @card_id AND l.kind = @blocks_kind)
ORDER BY b.number;

-- name: AddComment :exec
INSERT INTO comments (id, card_id, actor_kind, actor_id, body)
VALUES (@id, @card_id, @actor_kind, @actor_id, @body);

-- name: LatestComment :one
SELECT * FROM comments WHERE card_id = @card_id ORDER BY id DESC LIMIT 1;

-- name: ListComments :many
SELECT * FROM comments
WHERE card_id = @card_id AND (sqlc.arg('before')::bigint = 0 OR id < sqlc.arg('before')::bigint)
ORDER BY id DESC
LIMIT @max_rows;

-- name: MarkCardSeen :exec
INSERT INTO card_reads (card_id, project_id, actor_kind, actor_id, seen_id)
VALUES (@card_id, @project_id, @actor_kind, @actor_id, @seen_id)
ON CONFLICT (actor_kind, actor_id, card_id)
DO UPDATE SET seen_id = GREATEST(card_reads.seen_id, EXCLUDED.seen_id);

-- name: ListReplies :many
WITH unseen AS (
  SELECT m.id, m.card_id, m.actor_kind, m.actor_id, m.body,
         count(*) OVER (PARTITION BY m.card_id) AS unseen,
         row_number() OVER (PARTITION BY m.card_id ORDER BY m.id DESC) AS rn
  FROM card_reads r
  JOIN comments m ON m.card_id = r.card_id AND m.id > r.seen_id
  WHERE r.actor_kind = @actor_kind AND r.actor_id = @actor_id AND r.project_id = @project_id
    AND NOT (m.actor_kind = r.actor_kind AND m.actor_id = r.actor_id)
)
SELECT id, card_id, actor_kind, actor_id, body, unseen, count(*) OVER () AS total
FROM unseen WHERE rn = 1 AND (@before::bigint = 0 OR id < @before::bigint)
ORDER BY id DESC LIMIT @row_limit;

-- name: GetComment :one
SELECT * FROM comments WHERE id = @id;

-- name: ClaimRequestKey :execrows
INSERT INTO request_keys (actor_kind, actor_id, key, org_id, project_id, operation, card_id, subject_id, created_at)
VALUES (@actor_kind, @actor_id, @key, @org_id, @project_id, @operation, @card_id, @subject_id, @created_at)
ON CONFLICT (actor_kind, actor_id, key) DO NOTHING;

-- name: GetRequestKey :one
SELECT * FROM request_keys WHERE actor_kind = @actor_kind AND actor_id = @actor_id AND key = @key;
