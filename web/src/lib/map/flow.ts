import type { Edge, Node } from '@vue-flow/core'
import type { PlacedEdge, PlacedNode } from './elkGraph'

export interface NodeData {
  node: PlacedNode
}

export interface EdgeData {
  edge: PlacedEdge
}

/** Vue Flow nodes; parents come before the nodes they hold, as it expects. */
export function flowNodes(nodes: PlacedNode[]): Node<NodeData>[] {
  return nodes.map((n) => ({
    id: n.id,
    type: n.kind,
    position: { x: n.x, y: n.y },
    parentNode: n.parent,
    data: { node: n },
    draggable: false,
    selectable: false,
    connectable: false,
    focusable: false,
    zIndex: n.kind === 'story' || n.kind === 'lane' ? 0 : 2,
    // Vue Flow turns pointer events off on a node that is neither draggable
    // nor selectable, which would leave clicks and hover to the pan layer.
    style: { pointerEvents: 'all' },
  }))
}

export function flowEdges(edges: PlacedEdge[]): Edge<EdgeData>[] {
  return edges.map((e) => ({
    id: e.id,
    type: 'routed',
    source: e.source,
    target: e.target,
    data: { edge: e },
    selectable: false,
    focusable: false,
    zIndex: 10,
  }))
}
