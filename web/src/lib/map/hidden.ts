import type { CardBrief } from '../types'
import { groupOfCard } from './collapse'
import { NO_LANE, type GraphModel, type MapFilters, type MapMode } from './types'

export type Hider = 'status' | 'kind' | 'lane' | 'story' | 'linked' | 'group' | 'unloaded'

/** What the map draws, read off the graph it built, and the filters behind it. */
export interface HiddenContext {
  filters: MapFilters
  mode: MapMode
  /** The numbers of the cards drawn as a node: cards, and stories drawn for a child. */
  drawn: Set<number>
  /** The keys of the closed boxes drawn. */
  boxes: Set<string>
  /** The numbers of the cards the map has read. */
  loaded: Set<number>
}

export function hiddenContext(filters: MapFilters, mode: MapMode, model: GraphModel, loaded: CardBrief[]): HiddenContext {
  return {
    filters,
    mode,
    drawn: new Set([...model.cards, ...model.stories.map((s) => s.card)].map((c) => c.number)),
    boxes: new Set(model.groups.map((b) => b.key)),
    loaded: new Set(loaded.map((c) => c.number)),
  }
}

const word = (s: string): string => s.charAt(0).toUpperCase() + s.slice(1)

const LABELS: Record<Hider, (c: CardBrief) => string> = {
  status: (c) => `${word(c.status)} is off`,
  kind: (c) => `${word(c.kind)} kind is off`,
  lane: () => 'Lane filter',
  story: () => 'Story filter',
  linked: () => 'Only cards with links',
  group: () => 'Another box is shown alone',
  unloaded: () => 'Not loaded yet',
}

/**
 * What keeps a card off the map; empty when it is drawn, or sits in a closed
 * box that is, where it is a box hit. Whether it is drawn is read off the
 * graph; the filters only name the reasons, in the order they are read. A card
 * the filters pass that is not drawn is held by "only cards with links", or is
 * not loaded yet.
 */
export function hiders(c: CardBrief, ctx: HiddenContext): Hider[] {
  const f = ctx.filters
  const group = groupOfCard(c)
  const closed = ctx.mode === 'collapsed' && !f.open.includes(group)
  if (ctx.drawn.has(c.number) || (closed && ctx.boxes.has(group) && f.statuses.includes(c.status))) return []
  const out: Hider[] = []
  if (!f.statuses.includes(c.status)) out.push('status')
  if (f.story && c.number !== f.story && c.parent !== f.story) out.push('story')
  else if (f.group && group !== f.group) out.push('group')
  // The server counts a closed box by status alone, so the other filters apply only to cards drawn.
  if (!closed) {
    if (!f.kinds.includes(c.kind)) out.push('kind')
    if (f.lane && (f.lane === NO_LANE ? !!c.lane : c.lane !== f.lane)) out.push('lane')
  }
  if (out.length) return out
  return [f.linked && (closed || ctx.loaded.has(c.number)) ? 'linked' : 'unloaded']
}

/** Why a card is hidden, for a list; null when it is not. */
export function hiddenReason(c: CardBrief, ctx: HiddenContext): { label: string } | null {
  const h = hiders(c, ctx)
  return h.length ? { label: h.map((x) => LABELS[x](c)).join(', ') } : null
}

/** The filters with only what hides the card changed. */
export function revealFilters(c: CardBrief, ctx: HiddenContext): Pick<MapFilters, 'statuses' | 'kinds' | 'lane' | 'story' | 'linked' | 'group'> {
  const f = ctx.filters
  const h = new Set(hiders(c, ctx))
  return {
    statuses: h.has('status') ? [...f.statuses, c.status] : f.statuses,
    kinds: h.has('kind') ? [...f.kinds, c.kind] : f.kinds,
    lane: h.has('lane') ? '' : f.lane,
    story: h.has('story') ? 0 : f.story,
    linked: h.has('linked') ? false : f.linked,
    group: h.has('group') ? '' : f.group,
  }
}
