import { computed, inject, provide, ref, type ComputedRef, type InjectionKey, type Ref } from 'vue'
import type { GraphEdge } from '../lib/map/types'

export interface MapFocus {
  hoverNode: Ref<string>
  hoverEdge: Ref<string>
  /** The id of the line between boxes whose links are listed. */
  picked: Ref<string>
  /** Ids of the nodes the search matched. */
  matches: Ref<Set<string>>
  /** The nodes that stay at full strength, or null when nothing is hovered. */
  litNodes: ComputedRef<Set<string> | null>
  litEdges: ComputedRef<Set<string> | null>
  /** Lets go of the hovered node or edge and the picked line. A tile removed under the pointer never gets its mouseleave, so whatever removes tiles wholesale calls this. */
  release: () => void
}

const KEY: InjectionKey<MapFocus> = Symbol('map-focus')

/** Which nodes and edges stay at full strength while one is hovered. */
export function provideMapFocus(edges: () => GraphEdge[]): MapFocus {
  const hoverNode = ref('')
  const hoverEdge = ref('')
  const picked = ref('')
  const matches = ref(new Set<string>())

  const litNodes = computed(() => {
    if (hoverEdge.value) {
      const e = edges().find((x) => x.id === hoverEdge.value)
      return e ? new Set([e.source, e.target]) : null
    }
    if (!hoverNode.value) return null
    const lit = new Set([hoverNode.value])
    for (const e of edges()) {
      if (e.source === hoverNode.value) lit.add(e.target)
      if (e.target === hoverNode.value) lit.add(e.source)
    }
    return lit
  })
  const litEdges = computed(() => {
    if (hoverEdge.value) return new Set([hoverEdge.value])
    if (!hoverNode.value) return null
    return new Set(edges().filter((e) => e.source === hoverNode.value || e.target === hoverNode.value).map((e) => e.id))
  })

  function release(): void {
    hoverNode.value = ''
    hoverEdge.value = ''
    picked.value = ''
  }

  const focus: MapFocus = { hoverNode, hoverEdge, picked, matches, litNodes, litEdges, release }
  provide(KEY, focus)
  return focus
}

export function useMapFocus(): MapFocus {
  const focus = inject(KEY)
  if (!focus) throw new Error('useMapFocus outside a map')
  return focus
}

/** What a map node needs from the shared focus: whether it is dimmed or matched, and the listeners that hover it. */
export function useMapNodeFocus(id: () => string) {
  const focus = useMapFocus()
  const dim = computed(() => !!focus.litNodes.value && !focus.litNodes.value.has(id()))
  const hit = computed(() => focus.matches.value.has(id()))
  const hover = {
    onMouseenter: () => (focus.hoverNode.value = id()),
    onMouseleave: () => (focus.hoverNode.value = ''),
    onFocus: () => (focus.hoverNode.value = id()),
    onBlur: () => (focus.hoverNode.value = ''),
  }
  return { dim, hit, hover }
}
