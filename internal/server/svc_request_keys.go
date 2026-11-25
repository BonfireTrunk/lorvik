package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// claimRequestKey is the first write of a keyed request. With no key it does
// nothing. Otherwise it records the key for the caller and reports repeat
// false, or finds the earlier request that used it and reports repeat true
// with that row: the caller then returns what the first request made, writing
// nothing and checking nothing more. A key already used for another request
// is a 422. cardID is the card the request addresses; a create passes the id
// of the card it would make, which a repeat does not compare.
func (s *Service) claimRequestKey(ctx context.Context, tx Store, p Principal, key string, op domain.RequestOp, proj domain.Project, cardID, subjectID domain.ID) (domain.RequestKey, bool, error) {
	if key == "" {
		return domain.RequestKey{}, false, nil
	}
	if !domain.ValidRequestKey(key) {
		return domain.RequestKey{}, false, newProblem(http.StatusUnprocessableEntity, apitypes.HeaderIdempotencyKey+" is not a valid key",
			"use 1 to 64 letters, digits, hyphens and underscores")
	}
	k, claimed, err := tx.ClaimRequestKey(ctx, domain.RequestKey{Actor: p.Actor, Key: key, OrgID: p.OrgID, ProjectID: proj.ID,
		Operation: op, CardID: cardID, SubjectID: subjectID, CreatedAt: s.now()})
	if err != nil {
		return domain.RequestKey{}, false, fmt.Errorf("claim request key: %w", err)
	}
	if claimed {
		return domain.RequestKey{}, false, nil
	}
	if k.Operation != op || k.ProjectID != proj.ID || (op != domain.RequestCreateCard && k.CardID != cardID) {
		return domain.RequestKey{}, false, newProblem(http.StatusUnprocessableEntity, apitypes.HeaderIdempotencyKey+" was used for another request",
			"send a new key for a different request")
	}
	return k, true, nil
}
