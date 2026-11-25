<script setup lang="ts">
import { ref } from 'vue'
import { STATUS_ORDER } from '../../lib/cards'
import IconChevronDown from '~icons/lucide/chevron-down'
import IconChevronUp from '~icons/lucide/chevron-up'
import IconMinus from '~icons/lucide/minus'
import IconPlus from '~icons/lucide/plus'
import IconScan from '~icons/lucide/scan'

defineProps<{ zoom: number }>()
const emit = defineEmits<{ zoomIn: []; zoomOut: []; fit: []; actual: [] }>()

const legend = ref(false)
const LINES = [
  { kind: 'blocks', label: 'Blocks: the arrow points at the blocked card' },
  { kind: 'relates', label: 'Relates to' },
  { kind: 'applies', label: 'Decision applies to' },
  { kind: 'duplicates', label: 'Duplicates or supersedes' },
  { kind: 'mention', label: 'Note mentions the card' },
]
</script>

<template>
  <div class="absolute bottom-3 left-3 z-10 flex flex-col items-start gap-2">
    <section v-if="legend" id="map-legend" class="float w-72 p-3 text-sm" aria-label="Legend">
      <h2 class="mb-1.5 text-xs font-semibold text-muted uppercase">Status</h2>
      <ul class="mb-3 grid grid-cols-2 gap-x-3 gap-y-1">
        <li v-for="s in STATUS_ORDER" :key="s" class="flex items-center gap-1.5 capitalize"><span class="swatch" :style="{ '--c': `var(--color-st-${s})` }" aria-hidden="true"></span>{{ s }}</li>
      </ul>
      <h2 class="mb-1.5 text-xs font-semibold text-muted uppercase">Lines</h2>
      <ul class="space-y-1.5">
        <li v-for="l in LINES" :key="l.kind" class="flex items-center gap-2">
          <svg class="map-edge" :class="`map-edge-${l.kind}`" width="40" height="10" viewBox="0 0 40 10" aria-hidden="true">
            <path d="M2 5 H32" class="map-edge-line" />
            <path v-if="l.kind === 'blocks'" d="M38 5 L31 1.5 V8.5Z" class="map-edge-head" />
          </svg>
          {{ l.label }}
        </li>
      </ul>
      <p class="mt-3 flex items-center gap-2 text-xs text-muted"><span class="inline-block h-3 w-5 rounded-sm" style="background: var(--color-question)" aria-hidden="true"></span>Needs you: waits on a human</p>
    </section>
    <button type="button" class="btn float h-8 bg-panel px-2.5" :aria-expanded="legend" aria-controls="map-legend" @click="legend = !legend">
      <component :is="legend ? IconChevronDown : IconChevronUp" />Legend
    </button>
    <div class="float flex items-center gap-0.5 p-0.5" role="group" aria-label="Zoom">
      <button type="button" class="btn btn-quiet btn-icon" aria-label="Zoom out" title="Zoom out (-)" aria-keyshortcuts="-" @click="emit('zoomOut')"><IconMinus /></button>
      <output class="w-12 text-center text-sm tabular" aria-label="Zoom level">{{ Math.round(zoom * 100) }}%</output>
      <button type="button" class="btn btn-quiet btn-icon" aria-label="Zoom in" title="Zoom in (+)" aria-keyshortcuts="+" @click="emit('zoomIn')"><IconPlus /></button>
      <span class="mx-1 h-5 w-px bg-line" aria-hidden="true"></span>
      <button type="button" class="btn btn-quiet" title="Fit everything (f)" aria-keyshortcuts="f" @click="emit('fit')"><IconScan />Fit</button>
      <button type="button" class="btn btn-quiet" title="Actual size" @click="emit('actual')">100%</button>
    </div>
  </div>
</template>
