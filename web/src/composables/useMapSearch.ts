import { watch, type Ref } from 'vue'
import type { PlacedNode } from '../lib/map/elkGraph'
import { matchNodes } from '../lib/map/search'
import { useSearchCursor } from './useSearchCursor'

/**
 * The nodes the search matches, shared with the map focus as `matches`, plus
 * the closed boxes `inBoxes` names, which hold matching cards. A new search
 * centres on its first match after a pause; see useSearchCursor for the rest.
 */
export function useMapSearch(search: () => string, nodes: () => PlacedNode[], matches: Ref<Set<string>>, centre: (id: string) => void, inBoxes: () => string[] = () => []) {
  const cursor = useSearchCursor(
    search,
    () => [...matchNodes(nodes(), search()), ...inBoxes()],
    (id) => nodes().some((n) => n.id === id),
    centre,
  )
  watch(cursor.matchList, (ids) => (matches.value = new Set(ids)))
  return cursor
}
