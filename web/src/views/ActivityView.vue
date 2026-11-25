<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ErrorBanner from '../components/ErrorBanner.vue'
import EventRow from '../components/EventRow.vue'
import LoadState from '../components/LoadState.vue'
import ScopeSelect from '../components/ScopeSelect.vue'
import { NEWEST_EVENT, api, remainingLabel } from '../lib/api'
import { useListScope } from '../composables/useListScope'
import { subscribeEvents, type StreamState } from '../lib/stream'
import type { LorvikEvent } from '../lib/types'

const props = defineProps<{ slug: string }>()

const PAGE = 50
const scope = useListScope()
const project = computed(() => (scope.value === 'project' ? props.slug : undefined))

// GET /events returns oldest first. The log is held oldest first in `all`,
// shown newest first; "load older" pages backwards with before=<oldest position>,
// and the live stream appends newer events.
const all = ref<LorvikEvent[]>([])
const olderCursor = ref('')
const olderRemaining = ref(0)
const loading = ref(true)
const loadingOlder = ref(false)
const state = ref<StreamState>('connecting')
const error = ref<unknown>(null)

const rows = computed(() => [...all.value].reverse())

async function load(): Promise<void> {
  loading.value = true
  try {
    const page = await api.events({ project: project.value, before: NEWEST_EVENT, limit: PAGE })
    all.value = page.items
    olderCursor.value = page.prev ?? ''
    olderRemaining.value = page.remaining
    error.value = null
  } catch (e) {
    error.value = e
  } finally {
    loading.value = false
  }
}

async function loadOlder(): Promise<void> {
  if (!olderCursor.value || loadingOlder.value) return
  loadingOlder.value = true
  try {
    const page = await api.events({ project: project.value, before: olderCursor.value, limit: PAGE })
    all.value = [...page.items, ...all.value]
    olderCursor.value = page.prev ?? ''
    olderRemaining.value = page.remaining
  } catch (e) {
    error.value = e
  } finally {
    loadingOlder.value = false
  }
}

let stop: (() => void) | undefined

function connect(): void {
  stop?.()
  const newest = all.value[all.value.length - 1]?.position ?? ''
  stop = subscribeEvents(
    project.value ?? '',
    newest,
    (e) => {
      if (!all.value.some((x) => x.id === e.id)) all.value.push(e)
    },
    (s) => (state.value = s),
  )
}

async function start(): Promise<void> {
  stop?.()
  await load()
  connect()
}

onMounted(start)
watch([() => props.slug, scope], start)
onBeforeUnmount(() => stop?.())
</script>

<template>
  <div class="page">
    <h1 class="page-title">Activity</h1>
    <p class="meta inline-flex items-center gap-1.5" role="status">
      <span class="swatch" :style="{ '--c': state === 'live' ? 'var(--color-st-done)' : 'var(--color-st-backlog)' }"></span>
      {{ state === 'live' ? 'live' : state === 'polling' ? 'updating every 15s' : 'connecting' }}
    </p>
    <div class="toolbar">
      <ScopeSelect v-model="scope" />
    </div>
    <ErrorBanner :error="error" :retry="load" />
    <LoadState :loading="loading" :empty="!rows.length && !error" empty-text="No events yet." />
    <ul v-if="rows.length" class="list">
      <EventRow v-for="e in rows" :key="e.id" :event="e" :slug="slug" :show-project="scope === 'all'" />
    </ul>
    <button v-if="olderCursor && olderRemaining > 0" type="button" class="btn" :disabled="loadingOlder" @click="loadOlder">
      Load older ({{ remainingLabel(olderRemaining) }} more)
    </button>
  </div>
</template>
