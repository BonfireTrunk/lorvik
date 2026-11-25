/**
 * The rule a search box matches by, shared by the Map and the Board: a number
 * (`12`, `#12`, `s12`) or part of a title, ignoring case. Null for an empty search.
 */
export function titleMatcher(search: string): ((item: { number?: number; title: string }) => boolean) | null {
  const q = search.trim().toLowerCase().replace(/^s?#/, '')
  if (!q) return null
  const num = /^\d+$/.test(q) ? Number(q) : 0
  return (item) => (!!num && item.number === num) || item.title.toLowerCase().includes(q)
}
