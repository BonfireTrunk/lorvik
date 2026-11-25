import type { ElkExtendedEdge, ElkNode, LayoutOptions } from 'elkjs/lib/elk-api'
import type { CardBrief } from '../types'
import {
  CARD_W,
  GROUP_H,
  GROUP_W,
  HEAD_GAP,
  LANE_HEAD,
  NOTE_H,
  NOTE_W,
  STORY_EMPTY_H,
  STORY_HEAD,
  STORY_MIN_W,
  STORY_PAD,
  cardHeight,
  gridCols,
  gridLayout,
  type Grid,
} from './sizes'
import { cardId, groupNodeId, isUndirected, laneId, noteId, type GraphEdge, type GraphModel, type GroupBox, type LaneBox, type MapNote, type StoryBox } from './types'

export interface Pt {
  x: number
  y: number
}

export type PlacedKind = 'card' | 'story' | 'lane' | 'note'

/** A node ready for Vue Flow: x and y are relative to `parent`, ax and ay are absolute. */
export interface PlacedNode {
  id: string
  kind: PlacedKind
  x: number
  y: number
  ax: number
  ay: number
  w: number
  h: number
  parent?: string
  card?: CardBrief
  story?: StoryBox
  lane?: LaneBox
  /** Set on a story or lane node that is drawn closed. */
  group?: GroupBox
  note?: MapNote
  needs?: boolean
}

export interface PlacedEdge {
  id: string
  kind: GraphEdge['kind']
  source: string
  target: string
  merged?: GraphEdge['merged']
  points: Pt[]
}

export interface Component {
  stories: StoryBox[]
  lanes: LaneBox[]
  groups: GroupBox[]
  cards: CardBrief[]
  notes: MapNote[]
  edges: GraphEdge[]
}

export const ROOT_OPTIONS: LayoutOptions = {
  'elk.algorithm': 'layered',
  'elk.direction': 'RIGHT',
  'elk.hierarchyHandling': 'INCLUDE_CHILDREN',
  'elk.edgeRouting': 'ORTHOGONAL',
  'elk.padding': '[top=0,left=0,bottom=0,right=0]',
  'elk.spacing.nodeNode': '30',
  'elk.layered.spacing.nodeNodeBetweenLayers': '72',
  'elk.spacing.edgeNode': '22',
  'elk.spacing.edgeEdge': '12',
  'elk.layered.spacing.edgeNodeBetweenLayers': '22',
  'elk.layered.spacing.edgeEdgeBetweenLayers': '12',
  'elk.layered.considerModelOrder.strategy': 'NODES_AND_EDGES',
  'elk.layered.crossingMinimization.strategy': 'LAYER_SWEEP',
  'elk.layered.nodePlacement.strategy': 'NETWORK_SIMPLEX',
}

function boxOptions(head: number): LayoutOptions {
  return {
    'elk.padding': `[top=${head + HEAD_GAP},left=${STORY_PAD},bottom=${STORY_PAD},right=${STORY_PAD}]`,
    'elk.nodeSize.constraints': 'MINIMUM_SIZE',
    'elk.nodeSize.minimum': `(${STORY_MIN_W}, ${STORY_EMPTY_H})`,
  }
}

/** Groups of nodes that share a link, a story or lane box counting as one node with its cards. */
export function components(model: GraphModel): Component[] {
  const topOf = new Map<string, string>()
  for (const s of model.stories) for (const c of s.children) topOf.set(cardId(c.number), cardId(s.card.number))
  for (const l of model.lanes) for (const c of l.children) topOf.set(cardId(c.number), laneId(l.lane))
  const top = (id: string): string => topOf.get(id) ?? id

  const parent = new Map<string, string>()
  const find = (id: string): string => {
    let root = id
    while (parent.get(root) !== root) root = parent.get(root) ?? root
    parent.set(id, root)
    return root
  }
  const linkedCards = new Set(model.edges.flatMap((e) => [e.source, e.target]))
  const tops: string[] = [
    ...model.stories.map((s) => cardId(s.card.number)),
    ...model.lanes.map((l) => laneId(l.lane)),
    ...model.groups.map((g) => groupNodeId(g.key)),
    ...model.cards.filter((c) => !topOf.has(cardId(c.number)) && linkedCards.has(cardId(c.number))).map((c) => cardId(c.number)),
    ...model.notes.map((n) => noteId(n.slug)),
  ]
  for (const t of tops) parent.set(t, t)
  for (const e of model.edges) {
    const a = find(top(e.source))
    const b = find(top(e.target))
    if (a !== b) parent.set(a, b)
  }

  const out = new Map<string, Component>()
  const get = (t: string): Component => {
    const key = find(t)
    let c = out.get(key)
    if (!c) out.set(key, (c = { stories: [], lanes: [], groups: [], cards: [], notes: [], edges: [] }))
    return c
  }
  for (const s of model.stories) get(cardId(s.card.number)).stories.push(s)
  for (const l of model.lanes) get(laneId(l.lane)).lanes.push(l)
  for (const g of model.groups) get(groupNodeId(g.key)).groups.push(g)
  for (const c of model.cards) if (!topOf.has(cardId(c.number)) && linkedCards.has(cardId(c.number))) get(cardId(c.number)).cards.push(c)
  for (const n of model.notes) get(noteId(n.slug)).notes.push(n)
  for (const e of model.edges) get(top(e.source)).edges.push(e)
  return [...out.values()]
}

/** A topological rank of the nodes the directed pairs touch; a cycle is broken where the walk meets it. */
function topoRank(pairs: [string, string][]): Map<string, number> {
  const out = new Map<string, string[]>()
  for (const [a, b] of pairs) {
    const list = out.get(a)
    if (list) list.push(b)
    else out.set(a, [b])
  }
  const seen = new Set<string>()
  const post: string[] = []
  // Iterative, so a long blocks chain cannot overflow the stack.
  for (const start of out.keys()) {
    if (seen.has(start)) continue
    seen.add(start)
    const stack: [string, number][] = [[start, 0]]
    while (stack.length) {
      const top = stack[stack.length - 1]!
      const next = out.get(top[0])?.[top[1]++]
      if (next === undefined) {
        post.push(top[0])
        stack.pop()
      } else if (!seen.has(next)) {
        seen.add(next)
        stack.push([next, 0])
      }
    }
  }
  return new Map(post.reverse().map((id, i) => [id, i]))
}

/**
 * The ids of undirected edges whose ends ELK must see swapped. ELK ranks nodes
 * by edge direction and routes an edge that points against the ranking around
 * the outside of both boxes, so a relates line opposite a blocks line looped.
 * An edge between two boxes follows the order the blocks lines give the boxes;
 * an edge inside one box follows the order they give its cards. `home` names a
 * card's box, or nothing for a card outside every box.
 */
export function flippedEdges(edges: GraphEdge[], home: (id: string) => string | undefined): Set<string> {
  const at = (id: string): string => home(id) ?? id
  const directed = edges.filter((e) => !isUndirected(e.kind))
  const outer = topoRank(directed.filter((e) => at(e.source) !== at(e.target)).map((e) => [at(e.source), at(e.target)]))
  const inner = topoRank(directed.filter((e) => at(e.source) === at(e.target)).map((e) => [e.source, e.target]))
  const flipped = new Set<string>()
  for (const e of edges) {
    if (!isUndirected(e.kind)) continue
    const [ranks, a, b] = at(e.source) === at(e.target) ? [inner, e.source, e.target] : [outer, at(e.source), at(e.target)]
    const ra = ranks.get(a)
    const rb = ranks.get(b)
    if (ra !== undefined && rb !== undefined && ra > rb) flipped.add(e.id)
  }
  return flipped
}

interface Built {
  root: ElkNode
  packs: Map<string, Grid>
}

/** The ELK graph of one component: edges sit in their lowest common ancestor. */
export function toElk(c: Component, needs: (card: CardBrief) => boolean): Built {
  const linked = new Set(c.edges.flatMap((e) => [e.source, e.target]))
  const packs: Built['packs'] = new Map()
  const homeOf = new Map<string, string>()
  const children: ElkNode[] = []
  const boxNodes = new Map<string, ElkNode>()

  // A box holds its linked cards as ELK nodes and the rest as one grid.
  const box = (id: string, packKey: string, cards: CardBrief[], head: number): void => {
    if (!cards.length) {
      children.push({ id, width: STORY_MIN_W, height: STORY_EMPTY_H })
      return
    }
    const kids: ElkNode[] = []
    const loose: CardBrief[] = []
    for (const k of cards) {
      if (linked.has(cardId(k.number))) {
        kids.push({ id: cardId(k.number), width: CARD_W, height: cardHeight(needs(k)) })
        homeOf.set(cardId(k.number), id)
      } else loose.push(k)
    }
    if (loose.length) {
      const grid = gridLayout(loose, gridCols(loose.length), needs)
      const packId = `pack:${packKey}`
      kids.push({ id: packId, width: grid.w, height: grid.h })
      packs.set(packId, grid)
    }
    const node: ElkNode = { id, layoutOptions: boxOptions(head), children: kids, edges: [] }
    boxNodes.set(id, node)
    children.push(node)
  }
  for (const s of c.stories.slice().sort((a, b) => a.card.number - b.card.number)) box(cardId(s.card.number), String(s.card.number), s.children, STORY_HEAD)
  for (const l of c.lanes) box(laneId(l.lane), laneId(l.lane), l.children, LANE_HEAD)
  for (const k of c.cards.slice().sort((a, b) => a.number - b.number)) {
    children.push({ id: cardId(k.number), width: CARD_W, height: cardHeight(needs(k)) })
  }
  for (const g of c.groups) children.push({ id: groupNodeId(g.key), width: GROUP_W, height: GROUP_H })
  for (const n of c.notes) children.push({ id: noteId(n.slug), width: NOTE_W, height: NOTE_H })

  const root: ElkNode = { id: 'root', layoutOptions: ROOT_OPTIONS, children, edges: [] }
  const flipped = flippedEdges(c.edges, (id) => homeOf.get(id))
  for (const e of c.edges) {
    const edge: ElkExtendedEdge = flipped.has(e.id) ? { id: e.id, sources: [e.target], targets: [e.source] } : { id: e.id, sources: [e.source], targets: [e.target] }
    const home = homeOf.get(e.source)
    const container = home && home === homeOf.get(e.target) ? boxNodes.get(home)! : root
    container.edges!.push(edge)
  }
  return { root, packs }
}

export interface Laid {
  nodes: PlacedNode[]
  edges: PlacedEdge[]
  w: number
  h: number
}

/**
 * ELK's result as placed nodes and edges. Node positions are relative to the
 * parent, which is what Vue Flow wants; edge points are relative to the node
 * holding the edge, so each is moved by that node's absolute origin.
 */
export function fromElk(c: Component, built: Built, laid: ElkNode, needs: (card: CardBrief) => boolean): Laid {
  const byId = new Map<string, { story?: StoryBox; lane?: LaneBox; group?: GroupBox; card?: CardBrief; note?: MapNote }>()
  for (const s of c.stories) {
    byId.set(cardId(s.card.number), { story: s })
    for (const k of s.children) byId.set(cardId(k.number), { card: k })
  }
  for (const l of c.lanes) {
    byId.set(laneId(l.lane), { lane: l })
    for (const k of l.children) byId.set(cardId(k.number), { card: k })
  }
  for (const g of c.groups) byId.set(groupNodeId(g.key), { group: g })
  for (const k of c.cards) byId.set(cardId(k.number), { card: k })
  for (const n of c.notes) byId.set(noteId(n.slug), { note: n })
  const edgeByID = new Map(c.edges.map((e) => [e.id, e]))

  const nodes: PlacedNode[] = []
  const edges: PlacedEdge[] = []

  const walk = (n: ElkNode, ox: number, oy: number, parent?: string): void => {
    for (const e of n.edges ?? []) {
      const sec = (e as ElkExtendedEdge).sections?.[0]
      const meta = edgeByID.get(e.id)
      if (!sec || !meta) continue
      const pts = [sec.startPoint, ...(sec.bendPoints ?? []), sec.endPoint].map((p) => ({ x: p.x + ox, y: p.y + oy }))
      edges.push({ id: meta.id, kind: meta.kind, source: meta.source, target: meta.target, merged: meta.merged, points: pts })
    }
    for (const ch of n.children ?? []) {
      const ax = ox + (ch.x ?? 0)
      const ay = oy + (ch.y ?? 0)
      const pack = built.packs.get(ch.id)
      if (pack) {
        for (const cell of pack.cells) {
          nodes.push({
            id: cardId(cell.card.number),
            kind: 'card',
            x: (ch.x ?? 0) + cell.x,
            y: (ch.y ?? 0) + cell.y,
            ax: ax + cell.x,
            ay: ay + cell.y,
            w: CARD_W,
            h: cardHeight(needs(cell.card)),
            parent,
            card: cell.card,
            needs: needs(cell.card),
          })
        }
        continue
      }
      const meta = byId.get(ch.id)
      if (!meta) continue
      const base = { id: ch.id, x: ch.x ?? 0, y: ch.y ?? 0, ax, ay, w: ch.width ?? 0, h: ch.height ?? 0, parent }
      if (meta.group) nodes.push({ ...base, kind: meta.group.kind, group: meta.group })
      else if (meta.story) nodes.push({ ...base, kind: 'story', story: meta.story, card: meta.story.card })
      else if (meta.lane) nodes.push({ ...base, kind: 'lane', lane: meta.lane })
      else if (meta.card) nodes.push({ ...base, kind: 'card', card: meta.card, needs: needs(meta.card) })
      else if (meta.note) nodes.push({ ...base, kind: 'note', note: meta.note })
      walk(ch, ax, ay, ch.id)
    }
  }
  walk(laid, 0, 0)
  return { nodes, edges, w: laid.width ?? 0, h: laid.height ?? 0 }
}
