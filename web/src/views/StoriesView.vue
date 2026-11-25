<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import CardLink from '../components/card/CardLink.vue'
import CardPanel from '../components/card/CardPanel.vue'
import ErrorBanner from '../components/ErrorBanner.vue'
import LoadMore from '../components/LoadMore.vue'
import LoadState from '../components/LoadState.vue'
import ScopeSelect from '../components/ScopeSelect.vue'
import StatusBadge from '../components/StatusBadge.vue'
import TimeAgo from '../components/TimeAgo.vue'
import { useAction } from '../composables/useAction'
import { useCardPanel } from '../composables/useCardPanel'
import { useEventRefresh } from '../composables/useEventRefresh'
import { foldBody } from '../composables/useFoldForm'
import { usePagedList } from '../composables/usePagedList'
import { api } from '../lib/api'
import { isClosed } from '../lib/cards'
import { useListScope } from '../composables/useListScope'
import { projects } from '../lib/projects'
import { canIn } from '../lib/roles'
import type { CardBrief } from '../lib/types'

type StateFilter = 'open' | 'done' | 'dropped' | 'all'

const STATES: { value: StateFilter; label: string }[] = [
  { value: 'open', label: 'Open' },
  { value: 'done', label: 'Done' },
  { value: 'dropped', label: 'Dropped' },
  { value: 'all', label: 'All' },
]

const panel = useCardPanel()
const state = ref<StateFilter>('open')
const scope = useListScope()

const { items, remaining, loading, error, hasMore, load } = usePagedList((limit, cursor) =>
  api.allCards({
    kind: 'story',
    project: scope.value === 'project' ? projects.current : 'all',
    open: state.value === 'open' || undefined,
    status: state.value === 'done' || state.value === 'dropped' ? state.value : undefined,
    limit,
    cursor,
  }),
  { autoLoad: true, source: [state, scope, () => projects.current] },
)

function open(c: CardBrief): void {
  panel.open(c.project ?? '', c.number)
}

function foldLabel(c: CardBrief): string {
  if (c.fold) return c.fold.into ? `folded into ${c.fold.into}` : 'folded, no contract change'
  return isClosed(c.status) ? 'not folded' : ''
}

const paths = ref<Record<number, string>>({})
const nones = ref<Record<number, boolean>>({})
const { busy, error: foldError, run } = useAction()
const {
  items: unfolded,
  remaining: unfoldedRemaining,
  loading: unfoldedLoading,
  error: unfoldedError,
  hasMore: unfoldedHasMore,
  load: loadUnfolded,
} = usePagedList((limit, cursor) => {
  const slug = scope.value === 'project' ? projects.current : ''
  return slug ? api.unfolded(slug, limit, cursor) : Promise.resolve({ items: [], remaining: 0 })
})

// The unfolded list carries no version, so the card is read first.
function foldOf(c: CardBrief) {
  return foldBody({ path: paths.value[c.number] ?? '', none: nones.value[c.number] === true })
}

async function fold(c: CardBrief): Promise<void> {
  const slug = projects.current
  const f = foldOf(c)
  if (!f) return
  await run(async () => {
    const full = await api.card(slug, c.number)
    await api.foldCard(slug, c.number, { version: full.version, ...f })
  })
  await loadUnfolded()
}

onMounted(() => void loadUnfolded())
watch([scope, () => projects.current], () => void loadUnfolded())
useEventRefresh({
  slug: () => '',
  source: () => [],
  match: (e) => e.type.startsWith('card.'),
  reload: () => {
    void load()
    void loadUnfolded()
  },
  delay: 400,
})
</script>

<template>
  <div class="page">
    <h1 class="page-title">Stories</h1>
    <p class="meta">Requirement stories, each with the state it is in and whether its work is folded into the contract.</p>
    <div class="toolbar">
      <ScopeSelect v-model="scope" />
      <div class="cells" role="group" aria-label="State">
        <button
          v-for="o in STATES"
          :key="o.value"
          type="button"
          class="cell"
          :aria-pressed="state === o.value"
          @click="state = o.value"
        >
          {{ o.label }}
        </button>
      </div>
    </div>
    <ErrorBanner :error="error" :retry="() => load()" />
    <LoadState :loading="loading && !items.length" :empty="!loading && !items.length && !error" empty-text="No stories here." />
    <ul v-if="items.length" class="list">
      <li v-for="c in items" :key="c.id">
        <CardLink :slug="c.project ?? ''" :number="c.number" class="list-row flex-wrap" @open="open(c)">
          <span class="ident w-10 shrink-0">#{{ c.number }}</span>
          <span class="min-w-0 flex-1 basis-48 truncate font-medium">{{ c.title }}</span>
          <span v-if="c.lane" class="badge font-mono" title="Lane">{{ c.lane }}</span>
          <span class="meta w-40 truncate">{{ foldLabel(c) }}</span>
          <span class="meta w-24 truncate font-mono">{{ c.project }}</span>
          <TimeAgo class="meta w-16 text-right" :at="c.updated_at" />
          <span class="w-20 shrink-0"><StatusBadge :status="c.status" /></span>
        </CardLink>
      </li>
    </ul>
    <LoadMore :has-more="hasMore" :loading="loading" :remaining="remaining" @load="load(true)" />

    <p v-if="scope === 'all'" class="meta">Pick a project in the switcher to see its unfolded work.</p>
    <section v-else class="space-y-2" aria-label="Unfolded">
      <h2 class="h2">Unfolded in {{ projects.current }}</h2>
      <p class="meta">Done cards with no story and no fold. Fold each into a requirements document, or mark no contract change.</p>
      <ErrorBanner :error="unfoldedError" :retry="loadUnfolded" />
      <ErrorBanner :error="foldError" />
      <LoadState :loading="false" :empty="!unfolded.length && !unfoldedError" empty-text="Nothing waiting to be folded." />
      <ul v-if="unfolded.length" class="list">
        <li v-for="c in unfolded" :key="c.id" class="list-row flex-wrap">
          <span class="ident w-10 shrink-0">#{{ c.number }}</span>
          <router-link class="lnk min-w-0 flex-1 basis-48 truncate font-medium" :to="{ name: 'card', params: { slug: projects.current, number: c.number } }">{{ c.title }}</router-link>
          <template v-if="canIn(projects.current, 'member')">
            <input v-model="paths[c.number]" class="field field-auto w-56" placeholder="docs/requirements/x.md" :aria-label="`Document path for #${c.number}`" :disabled="nones[c.number]" />
            <label class="flex items-center gap-1 text-sm"><input v-model="nones[c.number]" type="checkbox" /> No contract change</label>
            <button type="button" class="btn" :disabled="busy || !foldOf(c)" @click="fold(c)">Fold</button>
          </template>
        </li>
      </ul>
      <LoadMore :has-more="unfoldedHasMore" :loading="unfoldedLoading" :remaining="unfoldedRemaining" @load="loadUnfolded(true)" />
    </section>
    <CardPanel v-if="panel.number.value" :slug="panel.slug.value" :number="panel.number.value" @close="panel.close()" />
  </div>
</template>
