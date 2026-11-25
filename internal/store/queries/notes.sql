-- name: InsertNote :one
INSERT INTO notes (id, org_id, project_id, status, version, page_bytes,
                   created_by_kind, created_by_id, updated_by_kind, updated_by_id,
                   created_at, updated_at, slug, title, body)
VALUES (@id, @org_id, @project_id, @status, 1, @page_bytes,
        @created_by_kind, @created_by_id, @updated_by_kind, @updated_by_id,
        @created_at, @created_at, @slug, @title, @body)
RETURNING *;

-- name: GetNote :one
SELECT * FROM notes WHERE id = @id;

-- name: GetNotes :many
SELECT * FROM notes WHERE id = ANY(@ids::bigint[]);

-- name: GetProjectNoteBySlug :one
SELECT * FROM notes WHERE project_id = @project_id AND slug = @slug;

-- name: SnapshotNoteRevision :execrows
-- The page column is NULL unless this edit changes the page; see note_revisions.
INSERT INTO note_revisions (id, note_id, version, updated_by_kind, updated_by_id, title, body, page)
SELECT @revision_id, n.id, n.version, n.updated_by_kind, n.updated_by_id, n.title, n.body,
       CASE WHEN sqlc.arg(page_changed)::boolean THEN COALESCE(p.html, '') END
FROM notes n LEFT JOIN note_pages p ON p.note_id = n.id WHERE n.id = @id AND n.version = @expected_version
ON CONFLICT (note_id, version) DO NOTHING;

-- name: UpdateNote :one
UPDATE notes
SET title = @title, body = @body, page_bytes = @page_bytes, status = @status,
    updated_by_kind = @updated_by_kind, updated_by_id = @updated_by_id,
    version = version + 1, updated_at = @updated_at
WHERE id = @id AND version = @expected_version
RETURNING *;

-- name: PutNotePage :exec
INSERT INTO note_pages (note_id, html, text) VALUES (@note_id, @html, @text)
ON CONFLICT (note_id) DO UPDATE SET html = EXCLUDED.html, text = EXCLUDED.text;

-- name: DeleteNotePage :exec
DELETE FROM note_pages WHERE note_id = @note_id;

-- name: GetNotePage :one
SELECT html FROM note_pages WHERE note_id = @note_id;

-- name: NoteExists :one
SELECT EXISTS (SELECT 1 FROM notes WHERE id = @id);

-- name: ListNoteRevisions :many
-- Without bodies or pages: a list names versions, GetNoteRevision reads one.
SELECT id, note_id, version, updated_by_kind, updated_by_id, title FROM note_revisions
WHERE note_id = @note_id AND (sqlc.arg('before')::bigint = 0 OR version < sqlc.arg('before')::bigint)
ORDER BY version DESC
LIMIT @max_rows;

-- name: GetNoteRevision :one
-- page is the page at that version: the first kept page at or after it, else the current one.
SELECT r.id, r.note_id, r.version, r.updated_by_kind, r.updated_by_id, r.title, r.body,
       COALESCE(
           (SELECT k.page FROM note_revisions k WHERE k.note_id = r.note_id AND k.version >= r.version AND k.page IS NOT NULL
            ORDER BY k.version LIMIT 1),
           (SELECT p.html FROM note_pages p WHERE p.note_id = r.note_id),
           '')::text AS page
FROM note_revisions r WHERE r.note_id = @note_id AND r.version = @version;

-- name: DeleteRefs :exec
DELETE FROM refs WHERE source_id = @source_id;

-- name: InsertRefs :exec
INSERT INTO refs (source_type, source_id, target_type, target_id)
SELECT @source_type::smallint, @source_id::bigint, unnest(@target_types::smallint[]), unnest(@target_ids::bigint[])
ON CONFLICT DO NOTHING;

-- name: Backlinks :many
SELECT * FROM refs WHERE target_id = @target_id ORDER BY source_id;
