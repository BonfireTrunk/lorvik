<script setup lang="ts">
import { computed } from 'vue'

// One badge for every lifecycle word: card, decision, note, agent and token
// statuses share the six card tones. Unknown words render quiet.
const TONE: Record<string, string> = {
  backlog: 'backlog',
  ready: 'ready',
  working: 'working',
  waiting: 'waiting',
  done: 'done',
  dropped: 'dropped',
  proposed: 'working',
  accepted: 'done',
  superseded: 'dropped',
  revoked: 'dropped',
  rejected: 'dropped',
  active: 'done',
  pending: 'working',
  archived: 'backlog',
}

const props = defineProps<{ status: string }>()
const colour = computed(() => `var(--color-st-${TONE[props.status] ?? 'backlog'})`)
</script>

<template>
  <span class="inline-flex items-center gap-1.5 text-sm font-medium whitespace-nowrap">
    <span class="swatch" :style="{ '--c': colour }"></span>{{ status }}
  </span>
</template>
