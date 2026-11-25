package server

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

const (
	projectList       = "projects"
	projectMemberList = "project-members"
)

// ListProjects returns a page of the projects the caller may see, the global
// project first. Visibility is decided row by row in Go, so the page is cut
// from the visible list rather than read with a limit, which would leave short
// pages and could not count what follows.
func (s *Service) ListProjects(ctx context.Context, token string, limit int) (apitypes.ProjectList, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.ProjectList{}, err
	}
	cur, err := decodeCursor(projectList, token)
	if err != nil {
		return apitypes.ProjectList{}, err
	}
	ps, accesses, err := s.visibleProjects(ctx, s.store, p)
	if err != nil {
		return apitypes.ProjectList{}, err
	}
	if cur != nil {
		start := slices.IndexFunc(ps, func(pr domain.Project) bool {
			return int16(pr.Scope) < cur.Scope || (int16(pr.Scope) == cur.Scope && pr.Slug > cur.Name)
		})
		if start < 0 {
			start = len(ps)
		}
		ps = ps[start:]
	}
	pg := ps[:min(pageSize(limit), len(ps))]
	out := make([]apitypes.Project, 0, len(pg))
	for _, pr := range pg {
		item := toProject(pr, accesses[pr.ID].role)
		item.Rights = accesses[pr.ID].rightNames()
		out = append(out, item)
	}
	remaining := min(len(ps)-len(pg), apitypes.RemainingCap)
	return apitypes.ProjectList{Items: out, Page: forwardPage(projectList, pg, func(pr domain.Project) cursor {
		return cursor{Scope: int16(pr.Scope), Name: pr.Slug}
	}, remaining)}, nil
}

// GetProject returns one project with card counts.
func (s *Service) GetProject(ctx context.Context, slug string) (apitypes.Project, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.Project{}, err
	}
	proj, a, err := s.project(ctx, s.store, p, slug)
	if err != nil {
		return apitypes.Project{}, err
	}
	counts, err := s.store.CountCards(ctx, proj.ID)
	if err != nil {
		return apitypes.Project{}, fmt.Errorf("count cards: %w", err)
	}
	out := toProject(proj, a.role)
	out.Rights = a.rightNames()
	out.Counts = toCounts(counts)
	if g, ok, err := sessionGrant(ctx, s.store, proj.ID, p, s.now()); err != nil {
		return apitypes.Project{}, err
	} else if ok {
		out.ChatGrant = new(toSessionGrant(g, proj.Slug))
	}
	if !p.IsAgent() {
		if out.MyAgentJoin, err = s.myAgentJoin(ctx, s.store, proj.ID, p.Actor.ID); err != nil {
			return apitypes.Project{}, err
		}
	}
	return out, nil
}

// myAgentJoin is a user's own rule for their agents joining one project.
func (s *Service) myAgentJoin(ctx context.Context, st Store, projectID, userID domain.ID) (string, error) {
	r, err := st.GetAgentJoinRule(ctx, projectID, userID)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return domain.AgentJoinFollow.String(), nil
	case err != nil:
		return "", fmt.Errorf("load agent join rule: %w", err)
	}
	return r.AgentJoin.String(), nil
}

// PutMyAgentJoin sets the calling user's own rule for their agents joining
// one project they can see; follow removes it.
func (s *Service) PutMyAgentJoin(ctx context.Context, slug string, in apitypes.PutAgentJoin) (apitypes.Project, error) {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return apitypes.Project{}, err
	}
	if err := p.requireWritable(); err != nil {
		return apitypes.Project{}, err
	}
	j, err := domain.ParseAgentJoin(in.AgentJoin)
	if err != nil {
		return apitypes.Project{}, err
	}
	proj, a, err := s.project(ctx, s.store, p, slug)
	if err != nil {
		return apitypes.Project{}, err
	}
	err = s.store.WithTx(ctx, func(tx Store) error {
		if j == domain.AgentJoinFollow {
			err = tx.DeleteAgentJoinRule(ctx, proj.ID, p.Actor.ID)
		} else {
			r := domain.AgentJoinRule{ProjectID: proj.ID, UserID: p.Actor.ID, AgentJoin: j, UpdatedAt: s.now()}
			if err = r.Validate(); err == nil {
				_, err = tx.PutAgentJoinRule(ctx, r)
			}
		}
		if err != nil {
			return err
		}
		return s.emitP(ctx, tx, p, &proj.ID, domain.EventAgentJoinSetting, projectSubject(proj.ID),
			payload("", "agent_join", j.String(), "project", slug))
	})
	if err != nil {
		return apitypes.Project{}, err
	}
	out := toProject(proj, a.role)
	out.MyAgentJoin = j.String()
	return out, nil
}

func parseLease(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, invalidField("lease", "must be a Go duration such as 30m")
	}
	return d, nil
}

// CreateProject creates a project (admins) and makes the creator a member
// of it when it is private.
func (s *Service) CreateProject(ctx context.Context, in apitypes.CreateProject) (apitypes.Project, error) {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return apitypes.Project{}, err
	}
	if err := p.requireOrg(domain.PermProjectManage); err != nil {
		return apitypes.Project{}, err
	}
	org, err := s.store.GetOrg(ctx, p.OrgID)
	if err != nil {
		return apitypes.Project{}, fmt.Errorf("load org: %w", err)
	}
	lease := domain.DefaultLeaseDuration
	if in.Lease != "" {
		if lease, err = parseLease(in.Lease); err != nil {
			return apitypes.Project{}, err
		}
	}
	vis := org.Settings.ProjectVisibilityOrDefault()
	if in.Visibility != "" {
		if vis, err = domain.ParseVisibility(in.Visibility); err != nil {
			return apitypes.Project{}, invalidField("visibility", err.Error())
		}
	}
	now := s.now()
	proj := domain.Project{ID: s.ids.New(), OrgID: p.OrgID, Slug: in.Slug, Name: in.Name, Description: in.Description,
		Visibility: vis, Concurrency: domain.ConcurrencyParallel, Scope: domain.ProjectScopeProject, Lease: lease, CreatedBy: p.Actor, Status: domain.RecordActive, CreatedAt: now, UpdatedAt: now}
	if err := proj.Validate(); err != nil {
		return apitypes.Project{}, err
	}
	var out domain.Project
	err = s.store.WithTx(ctx, func(tx Store) error {
		if _, gerr := tx.GetProjectBySlug(ctx, p.OrgID, in.Slug); gerr == nil {
			return fmt.Errorf("project %q: %w", in.Slug, errExists)
		} else if !errors.Is(gerr, domain.ErrNotFound) {
			return fmt.Errorf("check project slug: %w", gerr)
		}
		created, cerr := tx.CreateProject(ctx, proj)
		if cerr != nil {
			return fmt.Errorf("create project: %w", cerr)
		}
		out = created
		return s.emitP(ctx, tx, p, &out.ID, domain.EventProjectCreated, projectSubject(out.ID),
			payload(out.Name, "slug", out.Slug, "visibility", out.Visibility.String()))
	})
	if err != nil {
		return apitypes.Project{}, err
	}
	return toProject(out, p.OrgRole), nil
}

// UpdateProject edits a project (admins).
func (s *Service) UpdateProject(ctx context.Context, slug string, in apitypes.UpdateProject) (apitypes.Project, error) {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return apitypes.Project{}, err
	}
	var out domain.Project
	var role domain.Role
	err = s.store.WithTx(ctx, func(tx Store) error {
		proj, a, perr := s.project(ctx, tx, p, slug)
		if perr != nil {
			return perr
		}
		if err := a.require(p, domain.PermProjectManage); err != nil {
			return err
		}
		role = a.role
		typ := domain.EventProjectUpdated
		if in.Name != nil {
			proj.Name = strings.TrimSpace(*in.Name)
		}
		if in.Description != nil {
			proj.Description = *in.Description
		}
		if in.Visibility != nil {
			v, err := domain.ParseVisibility(*in.Visibility)
			if err != nil {
				return invalidField("visibility", err.Error())
			}
			proj.Visibility = v
		}
		if in.Concurrency != nil {
			cc, err := domain.ParseConcurrency(*in.Concurrency)
			if err != nil {
				return invalidField("concurrency", err.Error())
			}
			proj.Concurrency = cc
		}
		if in.Lease != nil {
			d, derr := parseLease(*in.Lease)
			if derr != nil {
				return derr
			}
			proj.Lease = d
		}
		if in.AgentJoin != nil {
			j, jerr := domain.ParseAgentJoin(*in.AgentJoin)
			if jerr != nil {
				return jerr
			}
			proj.AgentJoin = j
		}
		if in.Status != nil {
			switch *in.Status {
			case "active":
				proj.Status = domain.RecordActive
			case "archived":
				if proj.Scope == domain.ProjectScopeGlobal {
					return errGlobalArchive
				}
				proj.Status = domain.RecordArchived
				typ = domain.EventProjectArchived
			default:
				return invalidField("status", "must be active or archived")
			}
		}
		proj.UpdatedAt = s.now()
		if verr := proj.Validate(); verr != nil {
			return verr
		}
		updated, uerr := tx.UpdateProject(ctx, proj)
		if uerr != nil {
			return fmt.Errorf("update project: %w", uerr)
		}
		out = updated
		return s.emitP(ctx, tx, p, &out.ID, typ, projectSubject(out.ID),
			payload(out.Name, "slug", out.Slug, "visibility", out.Visibility.String(), "status", recordStatus(out.Status, "archived"), "agent_join", out.AgentJoin.String()))
	})
	if err != nil {
		return apitypes.Project{}, err
	}
	return toProject(out, role), nil
}

// ListProjectMembers lists who has a role in the project.
func (s *Service) ListProjectMembers(ctx context.Context, slug, token string, limit int) (apitypes.ProjectMemberList, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.ProjectMemberList{}, err
	}
	cur, err := decodeCursor(projectMemberList, token)
	if err != nil {
		return apitypes.ProjectMemberList{}, err
	}
	proj, _, err := s.project(ctx, s.store, p, slug)
	if err != nil {
		return apitypes.ProjectMemberList{}, err
	}
	ms, remaining, err := s.store.ListProjectMembers(ctx, proj.ID, query.MemberFilter{Page: query.Page{Limit: limit}, Cursor: cur.memberCursor()})
	if err != nil {
		return apitypes.ProjectMemberList{}, fmt.Errorf("list project members: %w", err)
	}
	out := make([]apitypes.ProjectMember, 0, len(ms))
	for _, m := range ms {
		out = append(out, toProjectMember(proj.Slug, m))
	}
	return apitypes.ProjectMemberList{Items: out, Page: forwardPage(projectMemberList, ms, func(m domain.ProjectMember) cursor {
		return memberCursorKey(m.Role, m.CreatedAt, m.Actor)
	}, remaining)}, nil
}

// PutProjectMember grants or changes an actor's project role (admins).
func (s *Service) PutProjectMember(ctx context.Context, slug string, target domain.ActorRef, in apitypes.PutMembership) (apitypes.ProjectMember, error) {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return apitypes.ProjectMember{}, err
	}
	var out domain.ProjectMember
	var pslug string
	err = s.store.WithTx(ctx, func(tx Store) error {
		proj, a, err := s.project(ctx, tx, p, slug)
		if err != nil {
			return err
		}
		if err := a.require(p, domain.PermProjectManage); err != nil {
			return err
		}
		role, rights, err := grantOf(target, in, a.role, slug)
		if err != nil {
			return err
		}
		if err := s.actorInOrg(ctx, tx, p.OrgID, target); err != nil {
			return err
		}
		now := s.now()
		m := domain.ProjectMember{ProjectID: proj.ID, Actor: target, Role: role, Rights: rights, Status: domain.RecordActive, GrantedBy: &p.Actor.ID, CreatedAt: now, UpdatedAt: now}
		if existing, gerr := tx.GetProjectMember(ctx, proj.ID, target); gerr == nil {
			m.CreatedAt = existing.CreatedAt
		}
		saved, err := putProjectMember(ctx, tx, m)
		if err != nil {
			return fmt.Errorf("put project member: %w", err)
		}
		out, pslug = saved, proj.Slug
		return s.emitP(ctx, tx, p, &proj.ID, domain.EventMembershipGranted, membershipSubject(target.ID),
			grantPayload(target, role, rights, map[string]any{"project": proj.Slug}))
	})
	if err != nil {
		return apitypes.ProjectMember{}, err
	}
	return toProjectMember(pslug, out), nil
}

// RemoveProjectMember ends an actor's project role (admins).
func (s *Service) RemoveProjectMember(ctx context.Context, slug string, target domain.ActorRef) error {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx Store) error {
		proj, a, err := s.project(ctx, tx, p, slug)
		if err != nil {
			return err
		}
		if err := a.require(p, domain.PermProjectManage); err != nil {
			return err
		}
		m, err := tx.GetProjectMember(ctx, proj.ID, target)
		if err != nil {
			return fmt.Errorf("project member: %w", err)
		}
		m.Status, m.UpdatedAt = domain.RecordRemoved, s.now()
		if _, err := putProjectMember(ctx, tx, m); err != nil {
			return fmt.Errorf("remove project member: %w", err)
		}
		if target.Kind == domain.ActorAgent {
			agent, err := tx.GetAgent(ctx, target.ID)
			if err != nil {
				return fmt.Errorf("load agent: %w", err)
			}
			if err := s.endSessionGrants(ctx, tx, p, agent, &proj.ID); err != nil {
				return err
			}
		}
		if err := s.dropAssignments(ctx, tx, p, target); err != nil {
			return err
		}
		return s.emitP(ctx, tx, p, &proj.ID, domain.EventMembershipRemoved, membershipSubject(target.ID),
			map[string]any{"actor": toActor(target), "project": proj.Slug})
	})
}

const searchList = "search"

// Search runs full-text search over what the caller may see, optionally
// within one project, a page at a time from the cursor token.
func (s *Service) Search(ctx context.Context, q, projectSlug, token string, limit int) (apitypes.SearchResult, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.SearchResult{}, err
	}
	scope, err := s.projectScope(ctx, p, projectSlug)
	if err != nil {
		return apitypes.SearchResult{}, err
	}
	cur, err := decodeCursor(searchList, token)
	if err != nil {
		return apitypes.SearchResult{}, err
	}
	var after *query.SearchCursor
	if cur != nil {
		after = &query.SearchCursor{Rank: cur.Rank, Type: domain.SubjectType(cur.Type), ID: domain.ID(cur.ID)}
	}
	hits, remaining, err := s.store.Search(ctx, p.OrgID, scope, q, limit, after)
	if err != nil {
		return apitypes.SearchResult{}, fmt.Errorf("search: %w", err)
	}
	lk := s.lookup(s.store, p)
	out := apitypes.SearchResult{Items: make([]apitypes.SearchHit, 0, len(hits))}
	for _, h := range hits {
		slug, serr := lk.projectSlugPtr(ctx, h.ProjectID)
		if serr != nil {
			return apitypes.SearchResult{}, serr
		}
		out.Items = append(out.Items, apitypes.SearchHit{Type: h.Type.String(), ID: h.ID.String(), Number: h.Number, Slug: h.Slug,
			Kind:    kindWord(h.Kind),
			Project: slug, Title: h.Title, Status: h.Status, Snippet: h.Snippet, Rank: h.Rank})
	}
	out.Page = page(searchList, hits, func(h query.SearchHit) cursor {
		return cursor{Rank: h.Rank, Type: int16(h.Type), ID: int64(h.ID)}
	}, remaining)
	out.Prev = ""
	return out, nil
}

// kindWord names a hit's card kind; notes have none.
func kindWord(k domain.CardKind) string {
	if k == 0 {
		return ""
	}
	return k.String()
}
