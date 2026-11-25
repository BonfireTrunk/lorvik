import type { ElkNode } from 'elkjs/lib/elk-api'
import type { CardBrief } from '../types'
import { components, fromElk, toElk, type PlacedEdge, type PlacedNode } from './elkGraph'
import { packToAspect } from './pack'
import { COMPONENT_GAP } from './sizes'
import type { GraphModel } from './types'

export interface MapLayout {
  nodes: PlacedNode[]
  edges: PlacedEdge[]
  w: number
  h: number
}

export interface LayoutEngine {
  layout: (graph: ElkNode) => Promise<ElkNode>
}

export interface LayoutOptions {
  engine: LayoutEngine
  /** Width over height of the viewport the map is fitted to. */
  aspect: number
  needs: (card: CardBrief) => boolean
}

/**
 * Lays out each linked component with ELK and packs the components into a
 * canvas close to the viewport's aspect.
 */
export async function layoutMap(model: GraphModel, o: LayoutOptions): Promise<MapLayout> {
  const comps = components(model)
  const built = comps.map((c) => toElk(c, o.needs))
  const results = await Promise.all(built.map((b) => o.engine.layout(b.root)))
  const laid = comps.map((c, i) => fromElk(c, built[i]!, results[i]!, o.needs))

  const nodes: PlacedNode[] = []
  const edges: PlacedEdge[] = []

  const pack = packToAspect(laid, COMPONENT_GAP, o.aspect)
  laid.forEach((l, i) => {
    const p = pack.places[i]!
    for (const n of l.nodes) {
      nodes.push({ ...n, ax: n.ax + p.x, ay: n.ay + p.y, x: n.parent ? n.x : n.x + p.x, y: n.parent ? n.y : n.y + p.y })
    }
    for (const e of l.edges) edges.push({ ...e, points: e.points.map((pt) => ({ x: pt.x + p.x, y: pt.y + p.y })) })
  })
  return { nodes, edges, w: pack.w, h: pack.h }
}
