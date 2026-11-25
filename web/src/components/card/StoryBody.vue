<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import KindBadge from '../KindBadge.vue'
import ErrorBanner from '../ErrorBanner.vue'
import StatusBadge from '../StatusBadge.vue'
import { MAX_PAGE, allPages, api } from '../../lib/api'
import { auth } from '../../lib/auth'
import { STATUS_ORDER, awaitsHuman, decisionWord, isClosed } from '../../lib/cards'
import { canHumanIn } from '../../lib/roles'
import type { Card, CardBrief, CardStatus } from '../../lib/types'

// Progress, children and the fold of a story. Children are read with
// ?parent=N so decisions and questions are included at every status.
const props = defineProps<{ card: Card }>()


const children = ref<CardBrief[]>([])
const error = ref<unknown>(null)

const percent = computed(() => {
  const p = props.card.progress
  return p && p.total ? Math.round((p.done / p.total) * 100) : 0
})
const groups = computed(() =>
  STATUS_ORDER.map((s) => ({ status: s, items: children.value.filter((c) => c.status === s) })).filter((g) => g.items.length),
)
const decisions = computed(() => children.value.filter((c) => c.kind === 'decision'))
const openQuestions = computed(() => children.value.filter((c) => c.kind === 'question' && !isClosed(c.status)))
const canAct = computed(() => canHumanIn(props.card.project, 'reviewer'))
const waitingOn = computed(() => children.value.filter((c) => awaitsHuman(c, canAct.value ? auth.me?.actor : c.assignee)))

async function load(): Promise<void> {
  try {
    children.value = await allPages((cursor) => api.cards(props.card.project, { parent: props.card.number, limit: MAX_PAGE, cursor }))
    error.value = null
  } catch (e) {
    error.value = e
  }
}

watch(() => [props.card.project, props.card.number, props.card.version], () => void load(), { immediate: true })
</script>

<template>
  <div class="space-y-4">
    <ErrorBanner :error="error" :retry="load" />

    <section v-if="waitingOn.length" class="needs-frame" :aria-label="canAct ? 'Waiting on you' : 'Waiting on a reviewer'">
      <h2 class="needs-plate">{{ canAct ? 'Waiting on you' : 'Waiting on a reviewer' }}</h2>
      <ul class="space-y-1.5 px-3 py-2.5">
        <li v-for="c in waitingOn" :key="c.number" class="flex flex-wrap items-center gap-2">
          <KindBadge :kind="c.kind" />
          <router-link class="lnk" :to="{ name: 'card', params: { slug: card.project, number: c.number } }"><span class="ident">#{{ c.number }}</span> {{ c.title }}</router-link>
          <span v-if="canAct" class="text-sm text-muted">{{ c.kind === 'decision' ? 'Accept or Reject are on that card' : 'Answer it on that card' }}</span>
        </li>
      </ul>
    </section>

    <section v-if="card.progress">
      <h2 class="h2">Progress</h2>
      <div class="h-2 overflow-hidden bg-sunken" role="progressbar" :aria-valuenow="card.progress.done" :aria-valuemax="card.progress.total" aria-label="Children done">
        <div class="h-full bg-fg" :style="{ width: `${percent}%` }"></div>
      </div>
      <p class="mt-1 text-sm"><span class="font-semibold">{{ card.progress.done }}</span> <span class="text-muted">of {{ card.progress.total }} done</span></p>
    </section>

    <section v-if="openQuestions.length">
      <h2 class="h2">Open questions</h2>
      <ul class="space-y-1">
        <li v-for="q in openQuestions" :key="q.number" class="flex flex-wrap items-center gap-2">
          <KindBadge kind="question" />
          <router-link class="lnk" :to="{ name: 'card', params: { slug: card.project, number: q.number } }"><span class="ident">#{{ q.number }}</span> {{ q.title }}</router-link>
          <StatusBadge class="ml-1" :status="q.status" />
        </li>
      </ul>
    </section>

    <section v-if="decisions.length">
      <h2 class="h2">Decisions</h2>
      <ul class="space-y-0.5">
        <li v-for="d in decisions" :key="d.number">
          <router-link class="lnk" :to="{ name: 'card', params: { slug: card.project, number: d.number } }"><span class="ident">#{{ d.number }}</span> {{ d.title }}</router-link>
          <StatusBadge class="ml-1" :status="decisionWord(d.status, d.drop_reason, d.decision)" />
        </li>
      </ul>
    </section>

    <section v-for="g in groups" :key="g.status">
      <h2 class="h2 capitalize">{{ g.status }} ({{ g.items.length }})</h2>
      <ul class="space-y-0.5">
        <li v-for="c in g.items" :key="c.number">
          <span class="meta mr-2 inline-block w-16">{{ c.kind }}</span>
          <router-link class="lnk" :to="{ name: 'card', params: { slug: card.project, number: c.number } }"><span class="ident">#{{ c.number }}</span> {{ c.title }}</router-link>
        </li>
      </ul>
    </section>
  </div>
</template>
