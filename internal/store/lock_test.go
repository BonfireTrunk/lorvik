//go:build integration

package store

import (
	"errors"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
)

func (e *env) lock(name string, ttl time.Duration) domain.Lock {
	return domain.Lock{ID: gen.New(), OrgID: e.org.ID, Name: name, Holder: e.agent, Session: "brisk-otter", Purpose: "work",
		AcquiredAt: t0, ExpiresAt: t0.Add(ttl)}
}

func TestLockNameIsUniquePerOrg(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	if err := e.st.InsertLock(ctx, e.lock("db/schema", time.Hour)); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := e.st.InsertLock(ctx, e.lock("db/schema", time.Hour)); !errors.Is(err, domain.ErrLockHeld) {
		t.Fatalf("second insert of the same name = %v, want ErrLockHeld", err)
	}
	otherOrg, err := e.st.CreateOrg(ctx, domain.Org{ID: gen.New(), Name: "Other", Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	inOther := e.lock("db/schema", time.Hour)
	inOther.OrgID = otherOrg.ID
	if err := e.st.InsertLock(ctx, inOther); err != nil {
		t.Errorf("same name in another org = %v, want it allowed", err)
	}
	if _, err := e.st.GetLock(ctx, e.org.ID, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get missing = %v, want ErrNotFound", err)
	}
}

func TestUpdateLockHolderTakesTheRowOverInPlace(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	old := e.lock("gen", time.Minute)
	if err := e.st.InsertLock(ctx, old); err != nil {
		t.Fatal(err)
	}
	card := e.card(t, nil)
	next := old
	next.Holder, next.Session, next.Purpose, next.CardID = e.user, "", "new work", &card.ID
	next.AcquiredAt, next.ExpiresAt = at(1), at(2)
	if err := e.st.UpdateLockHolder(ctx, next); err != nil {
		t.Fatal(err)
	}
	got, err := e.st.GetLock(ctx, e.org.ID, "gen")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != old.ID || got.Holder != e.user || got.Session != "" || got.Purpose != "new work" || got.CardID == nil || *got.CardID != card.ID ||
		!got.AcquiredAt.Equal(at(1)) || !got.ExpiresAt.Equal(at(2)) {
		t.Errorf("after take-over = %+v, want the same row with the new holder, purpose, card and times", got)
	}

	if err := e.st.RenewLock(ctx, old.ID, at(5)); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.st.GetLock(ctx, e.org.ID, "gen"); !got.ExpiresAt.Equal(at(5)) || got.Purpose != "new work" {
		t.Errorf("after renew = %+v, want only expires_at moved", got)
	}
}

func TestListLocksByNameAndExpiredForTheSweeper(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	short, long := e.lock("b", time.Minute), e.lock("a", time.Hour)
	for _, l := range []domain.Lock{short, long} {
		if err := e.st.InsertLock(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	all, err := e.st.ListLocks(ctx, e.org.ID)
	if err != nil || len(all) != 2 || all[0].Name != "a" || all[1].Name != "b" {
		t.Fatalf("list = %+v, %v; want a then b", all, err)
	}
	expired, err := e.st.ListExpiredLocks(ctx, t0.Add(2*time.Minute), maxPageSize)
	if err != nil {
		t.Fatal(err)
	}
	var ids []domain.ID
	for _, l := range expired {
		if l.OrgID == e.org.ID {
			ids = append(ids, l.ID)
		}
	}
	if len(ids) != 1 || ids[0] != short.ID {
		t.Errorf("expired ids = %v, want only the short lock", ids)
	}
	if err := e.st.DeleteLock(ctx, short.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.st.DeleteLock(ctx, short.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}
}

func TestLiveLockHoldsBackCardsInItsLane(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.card(t, func(c *domain.Card) { c.Lane = "db" })
	if err := e.st.InsertLock(ctx, e.lock("db", time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.NextReady(ctx, e.project.ID, e.agent, t0); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("next under a live lock = %v, want ErrNotFound", err)
	}
	waiting, err := e.st.CountOnBusyLanes(ctx, e.project.ID, e.agent, t0)
	if err != nil || len(waiting) != 1 || waiting[0].Lane != "db" || waiting[0].Cards != 1 {
		t.Errorf("busy lanes = %+v, %v; want one card on db", waiting, err)
	}
	later := t0.Add(2 * time.Minute)
	if _, err := e.st.NextReady(ctx, e.project.ID, e.agent, later); err != nil {
		t.Errorf("next after the lock lapsed = %v, want the card", err)
	}
	if waiting, err := e.st.CountOnBusyLanes(ctx, e.project.ID, e.agent, later); err != nil || len(waiting) != 0 {
		t.Errorf("busy lanes after expiry = %+v, %v; want none", waiting, err)
	}
}
