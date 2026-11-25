import type { PlacedEdge, PlacedNode } from './elkGraph'

export type Dir = 'up' | 'down' | 'left' | 'right'

/** How much a miss across the direction of travel costs against one unit along it. */
const LATERAL_WEIGHT = 2
/** Score multipliers: a smaller score wins. */
const LINKED_FACTOR = 0.5
const SAME_BOX_FACTOR = 0.7
/** Where a story's header sits below the top of its box, which is the point arrows aim at. */
const STORY_HEAD_Y = 24

/** Cards, story headers, notes and closed boxes take focus; open lanes do not. */
export function isNavigable(n: PlacedNode): boolean {
  return n.kind === 'card' || n.kind === 'story' || n.kind === 'note' || !!n.group
}

function anchor(n: PlacedNode): { x: number; y: number } {
  return { x: n.ax + n.w / 2, y: n.ay + (n.kind === 'story' && !n.group ? STORY_HEAD_Y : n.h / 2) }
}

function inSameBox(a: PlacedNode, b: PlacedNode): boolean {
  return (!!a.parent && a.parent === b.parent) || a.parent === b.id || b.parent === a.id
}

/**
 * The node an arrow key moves focus to from `from`, or null when nothing lies
 * that way. A candidate must be strictly ahead of `from`'s anchor point (card
 * centre, story header) along the arrow. Its score is the distance ahead plus
 * LATERAL_WEIGHT times the sideways miss, halved when a drawn line joins the two
 * and scaled by SAME_BOX_FACTOR when they share a box (or one is the other's
 * story); the lowest score wins and the id breaks ties.
 */
export function neighbour(from: PlacedNode, nodes: PlacedNode[], edges: Pick<PlacedEdge, 'source' | 'target'>[], dir: Dir): PlacedNode | null {
  const linked = new Set<string>()
  for (const e of edges) {
    if (e.source === from.id) linked.add(e.target)
    if (e.target === from.id) linked.add(e.source)
  }
  const a = anchor(from)
  let best: { n: PlacedNode; score: number } | null = null
  for (const n of nodes) {
    if (n.id === from.id || !isNavigable(n)) continue
    const p = anchor(n)
    const dx = p.x - a.x
    const dy = p.y - a.y
    const ahead = dir === 'right' ? dx : dir === 'left' ? -dx : dir === 'down' ? dy : -dy
    if (ahead <= 0) continue
    const aside = Math.abs(dir === 'left' || dir === 'right' ? dy : dx)
    let score = ahead + LATERAL_WEIGHT * aside
    if (linked.has(n.id)) score *= LINKED_FACTOR
    if (inSameBox(from, n)) score *= SAME_BOX_FACTOR
    if (!best || score < best.score || (score === best.score && n.id < best.n.id)) best = { n, score }
  }
  return best?.n ?? null
}

/** Where focus lands when an arrow is pressed with no map node focused: the top-left card, or the top-left note on a map of notes only. */
export function firstNode(nodes: PlacedNode[]): PlacedNode | null {
  let best: PlacedNode | null = null
  for (const n of nodes) {
    if (!isNavigable(n)) continue
    const card = n.kind !== 'note'
    const bestCard = !!best && best.kind !== 'note'
    if (!best || (card && !bestCard) || (card === bestCard && (n.ay < best.ay || (n.ay === best.ay && n.ax < best.ax)))) best = n
  }
  return best
}
