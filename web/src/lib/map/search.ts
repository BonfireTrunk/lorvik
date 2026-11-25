import { titleMatcher } from '../cardSearch'
import type { PlacedNode } from './elkGraph'

/** The ids of the nodes a search matches: cards and stories by number (`12`, `#12`, `s12`) or title, closed boxes the same way, notes by title. */
export function matchNodes(nodes: PlacedNode[], search: string): string[] {
  const matches = titleMatcher(search)
  if (!matches) return []
  return nodes
    .filter((n) => {
      if (n.group) return matches(n.group)
      if (n.kind === 'card' || n.kind === 'story') return matches(n.card!)
      return n.kind === 'note' && matches({ title: n.note!.title })
    })
    .map((n) => n.id)
}
