-- name: CreateProject :one
INSERT INTO projects (id, org_id, slug, name, description, visibility, concurrency, scope, lease_seconds,
                      next_card_number,
                      created_by_kind, created_by_id, status, agent_join, created_at, updated_at)
VALUES (@id, @org_id, @slug, @name, @description, @visibility, @concurrency, @scope, @lease_seconds, 1,
        @created_by_kind, @created_by_id, @status, @agent_join, @created_at, @created_at)
RETURNING *;

-- name: GetProject :one
SELECT * FROM projects WHERE id = @id;

-- name: GetProjectBySlug :one
SELECT * FROM projects WHERE org_id = @org_id AND slug = @slug;

-- name: ListProjects :many
SELECT * FROM projects WHERE org_id = @org_id ORDER BY scope DESC, slug;

-- name: UpdateProject :one
UPDATE projects
SET name = @name, description = @description, visibility = @visibility, concurrency = @concurrency,
    lease_seconds = @lease_seconds, status = @status, agent_join = @agent_join, updated_at = @updated_at
WHERE id = @id
RETURNING *;

-- name: NextCardNumber :one
UPDATE projects SET next_card_number = next_card_number + 1
WHERE id = @project_id
RETURNING (next_card_number - 1)::bigint AS number;

-- name: CountCardsByStatus :many
SELECT status, count(*)::bigint AS n FROM cards
WHERE project_id = @project_id GROUP BY status;

-- name: CountOpenQuestionsBy :one
SELECT count(*)::bigint FROM cards
WHERE project_id = @project_id AND kind = @question_kind AND status >= 0
  AND created_by_kind = @created_by_kind AND created_by_id = @created_by_id;

-- name: CountOpenQuestions :one
SELECT count(*)::bigint FROM cards
WHERE project_id = @project_id AND kind = @question_kind AND status >= 0;
