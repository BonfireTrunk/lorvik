package store

import (
	"context"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
	"github.com/BonfireTrunk/lorvik/internal/store/sqlcgen"
)

// CreateProject inserts a project with the caller's id.
func (s *Store) CreateProject(ctx context.Context, p domain.Project) (domain.Project, error) {
	row, err := s.q.CreateProject(ctx, sqlcgen.CreateProjectParams{
		ID: int64(p.ID), OrgID: int64(p.OrgID), Slug: p.Slug, Name: p.Name, Description: p.Description,
		Visibility: int16(p.Visibility), Concurrency: int16(p.Concurrency), Scope: int16(p.Scope), LeaseSeconds: int32(p.Lease / time.Second),
		CreatedByKind: int16(p.CreatedBy.Kind), CreatedByID: int64(p.CreatedBy.ID),
		Status: int16(p.Status), AgentJoin: int16(p.AgentJoin), CreatedAt: p.CreatedAt,
	})
	if err != nil {
		return domain.Project{}, wrap("create project", err)
	}
	return projectFromRow(row), nil
}

// GetProject returns one project.
func (s *Store) GetProject(ctx context.Context, id domain.ID) (domain.Project, error) {
	row, err := s.q.GetProject(ctx, int64(id))
	if err != nil {
		return domain.Project{}, wrap("get project", err)
	}
	return projectFromRow(row), nil
}

// GetProjectBySlug resolves a slug within an org.
func (s *Store) GetProjectBySlug(ctx context.Context, orgID domain.ID, slug string) (domain.Project, error) {
	row, err := s.q.GetProjectBySlug(ctx, sqlcgen.GetProjectBySlugParams{OrgID: int64(orgID), Slug: slug})
	if err != nil {
		return domain.Project{}, wrap("get project by slug", err)
	}
	return projectFromRow(row), nil
}

// ListProjects returns an org's projects by slug.
func (s *Store) ListProjects(ctx context.Context, orgID domain.ID) ([]domain.Project, error) {
	rows, err := s.q.ListProjects(ctx, int64(orgID))
	if err != nil {
		return nil, wrap("list projects", err)
	}
	return mapRows(rows, projectFromRow), nil
}

// UpdateProject writes name, description, visibility, concurrency, lease,
// status and the agent join setting.
func (s *Store) UpdateProject(ctx context.Context, p domain.Project) (domain.Project, error) {
	row, err := s.q.UpdateProject(ctx, sqlcgen.UpdateProjectParams{
		ID: int64(p.ID), Name: p.Name, Description: p.Description, Visibility: int16(p.Visibility),
		Concurrency: int16(p.Concurrency), LeaseSeconds: int32(p.Lease / time.Second), Status: int16(p.Status),
		AgentJoin: int16(p.AgentJoin), UpdatedAt: p.UpdatedAt,
	})
	if err != nil {
		return domain.Project{}, wrap("update project", err)
	}
	return projectFromRow(row), nil
}

// CountCards returns per-status counts and the count of questions awaiting a
// human: the open ones. A relayed answer is closed and no longer waits.
func (s *Store) CountCards(ctx context.Context, projectID domain.ID) (query.CardCounts, error) {
	const op = "count cards"
	rows, err := s.q.CountCardsByStatus(ctx, int64(projectID))
	if err != nil {
		return query.CardCounts{}, wrap(op, err)
	}
	out := query.CardCounts{ByStatus: make(map[domain.Status]int, len(rows))}
	for _, r := range rows {
		out.ByStatus[domain.Status(r.Status)] = int(r.N)
	}
	n, err := s.q.CountOpenQuestions(ctx, sqlcgen.CountOpenQuestionsParams{ProjectID: int64(projectID), QuestionKind: int16(domain.KindQuestion)})
	if err != nil {
		return query.CardCounts{}, wrap(op, err)
	}
	out.OpenQuestions = int(n)
	return out, nil
}

// CountOpenQuestionsBy counts the open questions the actor asked in the project.
func (s *Store) CountOpenQuestionsBy(ctx context.Context, projectID domain.ID, actor domain.ActorRef) (int, error) {
	n, err := s.q.CountOpenQuestionsBy(ctx, sqlcgen.CountOpenQuestionsByParams{ProjectID: int64(projectID),
		QuestionKind: int16(domain.KindQuestion), CreatedByKind: int16(actor.Kind), CreatedByID: int64(actor.ID)})
	if err != nil {
		return 0, wrap("count open questions by actor", err)
	}
	return int(n), nil
}
