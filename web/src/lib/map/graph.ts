import { STATUS_ORDER, isClosed } from '../cards'
import type { CardBrief } from '../types'
import { aloneGroup, countIn, groupOfCard } from './collapse'
import { cardId, groupNodeId, isUndirected, laneId, noteId, NO_LANE, type EdgeKind, type GraphEdge, type GraphModel, type GroupBox, type LaneBox, type MapFilters, type MapSource, type StoryBox } from './types'

function passes(c: CardBrief, f: MapFilters): boolean {
  if (!f.statuses.includes(c.status)) return false
  if (f.story && c.number !== f.story && c.parent !== f.story) return false
  if (f.lane === NO_LANE) return !c.lane
  return !f.lane || c.lane === f.lane
}

/** Board order: by status as the Board's columns run, then by number. */
function byStatus(a: CardBrief, b: CardBrief): number {
  return STATUS_ORDER.indexOf(a.status) - STATUS_ORDER.indexOf(b.status) || a.number - b.number
}

function push<K, V>(m: Map<K, V[]>, k: K, v: V): void {
  const list = m.get(k)
  if (list) list.push(v)
  else m.set(k, [v])
}

/**
 * The graph the filters leave: visible cards, the story boxes that hold them,
 * the links between visible ends, notes that mention a visible card, and a
 * lane box per lane for the cards that sit in no story box, so every card is
 * in a box. Cards in a box run in board order. A story stays when it passes
 * the filters itself or holds a visible card.
 */
function buildCards(src: MapSource, f: MapFilters): GraphModel {
  const kinds = new Set(f.kinds)
  const byNumber = new Map(src.cards.map((c) => [c.number, c]))
  const parentOf = new Map<number, number>()
  const childrenOf = new Map<number, CardBrief[]>()
  for (const c of src.cards) {
    if (c.parent && byNumber.get(c.parent)?.kind === 'story') {
      parentOf.set(c.number, c.parent)
      push(childrenOf, c.parent, c)
    }
  }

  const cards = src.cards.filter((c) => c.kind !== 'story' && kinds.has(c.kind) && passes(c, f))
  const visibleKids = new Set<number>()
  for (const c of cards) {
    const p = parentOf.get(c.number)
    if (p) visibleKids.add(p)
  }
  const storyCards = kinds.has('story') ? src.cards.filter((c) => c.kind === 'story' && (visibleKids.has(c.number) || passes(c, f))) : []

  const ids = new Set<string>([...cards, ...storyCards].map((c) => cardId(c.number)))
  const notes = kinds.has('note')
    ? src.notes.map((n) => ({ ...n, mentions: n.mentions.filter((m) => ids.has(cardId(m))) })).filter((n) => n.mentions.length)
    : []
  const drawn = new Set([...ids, ...notes.map((n) => noteId(n.slug))])
  const edges = new Map<string, GraphEdge>()
  const add = (kind: EdgeKind, source: string, target: string): void => {
    if (source === target || !drawn.has(source) || !drawn.has(target)) return
    const id = `${kind}:${source}>${target}`
    if (!edges.has(id)) edges.set(id, { id, kind, source, target })
  }
  for (const l of src.links) {
    if (parentOf.get(l.from) === l.to || parentOf.get(l.to) === l.from) continue
    add(l.kind, cardId(l.from), cardId(l.to))
  }

  for (const n of notes) for (const m of n.mentions) add('mention', cardId(m), noteId(n.slug))
  const edgeList = [...edges.values()]
  const linked = new Set(edgeList.flatMap((e) => [e.source, e.target]))

  const plain = f.linked ? cards.filter((c) => linked.has(cardId(c.number))) : cards
  const plainSet = new Set(plain.map((c) => c.number))
  const stories = f.linked
    ? storyCards.filter((s) => linked.has(cardId(s.number)) || (childrenOf.get(s.number) ?? []).some((k) => plainSet.has(k.number)))
    : storyCards
  const boxes = new Set(stories.map((s) => s.number))

  const storyBoxes: StoryBox[] = stories.map((s) => {
    const all = childrenOf.get(s.number) ?? []
    // Until the server's counts arrive, the loaded children are the best guess.
    const progress = src.progress.get(s.number) ?? { done: all.filter((c) => isClosed(c.status)).length, total: all.length }
    return {
      card: s,
      children: plain.filter((c) => parentOf.get(c.number) === s.number).sort(byStatus),
      ...progress,
    }
  })

  const byLane = new Map<string, CardBrief[]>()
  for (const c of plain) if (!boxes.has(parentOf.get(c.number) ?? 0)) push(byLane, c.lane ?? '', c)
  const lanes: LaneBox[] = [...byLane]
    .map(([lane, list]) => ({ lane, children: list.sort(byStatus) }))
    .sort((a, b) => (a.lane === '' ? 1 : b.lane === '' ? -1 : a.lane.localeCompare(b.lane)))

  return { cards: plain, stories: storyBoxes, notes, edges: edgeList, lanes, groups: [], shown: plain.length + stories.length }
}

/**
 * Closed boxes for the stories and lanes the status filter leaves, with one
 * line per pair of boxes and link kind; the boxes in `f.open` are drawn with
 * their cards, which `src` holds. A line to or from an open box ends on the box.
 * The kind and lane filters narrow only the cards of open boxes: the server
 * counts a box by status alone.
 */
function buildCollapsed(src: MapSource, f: MapFilters): GraphModel {
  const open = new Set(f.open)
  const cards = src.cards.filter((c) => open.has(groupOfCard(c)))
  const numbers = new Set(cards.map((c) => c.number))
  const inner = buildCards({ ...src, cards, links: src.links.filter((l) => numbers.has(l.from) && numbers.has(l.to)) }, f)

  const candidates: GroupBox[] = []
  for (const g of src.groups) {
    if (open.has(g.key)) continue
    const shown = countIn(g.counts, f.statuses)
    if (g.kind === 'story' ? !shown && !f.statuses.includes(g.status!) : !shown) continue
    candidates.push({ key: g.key, kind: g.kind, number: g.story ?? 0, title: g.title ?? '', lane: g.lane ?? '', status: g.status, shown, counts: Object.fromEntries(STATUS_ORDER.map((st) => [st, f.statuses.includes(st) ? g.counts[st] : 0])) as GroupBox['counts'] })
  }

  const drawn = new Set([...inner.stories.map((s) => cardId(s.card.number)), ...inner.lanes.map((l) => laneId(l.lane)), ...candidates.map((b) => groupNodeId(b.key))])
  const edges = new Map<string, GraphEdge>()
  for (const l of src.groupLinks) {
    const source = groupNodeId(l.from)
    const target = groupNodeId(l.to)
    if (source === target || !drawn.has(source) || !drawn.has(target) || (open.has(l.from) && open.has(l.to))) continue
    const pair = isUndirected(l.kind) && source > target ? `${target}|${source}` : `${source}>${target}`
    const id = `g:${l.kind}:${pair}`
    const held = edges.get(id)
    if (held) held.merged!.count += l.count
    else edges.set(id, { id, kind: l.kind, source, target, merged: { count: l.count, from: l.from, to: l.to } })
  }
  const merged = [...edges.values()]
  const linked = new Set(merged.flatMap((e) => [e.source, e.target]))
  const groups = f.linked ? candidates.filter((b) => linked.has(groupNodeId(b.key))) : candidates
  const kept = new Set([...groups.map((b) => groupNodeId(b.key)), ...inner.stories.map((s) => cardId(s.card.number)), ...inner.lanes.map((l) => laneId(l.lane))])

  return {
    ...inner,
    groups,
    edges: [...inner.edges, ...merged.filter((e) => kept.has(e.source) && kept.has(e.target))],
    shown: inner.shown + groups.reduce((n, b) => n + b.shown + (b.kind === 'story' && f.statuses.includes(b.status!) ? 1 : 0), 0),
  }
}

/**
 * The graph the source and filters call for: every card, a closed box per
 * story and lane, or the cards of one group alone.
 */
export function buildGraph(src: MapSource, f: MapFilters): GraphModel {
  if (src.mode === 'collapsed') return buildCollapsed(src, f)
  const alone = src.mode === 'group' ? aloneGroup(f) : ''
  return buildCards(alone ? { ...src, cards: src.cards.filter((c) => groupOfCard(c) === alone) } : src, f)
}
