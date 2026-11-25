package store

import (
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BonfireTrunk/lorvik/internal/query"
	"github.com/BonfireTrunk/lorvik/internal/store/sqlcgen"
)

// The keysets below only go forward, so they name no reverse order.

const (
	memberOrder = "role DESC, created_at, actor_kind, actor_id"
	memberAfter = "(role < @c_role OR (role = @c_role AND (created_at, actor_kind, actor_id) > (@c_created_at, @c_actor_kind, @c_actor_id)))"
)

func memberKey(role int16, created time.Time, kind int16, id int64) pgx.NamedArgs {
	return pgx.NamedArgs{"c_role": role, "c_created_at": created, "c_actor_kind": kind, "c_actor_id": id}
}

func memberCursorArgs(c *query.MemberCursor) pgx.NamedArgs {
	if c == nil {
		return nil
	}
	return memberKey(int16(c.Role), c.CreatedAt, int16(c.Actor.Kind), int64(c.Actor.ID))
}

var (
	membershipKeyset = keyset[sqlcgen.Membership]{
		order: memberOrder, after: memberAfter,
		key: func(r sqlcgen.Membership) pgx.NamedArgs {
			return memberKey(r.Role, r.CreatedAt, r.ActorKind, r.ActorID)
		},
	}
	projectMemberKeyset = keyset[sqlcgen.ProjectMember]{
		order: memberOrder, after: memberAfter,
		key: func(r sqlcgen.ProjectMember) pgx.NamedArgs {
			return memberKey(r.Role, r.CreatedAt, r.ActorKind, r.ActorID)
		},
	}
	agentKeyset = keyset[sqlcgen.Agent]{
		order: "status DESC, created_at, id",
		after: "(status < @c_status OR (status = @c_status AND (created_at, id) > (@c_created_at, @c_id)))",
		key: func(r sqlcgen.Agent) pgx.NamedArgs {
			return pgx.NamedArgs{"c_status": r.Status, "c_created_at": r.CreatedAt, "c_id": r.ID}
		},
	}
	tokenKeyset = keyset[sqlcgen.Token]{
		order: "created_at DESC, id DESC",
		after: "(created_at, id) < (@c_created_at, @c_id)",
		key:   func(r sqlcgen.Token) pgx.NamedArgs { return pgx.NamedArgs{"c_created_at": r.CreatedAt, "c_id": r.ID} },
	}
	lockKeyset = keyset[sqlcgen.Lock]{
		order: "name",
		after: "name > @c_name",
		key:   func(r sqlcgen.Lock) pgx.NamedArgs { return pgx.NamedArgs{"c_name": r.Name} },
	}
)
