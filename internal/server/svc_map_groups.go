package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

const (
	mapGroupList     = "map-groups"
	mapGroupLinkList = "map-group-links"
)

const (
	storyKeyPrefix = "story:"
	laneKeyPrefix  = "lane:"
)

// groupKey is the wire key of a map group. A lane name may hold any text, so
// the prefix, not a separator, tells the kinds apart.
func groupKey(r query.MapGroupRef) string {
	if r.Kind == query.GroupStory {
		return storyKeyPrefix + strconv.FormatInt(r.Number, 10)
	}
	return laneKeyPrefix + r.Lane
}

// parseGroupKey reads a key written by groupKey, from a cursor.
func parseGroupKey(key string) (query.MapGroupRef, error) {
	if lane, ok := strings.CutPrefix(key, laneKeyPrefix); ok {
		return query.MapGroupRef{Kind: query.GroupLane, Lane: lane}, nil
	}
	if num, ok := strings.CutPrefix(key, storyKeyPrefix); ok {
		n, err := strconv.ParseInt(num, 10, 64)
		if err == nil && n > 0 {
			return query.MapGroupRef{Kind: query.GroupStory, Number: n}, nil
		}
	}
	return query.MapGroupRef{}, badCursor()
}

// ListMapGroups lists the boxes of the collapsed map: one per story, with the
// status counts of its children, and one per lane, with the counts of the
// cards outside any story, the lane "" holding those with no lane.
func (s *Service) ListMapGroups(ctx context.Context, slug string, limit int, token string) (apitypes.MapGroupList, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.MapGroupList{}, err
	}
	proj, _, err := s.project(ctx, s.store, p, slug)
	if err != nil {
		return apitypes.MapGroupList{}, err
	}
	cur, err := decodeCursor(mapGroupList, token)
	if err != nil {
		return apitypes.MapGroupList{}, err
	}
	var after *query.MapGroupRef
	if cur != nil {
		ref, err := parseGroupKey(cur.GroupFrom)
		if err != nil {
			return apitypes.MapGroupList{}, err
		}
		after = &ref
	}
	groups, remaining, err := s.store.ListMapGroups(ctx, proj.ID, pageSize(limit), after)
	if err != nil {
		return apitypes.MapGroupList{}, fmt.Errorf("list map groups: %w", err)
	}
	items := make([]apitypes.MapGroup, len(groups))
	for i, g := range groups {
		item := apitypes.MapGroup{Key: groupKey(g.Ref), Counts: *toCounts(g.Counts)}
		if g.Ref.Kind == query.GroupStory {
			item.Kind, item.Story, item.Title, item.Status = "story", g.Ref.Number, g.Title, g.StoryStatus.String()
		} else {
			item.Kind, item.Lane = "lane", g.Ref.Lane
		}
		items[i] = item
	}
	key := func(g query.MapGroup) cursor { return cursor{GroupFrom: groupKey(g.Ref)} }
	return apitypes.MapGroupList{Items: items, Page: forwardPage(mapGroupList, groups, key, remaining)}, nil
}

// ListMapGroupLinks lists, per ordered pair of different map groups and link
// kind, how many links run from a card of the first group to a card of the
// second. Only links between two cards of the project count.
func (s *Service) ListMapGroupLinks(ctx context.Context, slug string, limit int, token string) (apitypes.MapGroupLinkList, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.MapGroupLinkList{}, err
	}
	proj, _, err := s.project(ctx, s.store, p, slug)
	if err != nil {
		return apitypes.MapGroupLinkList{}, err
	}
	cur, err := decodeCursor(mapGroupLinkList, token)
	if err != nil {
		return apitypes.MapGroupLinkList{}, err
	}
	var after *query.MapGroupLinkCursor
	if cur != nil {
		from, err := parseGroupKey(cur.GroupFrom)
		if err != nil {
			return apitypes.MapGroupLinkList{}, err
		}
		to, err := parseGroupKey(cur.GroupTo)
		if err != nil {
			return apitypes.MapGroupLinkList{}, err
		}
		after = &query.MapGroupLinkCursor{From: from, To: to, Kind: domain.CardLinkKind(cur.Type)}
	}
	links, remaining, err := s.store.ListMapGroupLinks(ctx, proj.ID, pageSize(limit), after)
	if err != nil {
		return apitypes.MapGroupLinkList{}, fmt.Errorf("list map group links: %w", err)
	}
	items := make([]apitypes.MapGroupLink, len(links))
	for i, l := range links {
		items[i] = apitypes.MapGroupLink{From: groupKey(l.From), To: groupKey(l.To), Kind: l.Kind.String(), Count: l.Count}
	}
	key := func(l query.MapGroupLink) cursor {
		return cursor{GroupFrom: groupKey(l.From), GroupTo: groupKey(l.To), Type: int16(l.Kind)}
	}
	return apitypes.MapGroupLinkList{Items: items, Page: forwardPage(mapGroupLinkList, links, key, remaining)}, nil
}
