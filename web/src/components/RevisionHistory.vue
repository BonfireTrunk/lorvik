<script setup lang="ts">
import { ref, watch } from 'vue'
import { useHistoryList } from '../composables/useHistoryList'
import type { CardRevision, HistoryQuery, NoteRevision } from '../lib/types'
import ActorName from './ActorName.vue'
import ErrorBanner from './ErrorBanner.vue'
import LoadMore from './LoadMore.vue'
import LoadState from './LoadState.vue'
import NoteBody from './NoteBody.vue'
import PageFrame from './PageFrame.vue'
import TimeAgo from './TimeAgo.vue'
import IconHistory from '~icons/lucide/history'

type Revision = CardRevision | NoteRevision
const props = defineProps<{
  source: string
  version: number
  project?: string
  fetchList: (q: HistoryQuery) => Promise<Revision[]>
  fetchRevision: (version: number) => Promise<Revision>
  replaceBody?: boolean
}>()
function pageBytes(page: string): number {
  return new TextEncoder().encode(page).length
}

const emit = defineEmits<{ error: [error: unknown] }>()
const list = useHistoryList(
  (q) => props.fetchList(q),
  (r: Revision) => String(r.version),
)
const { items: revisions, loading, hasOlder, loadOlder } = list
const shown = ref<Revision | null>(null)
const error = ref<unknown>(null)
const open = ref(false)

function failed(e: unknown): void {
  error.value = e
  emit('error', e)
}

watch(list.error, (e) => {
  if (e) failed(e)
  else error.value = null
})

async function toggle(): Promise<void> {
  if (open.value) {
    open.value = false
    list.reset()
    shown.value = null
    return
  }
  open.value = true
  await list.refresh()
}

async function show(version: number): Promise<void> {
  try {
    shown.value = await props.fetchRevision(version)
  } catch (e) {
    failed(e)
  }
}

// A saved edit bumps the version: refresh an open list, drop a stale view.
watch(
  () => [props.source, props.version],
  (now, was) => {
    shown.value = null
    if (now[0] !== was[0]) {
      open.value = false
      list.reset()
    } else if (open.value) void list.refresh()
  },
)

defineExpose({ open, toggle })
</script>

<template>
  <section v-if="!replaceBody || open">
    <p v-if="!replaceBody" class="flex items-center gap-2">
      <button type="button" class="btn-link" :aria-expanded="open" @click="toggle">
        <IconHistory />{{ open ? 'Hide history' : 'History' }}
      </button>
    </p>
    <ErrorBanner v-if="!replaceBody" :error="error" />
    <template v-if="open">
      <h2 class="h2">History</h2>
      <LoadState :loading="loading && !revisions.length" :empty="!loading && !revisions.length" empty-text="The title and body have not been edited." />
      <ul v-if="revisions.length" class="list">
        <li v-for="r in revisions" :key="r.id">
          <button type="button" class="list-row w-full text-left" @click="show(r.version)">
            <span class="meta w-10 font-mono">v{{ r.version }}</span>
            <span class="min-w-0 flex-1 truncate">{{ r.title }}</span>
            <span class="meta" :class="replaceBody ? 'w-28 truncate' : ''"><template v-if="!replaceBody">edited by </template><ActorName :who="r.updated_by" /></span>
            <TimeAgo class="meta text-right" :class="replaceBody ? 'w-16' : 'w-20'" :at="r.created_at" />
          </button>
        </li>
      </ul>
      <LoadMore class="mt-2" :has-more="hasOlder" :loading="loading" @load="loadOlder()" />
      <div v-if="shown && !replaceBody" class="mt-2 space-y-1">
        <h3 class="font-medium">Version {{ shown.version }}: {{ shown.title }}</h3>
        <div class="bg-sunken p-3">
          <PageFrame v-if="shown.page" :key="shown.version" :html="shown.page" :bytes="pageBytes(shown.page)" class="mb-3" />
          <NoteBody :text="shown.body ?? ''" :project="project" />
        </div>
      </div>
    </template>
  </section>
  <template v-if="replaceBody">
    <section v-if="shown">
      <h2 class="h2">Version {{ shown.version }}: {{ shown.title }}</h2>
      <div class="panel p-3">
        <PageFrame v-if="shown.page" :key="shown.version" :html="shown.page" :bytes="pageBytes(shown.page)" class="mb-3" />
        <NoteBody :text="shown.body ?? ''" :project="project" />
      </div>
    </section>
    <slot v-else />
  </template>
</template>
