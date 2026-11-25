<script setup lang="ts">
import { computed } from 'vue'
import type { MapProgress } from '../../composables/useMapData'

/** `blocking` dims the map and takes its clicks, until the first cards are drawn; after that a small note in the corner keeps the map usable. */
const props = defineProps<{ progress: MapProgress; blocking: boolean }>()

const pct = computed(() => (props.progress.total ? Math.min(100, Math.round((props.progress.done / props.progress.total) * 100)) : 0))
</script>

<template>
  <div v-if="blocking" class="absolute inset-0 z-20 grid place-items-center bg-canvas/70" role="status">
    <div class="float w-72 p-4">
      <p class="text-sm font-medium">{{ progress.label }}…</p>
      <div class="mt-2 h-1.5 overflow-hidden rounded-sm bg-sunken" role="progressbar" :aria-valuenow="pct" aria-valuemin="0" aria-valuemax="100" :aria-label="progress.label">
        <div class="h-full bg-accent transition-[width]" :style="{ width: `${pct}%` }"></div>
      </div>
      <p v-if="progress.total" class="mt-1.5 text-xs text-muted tabular">{{ progress.done }} of {{ progress.total }}</p>
    </div>
  </div>
  <p v-else class="float pointer-events-none absolute top-3 left-3 z-20 px-2.5 py-1 text-xs text-muted tabular" role="status">
    {{ progress.label }}<template v-if="progress.total">: {{ progress.done }} of {{ progress.total }}</template>
  </p>
</template>
