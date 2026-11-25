<script setup lang="ts">
import { computed } from 'vue'
import type { BoxHit } from '../../composables/useMapBoxHits'
import { groupLabel } from '../../lib/map/collapse'
import CardLink from '../card/CardLink.vue'

const props = defineProps<{ slug: string; hits: BoxHit[] }>()
const emit = defineEmits<{ card: [number: number]; open: [key: string] }>()

const boxName = (h: BoxHit): string => (h.box.kind === 'story' ? `#${h.box.number} ${groupLabel(h.box)}` : groupLabel(h.box))
const heard = computed(() => (props.hits.length ? `${props.hits.length} ${props.hits.length === 1 ? 'match' : 'matches'} inside closed boxes` : ''))
</script>

<template>
  <p class="sr-only" role="status">{{ heard }}</p>
  <section v-if="hits.length" class="float pointer-events-auto w-full overflow-hidden" aria-label="Matches inside closed boxes">
    <p class="border-b border-line px-3 py-1.5 text-xs text-muted" aria-hidden="true">{{ heard }}</p>
    <ul class="max-h-64 divide-y divide-line overflow-y-auto text-sm">
      <li v-for="h in hits" :key="h.card.number" class="flex items-start gap-2 px-3 py-1.5">
        <CardLink :slug="slug" :number="h.card.number" class="min-w-0 flex-1 hover:underline" @open="emit('card', h.card.number)">
          <span class="ident text-muted">#{{ h.card.number }}</span> {{ h.card.title }}
          <span class="block truncate text-xs text-muted">in {{ boxName(h) }}</span>
        </CardLink>
        <button type="button" class="btn shrink-0" @click="emit('open', h.box.key)">Open box</button>
      </li>
    </ul>
  </section>
</template>
