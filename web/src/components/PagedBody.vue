<script setup lang="ts">
import { nextTick, ref, useId, watch } from 'vue'
import type { HTMLPage } from '../lib/types'
import ErrorBanner from './ErrorBanner.vue'
import LoadState from './LoadState.vue'
import PageFrame from './PageFrame.vue'

// A card's or note's content: the agent-written page first, the Markdown after
// it. With both, two tabs (Page selected); with only a page, the page alone.
// The page is fetched when this mounts and again when the version changes.
const props = defineProps<{
  /** Page size from the card or note; 0 or absent when it has no page. */
  pageBytes?: number
  /** The card or note version; a new one refetches the page. */
  version: number
  hasText: boolean
  load: () => Promise<HTMLPage>
}>()

const page = ref<HTMLPage | null>(null)
const error = ref<unknown>(null)
const tab = ref<'page' | 'text'>('page')
const id = useId()
const tabs = ['page', 'text'] as const
let token = 0

async function fetchPage(): Promise<void> {
  const mine = ++token
  error.value = null
  try {
    const p = await props.load()
    if (mine === token) page.value = p
  } catch (e) {
    if (mine === token) error.value = e
  }
}

watch(
  () => [props.pageBytes ?? 0, props.version],
  ([bytes]) => {
    if (bytes) void fetchPage()
    else {
      token++
      page.value = null
      error.value = null
    }
  },
  { immediate: true },
)

function onKeydown(e: KeyboardEvent): void {
  const i = tabs.indexOf(tab.value)
  let next = i
  if (e.key === 'ArrowRight') next = (i + 1) % tabs.length
  else if (e.key === 'ArrowLeft') next = (i + tabs.length - 1) % tabs.length
  else if (e.key === 'Home') next = 0
  else if (e.key === 'End') next = tabs.length - 1
  else return
  e.preventDefault()
  tab.value = tabs[next]
  void nextTick(() => document.getElementById(`${id}-tab-${tabs[next]}`)?.focus())
}
</script>

<template>
  <section v-if="pageBytes">
    <h2 v-if="!hasText" class="sr-only">Page</h2>
    <div v-else class="mb-3 flex gap-1 border-b border-line" role="tablist" aria-label="Content" @keydown="onKeydown">
      <button
        v-for="t in tabs"
        :id="`${id}-tab-${t}`"
        :key="t"
        type="button"
        role="tab"
        class="-mb-px cursor-pointer border-b-2 px-3 py-1.5 text-sm font-medium"
        :class="tab === t ? 'border-fg text-fg' : 'border-transparent text-muted hover:text-fg'"
        :aria-selected="tab === t"
        :aria-controls="`${id}-panel-${t}`"
        :tabindex="tab === t ? 0 : -1"
        @click="tab = t"
      >
        {{ t === 'page' ? 'Page' : 'Text' }}
      </button>
    </div>
    <div
      v-show="!hasText || tab === 'page'"
      :id="`${id}-panel-page`"
      :role="hasText ? 'tabpanel' : undefined"
      :aria-labelledby="hasText ? `${id}-tab-page` : undefined"
    >
      <ErrorBanner :error="error" :retry="fetchPage" />
      <LoadState :loading="!page && !error" />
      <PageFrame v-if="page" :html="page.html" :bytes="page.bytes" />
    </div>
    <div
      v-if="hasText"
      v-show="tab === 'text'"
      :id="`${id}-panel-text`"
      role="tabpanel"
      :aria-labelledby="`${id}-tab-text`"
    >
      <slot />
    </div>
  </section>
  <section v-else-if="hasText">
    <slot />
  </section>
</template>
