-- name: InsertEvent :exec
INSERT INTO events (org_id, id, project_id, xid, type, actor_kind, actor_id, actor_session,
                    subject_type, subject_id, subject_number, payload)
VALUES (@org_id, @id, @project_id, pg_current_xact_id(), @type, @actor_kind, @actor_id, @actor_session,
        @subject_type, @subject_id, @subject_number, @payload);

-- name: NotifyOrg :exec
-- Delivered on commit, so listeners are told exactly when the event becomes visible.
SELECT pg_notify('lorvik_events', @org_id::text);

-- Visibility and order rule for every feed reader: an event is returned
-- only when the transaction that wrote it is older than every transaction
-- still open, and the feed is ordered by (xid, id). Every event that becomes
-- visible later has an xid at or above today's xmin, so it sorts after
-- everything already returned and a cursor advanced past a page never skips
-- one. Ordering by id alone would not hold: a transaction that took its xid
-- early can write a higher id and commit before a later one with a lower id.
-- A cursor is an event's position, its (xid, id), with the xid as text ("0"
-- with id 0 for the start). Every listed event carries its xid as text so
-- the server can hand out its position. That makes `xid` a text column of
-- the select list, and a bare name in ORDER BY means the select list's
-- column first: the feed queries therefore order by the table's column,
-- written `events.xid`. Text order is number order only while every xid has
-- the same count of digits.
--
-- Audience rule for every feed reader and for LatestEventPosition: an event whose type is in
-- limited_types (domain.LimitedEventTypes) is returned only when it concerns
-- the user concerns_user_id, as domain.Event.Concerns says: that user is its
-- subject (subject_type 6, a membership, carries the user's id) or its actor
-- (actor_kind 1, a user). limited_types is null only for a reader who gets
-- them all.

-- name: ListEventsAfter :many
SELECT org_id, id, project_id, type, actor_kind, actor_id, actor_session, subject_type, subject_id, subject_number, payload, xid::text AS xid
FROM events
WHERE org_id = @org_id
  AND (xid, id) > (sqlc.arg('after_xid')::text::xid8, sqlc.arg('after')::bigint)
  AND xid < pg_snapshot_xmin(pg_current_snapshot())
  AND (sqlc.narg('project_ids')::bigint[] IS NULL OR project_id IS NULL OR project_id = ANY(sqlc.narg('project_ids')::bigint[]))
  AND (sqlc.narg('exclude_actor_id')::bigint IS NULL
       OR NOT (actor_kind = sqlc.narg('exclude_actor_kind')::smallint AND actor_id = sqlc.narg('exclude_actor_id')::bigint
                AND actor_session = sqlc.narg('exclude_session')::text))
  AND (sqlc.narg('limited_types')::smallint[] IS NULL OR type <> ALL(sqlc.narg('limited_types')::smallint[])
       OR (sqlc.narg('concerns_user_id')::bigint IS NOT NULL
           AND ((subject_type = 6 AND subject_id = sqlc.narg('concerns_user_id')::bigint)
                OR (actor_kind = 1 AND actor_id = sqlc.narg('concerns_user_id')::bigint))))
ORDER BY events.xid, events.id
LIMIT @max_rows;

-- name: ListEventsBefore :many
SELECT org_id, id, project_id, type, actor_kind, actor_id, actor_session, subject_type, subject_id, subject_number, payload, xid::text AS xid
FROM (
    SELECT org_id, id, xid, project_id, type, actor_kind, actor_id, actor_session, subject_type, subject_id, subject_number, payload
    FROM events
    WHERE org_id = @org_id
      AND (xid, id) < (sqlc.arg('before_xid')::text::xid8, sqlc.arg('before')::bigint)
      AND xid < pg_snapshot_xmin(pg_current_snapshot())
      AND (sqlc.narg('project_ids')::bigint[] IS NULL OR project_id IS NULL OR project_id = ANY(sqlc.narg('project_ids')::bigint[]))
      AND (sqlc.narg('exclude_actor_id')::bigint IS NULL
           OR NOT (actor_kind = sqlc.narg('exclude_actor_kind')::smallint AND actor_id = sqlc.narg('exclude_actor_id')::bigint
                AND actor_session = sqlc.narg('exclude_session')::text))
      AND (sqlc.narg('limited_types')::smallint[] IS NULL OR type <> ALL(sqlc.narg('limited_types')::smallint[])
           OR (sqlc.narg('concerns_user_id')::bigint IS NOT NULL
               AND ((subject_type = 6 AND subject_id = sqlc.narg('concerns_user_id')::bigint)
                    OR (actor_kind = 1 AND actor_id = sqlc.narg('concerns_user_id')::bigint))))
    ORDER BY xid DESC, id DESC
    LIMIT @max_rows
) newest
ORDER BY newest.xid, newest.id;

-- name: CountEventsAfter :one
SELECT count(*)::bigint FROM (
    SELECT 1 FROM events
    WHERE org_id = @org_id
      AND (xid, id) > (sqlc.arg('after_xid')::text::xid8, sqlc.arg('after')::bigint)
      AND xid < pg_snapshot_xmin(pg_current_snapshot())
      AND (sqlc.narg('project_ids')::bigint[] IS NULL OR project_id IS NULL OR project_id = ANY(sqlc.narg('project_ids')::bigint[]))
      AND (sqlc.narg('exclude_actor_id')::bigint IS NULL
           OR NOT (actor_kind = sqlc.narg('exclude_actor_kind')::smallint AND actor_id = sqlc.narg('exclude_actor_id')::bigint
                AND actor_session = sqlc.narg('exclude_session')::text))
      AND (sqlc.narg('limited_types')::smallint[] IS NULL OR type <> ALL(sqlc.narg('limited_types')::smallint[])
           OR (sqlc.narg('concerns_user_id')::bigint IS NOT NULL
               AND ((subject_type = 6 AND subject_id = sqlc.narg('concerns_user_id')::bigint)
                    OR (actor_kind = 1 AND actor_id = sqlc.narg('concerns_user_id')::bigint))))
    LIMIT @max_rows
) bounded;

-- name: CountEventsBefore :one
SELECT count(*)::bigint FROM (
    SELECT 1 FROM events
    WHERE org_id = @org_id
      AND (xid, id) < (sqlc.arg('before_xid')::text::xid8, sqlc.arg('before')::bigint)
      AND xid < pg_snapshot_xmin(pg_current_snapshot())
      AND (sqlc.narg('project_ids')::bigint[] IS NULL OR project_id IS NULL OR project_id = ANY(sqlc.narg('project_ids')::bigint[]))
      AND (sqlc.narg('exclude_actor_id')::bigint IS NULL
           OR NOT (actor_kind = sqlc.narg('exclude_actor_kind')::smallint AND actor_id = sqlc.narg('exclude_actor_id')::bigint
                AND actor_session = sqlc.narg('exclude_session')::text))
      AND (sqlc.narg('limited_types')::smallint[] IS NULL OR type <> ALL(sqlc.narg('limited_types')::smallint[])
           OR (sqlc.narg('concerns_user_id')::bigint IS NOT NULL
               AND ((subject_type = 6 AND subject_id = sqlc.narg('concerns_user_id')::bigint)
                    OR (actor_kind = 1 AND actor_id = sqlc.narg('concerns_user_id')::bigint))))
    LIMIT @max_rows
) bounded;

-- name: ListEventsForSubject :many
-- A subject's history has no reader to check an audience against, so it
-- never returns an audience-limited event (domain.LimitedEventTypes).
SELECT org_id, id, project_id, type, actor_kind, actor_id, actor_session, subject_type, subject_id, subject_number, payload, xid::text AS xid
FROM events
WHERE org_id = @org_id AND subject_type = @subject_type AND subject_id = @subject_id
  AND type <> ALL(sqlc.arg('limited_types')::smallint[])
ORDER BY id DESC
LIMIT @max_rows;

-- name: CardsCreatedInSession :many
-- The cards among card_ids whose card.created event was written by the actor
-- in the session.
SELECT subject_id FROM events
WHERE org_id = @org_id AND subject_type = @subject_type AND subject_id = ANY(@card_ids::bigint[])
  AND type = @created_type AND actor_kind = @actor_kind AND actor_id = @actor_id AND actor_session = @session;

-- name: CardCreatedSession :one
-- The session label the card.created event of a card was written in.
SELECT actor_session FROM events
WHERE org_id = @org_id AND subject_type = @subject_type AND subject_id = @subject_id AND type = @created_type
LIMIT 1;

-- name: CardParkedFrom :one
-- The status a card left the last time it moved to waiting.
SELECT coalesce(payload->>'from', '')::text AS from_status FROM events
WHERE org_id = @org_id AND subject_type = @subject_type AND subject_id = @subject_id
  AND type = @changed_type AND payload->>'to' = @to_status::text
ORDER BY id DESC
LIMIT 1;

-- name: LatestEventPosition :many
-- The last event in feed order that the reader may get, under the project
-- and audience rules of the feed queries, so a cursor set to it skips
-- nothing of theirs and says nothing about events they may not get. No row
-- when there is none.
SELECT xid::text AS xid, id FROM events
WHERE org_id = @org_id AND xid < pg_snapshot_xmin(pg_current_snapshot())
  AND (sqlc.narg('project_ids')::bigint[] IS NULL OR project_id IS NULL OR project_id = ANY(sqlc.narg('project_ids')::bigint[]))
  AND (sqlc.narg('limited_types')::smallint[] IS NULL OR type <> ALL(sqlc.narg('limited_types')::smallint[])
       OR (sqlc.narg('concerns_user_id')::bigint IS NOT NULL
           AND ((subject_type = 6 AND subject_id = sqlc.narg('concerns_user_id')::bigint)
                OR (actor_kind = 1 AND actor_id = sqlc.narg('concerns_user_id')::bigint))))
ORDER BY events.xid DESC, events.id DESC
LIMIT 1;

-- name: EventXid :one
SELECT xid::text FROM events WHERE org_id = @org_id AND id = @id;
