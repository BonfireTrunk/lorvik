<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter, type RouteLocationRaw } from 'vue-router'
import { panelQuery } from '../composables/useCardPanel'
import { useModalDialog } from '../composables/useModalDialog'
import { sectionTo, sections } from '../lib/navigation'
import { overlays } from '../composables/useKeymap'
import { api, remainingLabel } from '../lib/api'
import { projects } from '../lib/projects'
import { switchOrg } from '../router'
import type { SearchHit } from '../lib/types'

const emit = defineEmits<{ close: [] }>()

const route = useRoute()
const HITS = 20
const router = useRouter()

interface Item {
  id: string
  label: string
  hint: string[]
  run: () => void
  // Runs without closing the palette.
  stay?: boolean
}

const q = ref('')
const hits = ref<SearchHit[]>([])
const more = ref<{ cursor: string; remaining: number } | null>(null)
const searching = ref(false)
const failed = ref(false)
const active = ref(0)

function go(to: RouteLocationRaw): void {
  void router.push(to)
}

// Board, Stories and Decisions keep the list and open a card in the panel
// over it (dashboard.md 2.2); any other page goes to the card's own page.
function openCard(slug: string, n: number): void {
  const name = route.name
  if (name === 'board' || name === 'stories' || name === 'decisions') {
    const page = name === 'board' ? String(route.params.slug) : undefined
    go({ query: panelQuery(route.query, slug, n, page) })
    return
  }
  go({ name: 'card', params: { slug, number: n } })
}

const pages = computed<Item[]>(() => {
  const s = projects.current
  const out: Item[] = []
  for (const section of sections) {
    const to = sectionTo(section, s)
    if (to) out.push({ id: `p-${section.name}`, label: section.label, hint: [section.project ? s : 'page'], run: () => go(to) })
  }
  out.push({ id: 'p-switch-org', label: 'Switch or create org', hint: ['action'], run: () => switchOrg() })
  out.push({ id: 'p-keys', label: 'Keyboard shortcuts', hint: ['?'], run: () => (overlays.help = true) })
  return out
})

function hitItem(h: SearchHit): Item | null {
  if (h.type === 'card' && h.project && h.number) {
    const project = h.project
    const n = h.number
    return {
      id: `c-${h.id}`,
      label: `#${n} ${h.title}`,
      hint: [project, h.kind ?? '', h.status ?? ''].filter(Boolean),
      run: () => openCard(project, n),
    }
  }
  if (h.type === 'note') {
    return { id: `n-${h.id}`, label: h.title, hint: ['note', h.project ?? ''].filter(Boolean), run: () => go({ name: 'note', params: { noteKey: h.id } }) }
  }
  return null
}

const items = computed<Item[]>(() => {
  const text = q.value.trim()
  const lower = text.toLowerCase()
  const out: Item[] = []
  const num = /^#?(\d+)$/.exec(text)
  const s = projects.current
  if (num && s) {
    const n = Number(num[1])
    out.push({ id: `num-${n}`, label: `Card #${n}`, hint: [s], run: () => openCard(s, n) })
  }
  out.push(...pages.value.filter((p) => !lower || p.label.toLowerCase().includes(lower)))
  for (const h of hits.value) {
    const it = hitItem(h)
    if (it) out.push(it)
  }
  const m = more.value
  if (m) {
    out.push({ id: 'more', label: `More results (${remainingLabel(m.remaining)})`, hint: [], stay: true, run: () => void search(text, m.cursor) })
  }
  return out
})

// Debounced, and a stale response never replaces a newer one.
let timer: ReturnType<typeof setTimeout> | undefined
let seq = 0

async function search(text: string, cursor?: string): Promise<void> {
  const mine = ++seq
  searching.value = true
  try {
    const res = await api.search(text, undefined, HITS, cursor)
    if (mine !== seq) return
    hits.value = cursor ? [...hits.value, ...res.items] : res.items
    more.value = res.next && res.remaining > 0 ? { cursor: res.next, remaining: res.remaining } : null
    failed.value = false
  } catch {
    if (mine !== seq) return
    if (!cursor) hits.value = []
    more.value = null
    failed.value = true
  } finally {
    if (mine === seq) searching.value = false
  }
}

watch(q, (v) => {
  active.value = 0
  clearTimeout(timer)
  const text = v.trim()
  if (text.length < 2 || /^#?\d+$/.test(text)) {
    seq++
    hits.value = []
    more.value = null
    searching.value = false
    failed.value = false
    return
  }
  searching.value = true
  timer = setTimeout(() => void search(text), 200)
})

function choose(i: number): void {
  const it = items.value[i]
  if (!it) return
  if (!it.stay) emit('close')
  it.run()
}

function onKeydown(e: KeyboardEvent): void {
  const n = items.value.length
  if (e.key === 'ArrowDown' && n) {
    e.preventDefault()
    active.value = (active.value + 1) % n
  } else if (e.key === 'ArrowUp' && n) {
    e.preventDefault()
    active.value = (active.value - 1 + n) % n
  } else if (e.key === 'Enter') {
    e.preventDefault()
    choose(active.value)
  }
}

watch(active, (i) => document.getElementById(`cp-${i}`)?.scrollIntoView({ block: 'nearest' }))

const input = ref<HTMLInputElement | null>(null)
const { dialog: dlg, dialogEvents } = useModalDialog(() => emit('close'), {
  focus: () => input.value,
  restoreFocus: 'if-unfocused',
})
onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <dialog
    ref="dlg"
    class="float mx-auto mt-[12vh] w-full max-w-xl p-0 text-fg backdrop:bg-scrim"
    aria-label="Command palette"
    v-on="dialogEvents"
  >
    <div class="border-b border-line p-2">
      <input
        ref="input"
        v-model="q"
        class="field"
        placeholder="Go to a page, #number, or search cards and notes"
        role="combobox"
        aria-expanded="true"
        aria-controls="cp-list"
        aria-autocomplete="list"
        :aria-activedescendant="items.length ? `cp-${active}` : undefined"
        @keydown="onKeydown"
      />
    </div>
    <ul id="cp-list" role="listbox" class="max-h-[50vh] overflow-y-auto py-1" aria-label="Results">
      <li
        v-for="(it, i) in items"
        :id="`cp-${i}`"
        :key="it.id"
        role="option"
        :aria-selected="i === active"
        class="flex cursor-pointer items-baseline gap-2 px-3 py-1.5 text-sm"
        :class="i === active ? 'bg-sunken' : ''"
        @mousemove="active = i"
        @click="choose(i)"
      >
        <span class="min-w-0 flex-1 truncate">{{ it.label }}</span>
        <span class="meta flex shrink-0 gap-3"><span v-for="h in it.hint" :key="h">{{ h }}</span></span>
      </li>
    </ul>
    <p class="meta border-t border-line px-3 py-1.5">
      <template v-if="searching">Searching…</template>
      <template v-else-if="failed">Search failed; pages and #number still work.</template>
      <template v-else-if="q.trim().length >= 2 && !hits.length && !/^#?\d+$/.test(q.trim())">No cards or notes match.</template>
      <template v-else>Up and Down move, Enter opens, Esc closes.</template>
    </p>
  </dialog>
</template>
