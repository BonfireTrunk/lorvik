-- name: GetLock :one
-- FOR UPDATE serializes concurrent acquires of one name inside a transaction.
SELECT * FROM locks WHERE org_id = @org_id AND name = @name FOR UPDATE;

-- name: InsertLock :execrows
INSERT INTO locks (id, org_id, holder_kind, holder_id, card_id, acquired_at, expires_at, name, session, purpose)
VALUES (@id, @org_id, @holder_kind, @holder_id, @card_id, @acquired_at, @expires_at, @name, @session, @purpose)
ON CONFLICT (org_id, name) DO NOTHING;

-- name: UpdateLockHolder :execrows
UPDATE locks
SET holder_kind = @holder_kind, holder_id = @holder_id, session = @session, purpose = @purpose,
    card_id = @card_id, acquired_at = @acquired_at, expires_at = @expires_at
WHERE id = @id;

-- name: RenewLock :execrows
UPDATE locks SET expires_at = @expires_at WHERE id = @id;

-- name: DeleteLock :execrows
DELETE FROM locks WHERE id = @id;

-- name: ListLocks :many
SELECT * FROM locks WHERE org_id = @org_id ORDER BY name;

-- name: ListExpiredLocks :many
SELECT * FROM locks WHERE expires_at <= @before::timestamptz ORDER BY expires_at, id LIMIT @max_rows;
