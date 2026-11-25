<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import ActorName from './ActorName.vue'
import ErrorBanner from './ErrorBanner.vue'
import LoadState from './LoadState.vue'
import TimeAgo from './TimeAgo.vue'
import { useEventRefresh } from '../composables/useEventRefresh'
import { chatLabel } from '../lib/actors'
import { groupClaims, liveClaims, loadWorking, tickWorking, working, workingSummary } from '../lib/working'

// Who is working on what across the org: the live claims, one group per holder
// and session. The org-wide stream delivers every claim, release, expiry and
// status change; between events the tick drops a lease that has run out.
const TICK_MS = 60_000
const error = ref<unknown>(null)

const live = computed(() => liveClaims())
const groups = computed(() => groupClaims(live.value))

async function load(): Promise<void> {
  try {
    await loadWorking()
    error.value = null
  } catch (e) {
    error.value = e
  }
}

useEventRefresh({
  slug: () => '',
  source: () => [],
  match: (e) => ['card.claimed', 'card.released', 'card.claim_expired', 'card.status_changed'].includes(e.type),
  reload: () => void load(),
  delay: 400,
})

let tick: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  void load()
  tick = setInterval(tickWorking, TICK_MS)
})
onBeforeUnmount(() => clearInterval(tick))
</script>

<template>
  <section class="space-y-2" aria-label="Working now">
    <ErrorBanner :error="error" :retry="() => void load()" />
    <LoadState :loading="!working.loaded && !error" />
    <p v-if="working.loaded && !live.length" class="state">Nobody is working on anything right now.</p>
    <template v-else-if="live.length">
      <p class="text-sm font-medium">{{ workingSummary(live) }}</p>
      <ul class="panel divide-y divide-line">
        <li v-for="g in groups" :key="g.key" class="space-y-1 px-3 py-2">
          <div class="flex flex-wrap items-baseline gap-x-2">
            <span class="font-medium"><ActorName :who="g.holder" /></span>
            <span class="badge">{{ g.holder.kind === 'agent' ? 'agent' : 'person' }}</span>
            <span v-if="g.session" class="meta font-mono">{{ chatLabel(g.session, g.sessionName) }}</span>
          </div>
          <ul>
            <li v-for="c in g.claims" :key="`${c.card.project}/${c.card.number}`" class="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 py-0.5">
              <router-link :to="{ name: 'card', params: { slug: c.card.project, number: c.card.number } }" class="lnk min-w-0 flex-1 truncate">
                <span class="ident">#{{ c.card.number }}</span> {{ c.card.title }}
              </router-link>
              <span class="meta font-mono">{{ c.card.project }}</span>
              <span class="meta">claimed <TimeAgo :at="c.claimed_at" :now="working.now" /></span>
              <TimeAgo class="meta" :at="c.expires_at" :now="working.now" remaining />
            </li>
          </ul>
        </li>
      </ul>
    </template>
  </section>
</template>
