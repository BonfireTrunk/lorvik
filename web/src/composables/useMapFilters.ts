import { computed, watch } from 'vue'
import { useRoute, useRouter, type LocationQueryValue } from 'vue-router'
import { OPEN_STATUSES, STATUS_ORDER } from '../lib/cards'
import { readOrgStored, writeOrgStored } from '../lib/auth'
import { MAP_KINDS, type MapFilters, type MapKind } from '../lib/map/types'
import type { CardStatus } from '../lib/types'

const DEFAULT_STATUSES = OPEN_STATUSES

const FILTER_KEYS = ['status', 'kind', 'lane', 'story', 'linked']

const storageKey = (slug: string) => `map.filters.${slug}`

function readSaved(slug: string): Record<string, string> {
  try {
    const v: unknown = JSON.parse(readOrgStored(storageKey(slug)) || '{}')
    if (typeof v !== 'object' || v === null) return {}
    return Object.fromEntries(Object.entries(v).filter(([k, x]) => FILTER_KEYS.includes(k) && typeof x === 'string'))
  } catch {
    return {}
  }
}

function text(v: LocationQueryValue | LocationQueryValue[] | undefined): string | undefined {
  return typeof v === 'string' ? v : undefined
}

function list<T extends string>(v: string | undefined, all: readonly T[], fallback: T[]): T[] {
  if (v === undefined) return fallback
  const wanted = v.split(',')
  return all.filter((x) => wanted.includes(x))
}

/**
 * The map's filters, opened boxes and search, kept in the URL query so a view can be shared.
 * The filters (not the search) are also remembered per project in this browser
 * and restored when a map opens with none of them in its link.
 */
export function useMapFilters() {
  const route = useRoute()
  const router = useRouter()

  const filters = computed<MapFilters>(() => ({
    statuses: list(text(route.query.status), STATUS_ORDER, DEFAULT_STATUSES),
    kinds: list<MapKind>(text(route.query.kind), MAP_KINDS, MAP_KINDS),
    lane: text(route.query.lane) ?? '',
    story: Number(text(route.query.story)) || 0,
    linked: route.query.linked === '1',
    open: (text(route.query.open) ?? '').split(',').filter(Boolean),
    group: text(route.query.group) ?? '',
  }))
  const search = computed(() => text(route.query.q) ?? '')
  /** The group key whose cards the side list shows, or empty. */
  const listedKey = computed(() => text(route.query.list) ?? '')

  function restore(): void {
    const slug = route.params.slug
    if (route.name !== 'map' || typeof slug !== 'string') return
    if (FILTER_KEYS.some((k) => k in route.query)) return
    const saved = readSaved(slug)
    if (Object.keys(saved).length) void router.replace({ query: { ...route.query, ...saved } })
  }
  watch(() => route.params.slug, restore, { immediate: true })

  function remember(query: Record<string, unknown>): void {
    const slug = route.params.slug
    if (typeof slug !== 'string') return
    const kept = Object.fromEntries(FILTER_KEYS.filter((k) => typeof query[k] === 'string').map((k) => [k, query[k]]))
    writeOrgStored(storageKey(slug), JSON.stringify(kept))
  }

  function patch(next: Record<string, string | undefined>): Promise<unknown> {
    const query = { ...route.query }
    for (const [k, v] of Object.entries(next)) {
      if (v === undefined || v === '' && k !== 'status' && k !== 'kind') delete query[k]
      else query[k] = v
    }
    remember(query)
    return router.replace({ query })
  }

  /** A change that opens or leaves a view of its own gets a history entry, so Back returns to the map as it was. */
  function go(next: Record<string, string | undefined>): Promise<unknown> {
    const query = { ...route.query }
    for (const [k, v] of Object.entries(next)) {
      if (v === undefined) delete query[k]
      else query[k] = v
    }
    remember(query)
    return router.push({ query })
  }

  function toggleStatus(s: CardStatus): void {
    const cur = filters.value.statuses
    const next = STATUS_ORDER.filter((x) => (x === s ? !cur.includes(s) : cur.includes(x)))
    patch({ status: next.join(',') === DEFAULT_STATUSES.join(',') ? undefined : next.join(',') })
  }

  function toggleKind(k: MapKind): void {
    const cur = filters.value.kinds
    const next = MAP_KINDS.filter((x) => (x === k ? !cur.includes(k) : cur.includes(x)))
    patch({ kind: next.length === MAP_KINDS.length ? undefined : next.join(',') })
  }

  /** Sets several filters in one step, so a card hidden by more than one appears with a single redraw. */
  function setFilters(next: Pick<MapFilters, 'statuses' | 'kinds' | 'lane' | 'story' | 'linked' | 'group'>): Promise<unknown> {
    const statuses = STATUS_ORDER.filter((s) => next.statuses.includes(s))
    const kinds = MAP_KINDS.filter((k) => next.kinds.includes(k))
    return patch({
      status: statuses.join(',') === DEFAULT_STATUSES.join(',') ? undefined : statuses.join(','),
      kind: kinds.length === MAP_KINDS.length ? undefined : kinds.join(','),
      lane: next.lane,
      story: next.story ? String(next.story) : undefined,
      linked: next.linked ? '1' : undefined,
      group: next.group || undefined,
    })
  }

  return {
    filters,
    setFilters,
    search,
    listedKey,
    toggleStatus,
    toggleKind,
    setLane: (lane: string) => patch({ lane }),
    // A story picked in the filter replaces the group a map shows alone, so the picker and the map never disagree.
    setStory: (story: number) => patch({ story: story ? String(story) : undefined, group: undefined }),
    setLinked: (on: boolean) => patch({ linked: on ? '1' : undefined }),
    setSearch: (q: string) => patch({ q }),
    setList: (key: string) => patch({ list: key }),
    openBox: (key: string) => go({ open: [...filters.value.open.filter((k) => k !== key), key].join(','), list: undefined }),
    closeBox: (key: string) => go({ open: filters.value.open.filter((k) => k !== key).join(',') || undefined }),
    showGroup: (key: string) => go({ group: key || undefined, open: undefined, list: undefined, story: undefined }),
  }
}
