<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useEdgeLinks } from '../../composables/useEdgeLinks'
import type { GraphEdge } from '../../lib/map/types'
import CardLink from '../card/CardLink.vue'
import ErrorBanner from '../ErrorBanner.vue'
import LoadState from '../LoadState.vue'
import IconX from '~icons/lucide/x'

const SHOWN = 20

const props = defineProps<{ slug: string; edge: GraphEdge }>()
const emit = defineEmits<{ close: []; card: [number: number] }>()

const box = ref<HTMLElement | null>(null)
let openedBy = ''
onMounted(() => {
  const active = document.activeElement
  if (active instanceof SVGElement || active instanceof HTMLElement) openedBy = active.dataset.edgeLabel ?? ''
  box.value?.focus()
})
// Focus goes back to the line's label that opened the list. A new layout may have redrawn the label, so it is looked up again.
onBeforeUnmount(() => {
  if (openedBy) document.querySelector<SVGElement>(`[data-edge-label="${CSS.escape(openedBy)}"]`)?.focus()
})

const list = useEdgeLinks(() => props.slug, () => props.edge)
const rows = ref(SHOWN)
watch(() => props.edge.id, () => (rows.value = SHOWN))
const shown = computed(() => list.rows.value.slice(0, rows.value))
</script>

<template>
  <section ref="box" tabindex="-1" class="float absolute top-3 left-3 z-20 flex max-h-[min(24rem,60%)] w-[min(28rem,calc(100%-1.5rem))] flex-col overflow-hidden" aria-labelledby="edge-links-title" @keydown.esc.stop="emit('close')">
    <div class="flex items-center gap-2 border-b border-line px-3 py-2">
      <h2 id="edge-links-title" class="min-w-0 flex-1 truncate text-sm font-semibold">{{ edge.merged?.count }} {{ edge.kind }} {{ edge.merged?.count === 1 ? 'link' : 'links' }}</h2>
      <button type="button" class="btn btn-quiet btn-icon" aria-label="Close the links" title="Close the links" @click="emit('close')"><IconX /></button>
    </div>
    <div class="min-h-0 flex-1 overflow-y-auto">
      <ErrorBanner class="m-2" :error="list.error.value" :retry="list.retry" />
      <LoadState class="p-3" :loading="list.loading.value" :empty="!list.error.value && !list.rows.value.length" empty-text="No links found." />
      <ul v-if="shown.length" class="divide-y divide-line text-sm">
        <li v-for="(r, i) in shown" :key="i" class="grid grid-cols-[1fr_auto_1fr] items-start gap-2 px-3 py-1.5">
          <CardLink :slug="slug" :number="r.from.number" class="min-w-0 hover:underline" @open="emit('card', r.from.number)">
            <span class="ident text-muted">#{{ r.from.number }}</span> {{ r.from.title }}
          </CardLink>
          <span class="text-muted" aria-hidden="true">{{ edge.kind === 'relates' || edge.kind === 'duplicates' ? '↔' : '→' }}</span>
          <CardLink :slug="slug" :number="r.to.number" class="min-w-0 hover:underline" @open="emit('card', r.to.number)">
            <span class="ident text-muted">#{{ r.to.number }}</span> {{ r.to.title }}
          </CardLink>
        </li>
      </ul>
      <div v-if="list.rows.value.length > rows" class="p-2">
        <button type="button" class="btn w-full justify-center" @click="rows += SHOWN">Show more ({{ list.rows.value.length - rows }} left)</button>
      </div>
    </div>
  </section>
</template>
