package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// requireAnywhere checks perm against the caller's org standing and, failing
// that, their standing in every project they can see. Locks are org-scoped, so any
// project where the caller holds the role is enough.
func (s *Service) requireAnywhere(ctx context.Context, st Store, p Principal, perm domain.Permission) error {
	if perm.Mutating() {
		if err := p.requireWritable(); err != nil {
			return err
		}
	}
	if p.canOrg(perm) {
		return nil
	}
	_, accesses, err := s.visibleProjects(ctx, st, p)
	if err != nil {
		return err
	}
	for _, a := range accesses {
		if a.can(perm) {
			return nil
		}
	}
	return p.orgRefusal(perm)
}

// lockHeld is the 409 for a name someone else holds: it names the holder,
// their session, the purpose and the seconds left.
func lockHeld(l domain.Lock, now time.Time) error {
	left := int((l.ExpiresAt.Sub(now) + time.Second - 1) / time.Second)
	return withHint(fmt.Errorf("%w: %s is held by %s %s (session %q) for %q, %ds left",
		domain.ErrLockHeld, l.Name, l.Holder.Kind, l.Holder.ID, l.Session, l.Purpose, left), "wait, or pick other work")
}

func lockExpiredEvent(l domain.Lock) map[string]any {
	return map[string]any{"name": l.Name, "holder": toActor(l.Holder), "purpose": l.Purpose}
}

// AcquireLock takes the named lock for the caller. Any caller with the member
// role in the org or in some project may take one. A lapsed lock is taken over
// in place; the caller's own live lock in the same session is renewed with the
// new purpose and writes no event; anyone else's live lock is a 409.
func (s *Service) AcquireLock(ctx context.Context, name string, in apitypes.AcquireLock) (apitypes.Lock, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.Lock{}, err
	}
	name, nerr := domain.ParseLockName(name)
	ttl, terr := domain.LockTTL(in.TTLSeconds)
	if err := errors.Join(nerr, domain.ValidateLockPurpose(in.Purpose), terr); err != nil {
		return apitypes.Lock{}, err
	}
	switch {
	case in.Card != nil && in.Project == "":
		return apitypes.Lock{}, invalidField("project", "card numbers are per project; name the project of the card")
	case in.Card == nil && in.Project != "":
		return apitypes.Lock{}, invalidField("card", "project is only for naming a card")
	}
	var out domain.Lock
	err = s.store.WithTx(ctx, func(tx Store) error {
		if err := s.requireAnywhere(ctx, tx, p, domain.PermCardWrite); err != nil {
			return err
		}
		var cardID *domain.ID
		if in.Card != nil {
			proj, _, err := s.project(ctx, tx, p, in.Project)
			if err != nil {
				return err
			}
			c, err := refCard(ctx, tx, proj.ID, *in.Card, "card")
			if err != nil {
				return err
			}
			cardID = &c.ID
		}
		now := s.now()
		next := domain.Lock{
			ID: s.ids.New(), OrgID: p.OrgID, Name: name, Holder: p.Actor, Session: p.Session, Purpose: in.Purpose,
			CardID: cardID, AcquiredAt: now, ExpiresAt: now.Add(ttl),
		}
		cur, err := tx.GetLock(ctx, p.OrgID, name)
		switch {
		case errors.Is(err, domain.ErrNotFound):
			if err := tx.InsertLock(ctx, next); err != nil {
				if errors.Is(err, domain.ErrLockHeld) {
					// Another transaction inserted the name first.
					if cur, gerr := tx.GetLock(ctx, p.OrgID, name); gerr == nil {
						return lockHeld(cur, now)
					}
				}
				return fmt.Errorf("insert lock %q: %w", name, err)
			}
		case err != nil:
			return fmt.Errorf("load lock %q: %w", name, err)
		case cur.Expired(now):
			if err := s.emit(ctx, tx, cur.Holder, p.OrgID, domain.EventLockExpired, lockSubject(cur), lockExpiredEvent(cur)); err != nil {
				return err
			}
			next.ID = cur.ID
			if err := tx.UpdateLockHolder(ctx, next); err != nil {
				return fmt.Errorf("take over lock %q: %w", name, err)
			}
		case cur.HeldBy(p.Actor, p.Session):
			next.ID, next.AcquiredAt = cur.ID, cur.AcquiredAt
			if err := tx.UpdateLockHolder(ctx, next); err != nil {
				return fmt.Errorf("renew lock %q: %w", name, err)
			}
			out = next
			return nil
		default:
			return lockHeld(cur, now)
		}
		out = next
		pl := map[string]any{"name": name, "purpose": in.Purpose, "ttl": int(ttl / time.Second)}
		if in.Card != nil {
			pl["card"] = *in.Card
		}
		return s.emitP(ctx, tx, p, nil, domain.EventLockAcquired, lockSubject(next), pl)
	})
	if err != nil {
		return apitypes.Lock{}, err
	}
	return s.lookup(s.store, p).lock(ctx, out)
}

// RenewLock extends the caller's own lock to now plus the new lease. Only the
// session that took the lock may renew it; renewing is explicit and writes no
// event.
func (s *Service) RenewLock(ctx context.Context, name string, in apitypes.RenewLock) (apitypes.Lock, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.Lock{}, err
	}
	if err := p.requireWritable(); err != nil {
		return apitypes.Lock{}, err
	}
	name, nerr := domain.ParseLockName(name)
	ttl, terr := domain.LockTTL(in.TTLSeconds)
	if err := errors.Join(nerr, terr); err != nil {
		return apitypes.Lock{}, err
	}
	var out domain.Lock
	err = s.store.WithTx(ctx, func(tx Store) error {
		cur, err := s.liveLock(ctx, tx, p, name)
		if err != nil {
			return err
		}
		if !cur.HeldBy(p.Actor, p.Session) {
			return withHint(fmt.Errorf("%w: lock %q is held by %s %s (session %q)", domain.ErrForbidden, name, cur.Holder.Kind, cur.Holder.ID, cur.Session),
				"only the session that took the lock can renew it")
		}
		cur.ExpiresAt = s.now().Add(ttl)
		if err := tx.RenewLock(ctx, cur.ID, cur.ExpiresAt); err != nil {
			return fmt.Errorf("renew lock %q: %w", name, err)
		}
		out = cur
		return nil
	})
	if err != nil {
		return apitypes.Lock{}, err
	}
	return s.lookup(s.store, p).lock(ctx, out)
}

// ReleaseLock deletes a live lock. The holder may release it from any
// session; a user who may triage may release anyone's with force.
func (s *Service) ReleaseLock(ctx context.Context, name string, force bool) error {
	p, err := s.principal(ctx)
	if err != nil {
		return err
	}
	if err := p.requireWritable(); err != nil {
		return err
	}
	name, err = domain.ParseLockName(name)
	if err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx Store) error {
		cur, err := s.liveLock(ctx, tx, p, name)
		if err != nil {
			return err
		}
		forced := false
		if cur.Holder != p.Actor {
			if !force || p.IsAgent() {
				return withHint(fmt.Errorf("%w: lock %q is held by %s %s", domain.ErrForbidden, name, cur.Holder.Kind, cur.Holder.ID),
					"only the holder can release it; a reviewer can force it")
			}
			if err := s.requireAnywhere(ctx, tx, p, domain.PermCardTriage); err != nil {
				return err
			}
			forced = true
		}
		if err := tx.DeleteLock(ctx, cur.ID); err != nil {
			return fmt.Errorf("release lock %q: %w", name, err)
		}
		pl := map[string]any{"name": name, "forced": forced}
		if forced {
			pl["holder"] = toActor(cur.Holder)
		}
		return s.emitP(ctx, tx, p, nil, domain.EventLockReleased, lockSubject(cur), pl)
	})
}

// liveLock loads the org's lock of that name; a lapsed one counts as gone,
// since the next acquire or the sweeper settles it.
func (s *Service) liveLock(ctx context.Context, st Store, p Principal, name string) (domain.Lock, error) {
	cur, err := st.GetLock(ctx, p.OrgID, name)
	if err != nil {
		return domain.Lock{}, fmt.Errorf("lock %q: %w", name, err)
	}
	if cur.Expired(s.now()) {
		return domain.Lock{}, withHint(fmt.Errorf("lock %q expired: %w", name, domain.ErrNotFound), "acquire it again")
	}
	return cur, nil
}

const lockList = "locks"

// ListLocks returns a page of the org's live locks by name.
func (s *Service) ListLocks(ctx context.Context, token string, limit int) (apitypes.LockList, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.LockList{}, err
	}
	cur, err := decodeCursor(lockList, token)
	if err != nil {
		return apitypes.LockList{}, err
	}
	after := ""
	if cur != nil {
		after = cur.Name
	}
	locks, remaining, err := s.store.ListLiveLocks(ctx, p.OrgID, s.now(), query.Page{Limit: limit}, after)
	if err != nil {
		return apitypes.LockList{}, fmt.Errorf("list locks: %w", err)
	}
	lk := s.lookup(s.store, p)
	if err := lk.loadLockNames(ctx, locks); err != nil {
		return apitypes.LockList{}, err
	}
	out := make([]apitypes.Lock, 0, len(locks))
	for _, l := range locks {
		al, err := lk.lock(ctx, l)
		if err != nil {
			return apitypes.LockList{}, err
		}
		out = append(out, al)
	}
	return apitypes.LockList{Items: out, Page: forwardPage(lockList, locks, func(l domain.Lock) cursor {
		return cursor{Name: l.Name}
	}, remaining)}, nil
}

func (s *Service) liveLocks(ctx context.Context, p Principal) ([]apitypes.Lock, error) {
	all, err := s.store.ListLocks(ctx, p.OrgID)
	if err != nil {
		return nil, fmt.Errorf("list locks: %w", err)
	}
	now := s.now()
	lk := s.lookup(s.store, p)
	if err := lk.loadLockNames(ctx, all); err != nil {
		return nil, err
	}
	out := make([]apitypes.Lock, 0, len(all))
	for _, l := range all {
		if l.Expired(now) {
			continue
		}
		al, err := lk.lock(ctx, l)
		if err != nil {
			return nil, err
		}
		out = append(out, al)
	}
	return out, nil
}

// loadLockNames loads the names of the chats holding locks in one pass.
func (l *lookup) loadLockNames(ctx context.Context, locks []domain.Lock) error {
	chats := make([]chatOf, len(locks))
	for i, k := range locks {
		chats[i] = chatOf{k.Holder, k.Session}
	}
	return l.loadSessionNames(ctx, chats...)
}

// lock converts a lock, resolving its card to a number and project slug.
func (l *lookup) lock(ctx context.Context, k domain.Lock) (apitypes.Lock, error) {
	name, err := l.sessionName(ctx, k.Holder, k.Session)
	if err != nil {
		return apitypes.Lock{}, err
	}
	out := apitypes.Lock{
		ID: k.ID.String(), Name: k.Name, Holder: toActor(k.Holder), Session: k.Session, SessionName: name, Purpose: k.Purpose,
		AcquiredAt: k.AcquiredAt, ExpiresAt: k.ExpiresAt,
	}
	if k.CardID == nil {
		return out, nil
	}
	c, err := l.st.GetCard(ctx, *k.CardID)
	if errors.Is(err, domain.ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return apitypes.Lock{}, fmt.Errorf("load card of lock %q: %w", k.Name, err)
	}
	slug, err := l.projectSlug(ctx, c.ProjectID)
	if err != nil {
		return apitypes.Lock{}, err
	}
	out.Card, out.Project = &c.Number, slug
	return out, nil
}

// sweepLocks deletes every lapsed lock it can see and writes lock.expired for
// each, attributed to the former holder so their inbox shows it. It reports
// how many it removed.
func (s *Service) sweepLocks(ctx context.Context, now time.Time) (int, error) {
	expired, err := s.store.ListExpiredLocks(ctx, now, sweepBatch)
	if err != nil {
		return 0, fmt.Errorf("list expired locks: %w", err)
	}
	removed := 0
	for _, l := range expired {
		done, err := s.expireLock(ctx, l, now)
		if err != nil {
			return removed, err
		}
		if done {
			removed++
		}
	}
	return removed, nil
}

// expireLock re-reads the row under its lock: an acquire may have taken the
// name over since the sweeper listed it, and that new lease must stay.
func (s *Service) expireLock(ctx context.Context, l domain.Lock, now time.Time) (bool, error) {
	done := false
	err := s.store.WithTx(ctx, func(tx Store) error {
		cur, err := tx.GetLock(ctx, l.OrgID, l.Name)
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("load lock %q: %w", l.Name, err)
		}
		if !cur.Expired(now) {
			return nil
		}
		if err := tx.DeleteLock(ctx, cur.ID); err != nil {
			return fmt.Errorf("delete lock %q: %w", cur.Name, err)
		}
		done = true
		return s.emit(ctx, tx, cur.Holder, cur.OrgID, domain.EventLockExpired, lockSubject(cur), lockExpiredEvent(cur))
	})
	return done, err
}
