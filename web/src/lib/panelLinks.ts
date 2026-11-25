import type { Router } from 'vue-router'

export interface PanelLink {
  slug: string
  number: number
}

export function cardHref(router: Router, slug: string, number: number): string {
  return router.resolve({ name: 'card', params: { slug, number } }).href
}

export function isPlainLinkClick(e: MouseEvent, target = ''): boolean {
  return !e.defaultPrevented && e.button === 0 && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey && (!target || target === '_self')
}

// What an unmodified left click on a
// link inside a card panel points at. Modified clicks and links that are not
// cards return null so the browser opens the full page. The caller
// decides whether the target belongs in a panel and calls preventDefault.
export function panelLinkTarget(e: MouseEvent, router: Router): PanelLink | null {
  if (!isPlainLinkClick(e)) return null
  const a = (e.target as Element | null)?.closest('a')
  if (!a || !isPlainLinkClick(e, a.target)) return null
  const href = a.getAttribute('href')
  if (!href) return null
  const r = router.resolve(href)
  const n = Number(r.params.number)
  if (!Number.isInteger(n) || n <= 0) return null
  if (r.name === 'card' && typeof r.params.slug === 'string') return { slug: r.params.slug, number: n }
  return null
}
