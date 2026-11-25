import type { CardBrief, CardStatus, MapGroup, StatusCounts } from '../types'
import type { MapFilters, MapMode } from './types'

/** Above this many items on screen the map draws a closed box per story and lane. */
export const COLLAPSE_ABOVE = 200

export const storyKey = (n: number): string => `story:${n}`
export const laneKey = (lane: string): string => `lane:${lane}`

/** The group a card belongs to: a story is in its own, a child in its story's, any other card in its lane's. */
export function groupOfCard(c: Pick<CardBrief, 'kind' | 'number' | 'parent' | 'lane'>): string {
  if (c.kind === 'story') return storyKey(c.number)
  return c.parent ? storyKey(c.parent) : laneKey(c.lane ?? '')
}

/** How a lane box is headed on the map: by the lane's name alone, since its list icon says it is a lane (#438). */
export function laneLabel(lane: string): string {
  return lane || 'No lane'
}

/** How a lane box is named to a screen reader, which does not see the icon. */
export function laneSpoken(lane: string): string {
  return lane ? `Lane ${lane}` : 'No lane'
}

/** How a box is headed: a story by its title, a lane by laneLabel. */
export function groupLabel(g: { kind: 'story' | 'lane'; title?: string; lane?: string }): string {
  return g.kind === 'story' ? (g.title ?? '') : laneLabel(g.lane ?? '')
}

/** How a box is named to a screen reader: a story by its title, a lane by laneSpoken. */
export function groupSpoken(g: { kind: 'story' | 'lane'; title?: string; lane?: string }): string {
  return g.kind === 'story' ? (g.title ?? '') : laneSpoken(g.lane ?? '')
}

/** The group whose cards a map shows alone: the one named, or the picked story. */
export function aloneGroup(f: Pick<MapFilters, 'group' | 'story'>): string {
  return f.group || (f.story ? storyKey(f.story) : '')
}

/** How many cards of the given statuses a set of counts holds. */
export function countIn(counts: StatusCounts, statuses: CardStatus[]): number {
  return statuses.reduce((n, s) => n + counts[s], 0)
}

/**
 * The mode the filters call for. The size is judged from the project's card
 * counts by status, which are known before any card is read; the kind and lane
 * filters narrow a map only once its cards are drawn.
 */
export function mapMode(f: MapFilters, counts: StatusCounts | null): MapMode {
  if (aloneGroup(f)) return 'group'
  return counts && countIn(counts, f.statuses) > COLLAPSE_ABOVE ? 'collapsed' : 'cards'
}

/** Whether opening `key` on a map that has `open` boxes open keeps the cards drawn within the limit. */
export function fitsOpen(groups: MapGroup[], open: string[], key: string, statuses: CardStatus[]): boolean {
  const keys = new Set([...open, key])
  const total = groups.filter((g) => keys.has(g.key)).reduce((n, g) => n + countIn(g.counts, statuses), 0)
  return total <= COLLAPSE_ABOVE
}
