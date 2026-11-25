import type { Ref } from 'vue'
import { firstNode, neighbour, type Dir } from '../lib/map/nav'
import type { PlacedNode } from '../lib/map/elkGraph'
import { useCardActionKeys } from './useCardActions'
import { useKeymap } from './useKeymap'

const ARROWS: [string, Dir, string][] = [
  ['ArrowUp', 'up', 'Move to the card above'],
  ['ArrowDown', 'down', 'Move to the card below'],
  ['ArrowLeft', 'left', 'Move to the card on the left'],
  ['ArrowRight', 'right', 'Move to the card on the right'],
]

/**
 * Arrow-key focus and the card action keys on the map. Cards, story headers and
 * notes are real links, so Tab reaches them and Enter opens the panel; the
 * pane's focusin handler scrolls the new focus into view.
 */
export function useMapNav(pane: Ref<HTMLElement | null>, nodes: () => PlacedNode[], edges: () => { source: string; target: string }[], slug: () => string): void {
  function focusedNode(): HTMLElement | null {
    const active = document.activeElement
    return active instanceof HTMLElement && pane.value?.contains(active) ? active.closest<HTMLElement>('.vue-flow__node') : null
  }

  function focusNode(n: PlacedNode): void {
    pane.value?.querySelector<HTMLElement>(`.vue-flow__node[data-id="${CSS.escape(n.id)}"] :is(a, button)`)?.focus({ preventScroll: true })
  }

  function move(dir: Dir): void {
    const all = nodes()
    const id = focusedNode()?.dataset.id
    const from = id ? all.find((n) => n.id === id) : undefined
    const next = from ? neighbour(from, all, edges(), dir) : firstNode(all)
    if (next) focusNode(next)
  }

  useKeymap([
    ...ARROWS.map(([keys, dir, label]) => ({ keys, label, group: 'Map', run: () => move(dir), layers: ['page' as const] })),
    { keys: 'Enter', label: 'Open the focused card', group: 'Map', run: () => undefined, layers: [] },
  ])

  useCardActionKeys('page', () => {
    const el = focusedNode()?.querySelector<HTMLElement>('[data-card]')
    return el ? { slug: slug(), number: Number(el.dataset.card) } : null
  }, 'Focus a card first: Tab or the arrow keys walk the map')
}
