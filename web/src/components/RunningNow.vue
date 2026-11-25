<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import ActorName from './ActorName.vue'
import ErrorBanner from './ErrorBanner.vue'
import TimeAgo from './TimeAgo.vue'
import { useAction } from '../composables/useAction'
import { useEventRefresh } from '../composables/useEventRefresh'
import { actorName, chatLabel } from '../lib/actors'
import { allPages, api } from '../lib/api'
import { canHumanIn } from '../lib/roles'
import type { Lock } from '../lib/types'

// Live locks of the org: who holds which shared resource and for how long.
// Renders nothing when no lock is live.
const props = defineProps<{ slug: string }>()
const TICK_MS = 30_000
const locks = ref<Lock[]>([])
const error = ref<unknown>(null)
const now = ref(Date.now())
const { busy, error: actionError, run } = useAction()

// Lock events carry no project, so a project-filtered stream still delivers
// them, and the sweeper's lock.expired removes a lapsed lock from the list.
// Between events the tick drops locks whose lease has run out.
const live = computed(() => locks.value.filter((l) => Date.parse(l.expires_at) > now.value))
const canRelease = computed(() => canHumanIn(props.slug, 'reviewer'))

async function load(): Promise<void> {
  try {
    locks.value = await allPages((cursor) => api.locks(cursor))
    now.value = Date.now()
    error.value = null
  } catch (e) {
    error.value = e
  }
}

async function release(l: Lock): Promise<void> {
  if (!window.confirm(`Release lock ${l.name}, held by ${actorName(l.holder)}?`)) return
  await run(async () => {
    await api.releaseLock(l.name, true)
  })
  await load()
}

useEventRefresh({
  slug: () => '',
  source: () => [],
  match: (e) => e.type.startsWith('lock.'),
  reload: () => void load(),
  delay: 400,
})

let tick: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  void load()
  tick = setInterval(() => {
    now.value = Date.now()
  }, TICK_MS)
})
onBeforeUnmount(() => clearInterval(tick))
</script>

<template>
  <section v-if="live.length || error || actionError" class="space-y-2" aria-label="Running now">
    <ErrorBanner :error="error" :retry="() => void load()" />
    <ErrorBanner :error="actionError" />
    <div v-if="live.length" class="plate px-4 py-3">
      <h2 class="mb-1 text-xs font-medium text-plate-muted">Running now</h2>
      <ul class="divide-y divide-plate-muted/30">
        <li v-for="l in live" :key="l.id" class="flex flex-wrap items-baseline gap-x-4 gap-y-1 py-2">
          <span class="ident">{{ l.name }}</span>
          <span class="text-sm"><ActorName :who="l.holder" /></span>
          <span v-if="l.session" class="text-xs text-plate-muted">{{ chatLabel(l.session, l.session_name) }}</span>
          <span class="min-w-0 flex-1 truncate text-sm text-plate-muted" :title="l.purpose">{{ l.purpose }}</span>
          <router-link
            v-if="l.card"
            class="ident text-sm underline underline-offset-2"
            :to="{ name: 'card', params: { slug: l.project ?? slug, number: l.card } }"
            >#{{ l.card }}</router-link
          >
          <TimeAgo class="figure w-24 text-right text-lg" :at="l.expires_at" :now="now" remaining />
          <button v-if="canRelease" type="button" class="btn" :disabled="busy" @click="release(l)">Release</button>
        </li>
      </ul>
    </div>
  </section>
</template>
