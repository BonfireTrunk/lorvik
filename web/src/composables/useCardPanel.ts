import { computed, type ComputedRef } from 'vue'
import { useRoute, useRouter, type LocationQuery } from 'vue-router'

export interface CardPanelState {
  slug: ComputedRef<string>
  number: ComputedRef<number>
  open: (slug: string, number: number) => void
  close: () => void
}

/**
 * The query that opens card `n` of project `target` in a panel over a page
 * whose own project is `pageSlug` (none on Stories and Decisions): `card`,
 * plus `cardProject` unless the card is the page's own.
 */
export function panelQuery(current: LocationQuery, target: string, n: number, pageSlug?: string): LocationQuery {
  const { cardProject: _, ...rest } = current
  const query = { ...rest, card: String(n) }
  return target === pageSlug ? query : { ...query, cardProject: target }
}

/**
 * The card panel's URL state: `?card=N`, plus `?cardProject=slug` when the card
 * is not in the page's own project. Pages without a project (Stories,
 * Decisions) pass no `pageSlug` and always carry `cardProject`.
 */
export function useCardPanel(pageSlug?: string): CardPanelState {
  const route = useRoute()
  const router = useRouter()

  const number = computed(() => {
    const n = Number(route.query.card)
    return Number.isInteger(n) && n > 0 ? n : 0
  })
  const slug = computed(() => {
    const p = route.query.cardProject
    return typeof p === 'string' && p ? p : (pageSlug ?? '')
  })

  function open(target: string, n: number): void {
    void router.push({ query: panelQuery(route.query, target, n, pageSlug) })
  }

  // Replace, not push: closing must not add an entry that Back would reopen.
  function close(): void {
    const { card: _card, cardProject: _project, ...rest } = route.query
    void router.replace({ query: rest })
  }

  return {
    slug,
    number: computed(() => (slug.value ? number.value : 0)),
    open,
    close,
  }
}
