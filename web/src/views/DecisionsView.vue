<script setup lang="ts">
import { computed, ref } from 'vue'
import ActorName from '../components/ActorName.vue'
import CardLink from '../components/card/CardLink.vue'
import NewCardPanel from '../components/card/NewCardPanel.vue'
import CardPanel from '../components/card/CardPanel.vue'
import ErrorBanner from '../components/ErrorBanner.vue'
import LoadMore from '../components/LoadMore.vue'
import LoadState from '../components/LoadState.vue'
import ScopeSelect from '../components/ScopeSelect.vue'
import StatusBadge from '../components/StatusBadge.vue'
import TimeAgo from '../components/TimeAgo.vue'
import { useCardPanel } from '../composables/useCardPanel'
import { useEventRefresh } from '../composables/useEventRefresh'
import { usePagedList } from '../composables/usePagedList'
import { api } from '../lib/api'
import { decisionWord } from '../lib/cards'
import { useListScope } from '../composables/useListScope'
import { globalProjectSlug, projects } from '../lib/projects'
import type { Card, CardBrief, CardStatus } from '../lib/types'
import IconPlus from '~icons/lucide/plus'

const panel = useCardPanel()
const scope = useListScope()
const status = ref<CardStatus | ''>('')
const adding = ref(false)

const STATES: { value: CardStatus | ''; label: string }[] = [
  { value: '', label: 'Any state' },
  { value: 'ready', label: 'Proposed' },
  { value: 'done', label: 'Accepted' },
  { value: 'dropped', label: 'Rejected, revoked or superseded' },
]

const globalSlug = computed(() => globalProjectSlug('global'))
const projectQuery = computed(() => (scope.value === 'project' ? projects.current : 'all'))

const { items, remaining, loading, error, hasMore, load } = usePagedList((limit, cursor) =>
  api.allCards({ kind: 'decision', project: projectQuery.value, status: status.value || undefined, limit, cursor }),
  { autoLoad: true, source: [scope, status, () => projects.current] },
)

function open(c: CardBrief): void {
  panel.open(c.project ?? '', c.number)
}

function created(card: Card): void {
  adding.value = false
  void load()
  panel.open(card.project, card.number)
}

// Lock and project events do not change a row; every card event may (a verdict given in the panel, a new proposal).
useEventRefresh({ slug: () => '', source: () => [], match: (e) => e.type.startsWith('card.'), reload: () => void load(), delay: 400 })
</script>

<template>
  <div class="page">
    <h1 class="page-title">Decisions</h1>
    <p class="meta">Proposals and their verdicts, with who accepted each.</p>
    <div class="toolbar">
      <ScopeSelect v-model="scope" />
      <div class="cells" role="group" aria-label="State">
        <button
          v-for="o in STATES"
          :key="o.value"
          type="button"
          class="cell"
          :aria-pressed="status === o.value"
          @click="status = o.value"
        >
          {{ o.label }}
        </button>
      </div>
      <button type="button" class="btn btn-primary ml-auto" @click="adding = true"><IconPlus />New decision</button>
    </div>
    <ErrorBanner :error="error" :retry="() => load()" />
    <LoadState :loading="loading && !items.length" :empty="!loading && !items.length && !error" empty-text="No decisions here." />
    <ul v-if="items.length" class="list">
      <li v-for="d in items" :key="d.id">
        <CardLink :slug="d.project ?? ''" :number="d.number" class="list-row flex-wrap" @open="open(d)">
          <span class="ident w-10 shrink-0">#{{ d.number }}</span>
          <span class="min-w-0 flex-1 basis-48 truncate font-medium">{{ d.title }}</span>
          <span class="meta w-32 truncate"><ActorName v-if="d.decision?.accepted_by" :who="d.decision.accepted_by" /></span>
          <span class="meta w-24 truncate font-mono">{{ d.project }}</span>
          <TimeAgo class="meta w-16 text-right" :at="d.updated_at" />
          <span class="w-24 shrink-0"><StatusBadge :status="decisionWord(d.status, d.drop_reason, d.decision)" /></span>
        </CardLink>
      </li>
    </ul>
    <LoadMore :has-more="hasMore" :loading="loading" :remaining="remaining" @load="load(true)" />
    <NewCardPanel
      v-if="adding"
      :slug="projects.current || globalSlug"
      preset="decision"
      pick-project
      @created="created"
      @close="adding = false"
    />
    <CardPanel v-if="panel.number.value" :slug="panel.slug.value" :number="panel.number.value" @close="panel.close()" />
  </div>
</template>
