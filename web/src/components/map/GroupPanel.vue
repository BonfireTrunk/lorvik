<script setup lang="ts">
import { computed } from 'vue'
import { useGroupList } from '../../composables/useGroupList'
import { stateWord } from '../../lib/cards'
import { groupLabel } from '../../lib/map/collapse'
import type { CardStatus, MapGroup } from '../../lib/types'
import CardLink from '../card/CardLink.vue'
import ErrorBanner from '../ErrorBanner.vue'
import LoadMore from '../LoadMore.vue'
import LoadState from '../LoadState.vue'
import StatusBadge from '../StatusBadge.vue'
import IconLayers from '~icons/lucide/layers'
import IconList from '~icons/lucide/list'
import IconMaximize from '~icons/lucide/maximize-2'
import IconX from '~icons/lucide/x'

const props = defineProps<{
  slug: string
  group: MapGroup
  statuses: CardStatus[]
  /** Cards of the group the status filter lets through. */
  shown: number
  /** Whether the group opens on the canvas with the boxes already open, or only as a map of its own. */
  fits: boolean
}>()
const emit = defineEmits<{ close: []; expand: []; card: [number: number] }>()

const list = useGroupList(() => props.slug, () => props.group.key, () => props.statuses)
const isStory = computed(() => props.group.kind === 'story')
const title = computed(() => groupLabel(props.group))
</script>

<template>
  <aside class="panel flex w-96 max-w-[40%] min-w-72 shrink-0 flex-col overflow-hidden" aria-labelledby="group-title">
    <div class="flex items-start gap-2 border-b border-line px-3 py-2.5">
      <component :is="isStory ? IconLayers : IconList" class="mt-1 h-4 w-4 shrink-0 text-muted" />
      <div class="min-w-0 flex-1">
        <h2 id="group-title" class="flex items-baseline gap-1.5 text-base font-semibold">
          <span v-if="isStory" class="ident shrink-0 text-muted">#{{ group.story }}</span><span v-else-if="group.lane" class="sr-only">Lane </span><span class="truncate" :title="title">{{ title }}</span>
        </h2>
        <p class="text-xs text-muted tabular">{{ shown }} {{ shown === 1 ? 'card' : 'cards' }}</p>
      </div>
      <button type="button" class="btn btn-quiet btn-icon" aria-label="Close the list" title="Close the list" @click="emit('close')"><IconX /></button>
    </div>
    <div class="border-b border-line px-3 py-2">
      <button type="button" class="btn w-full justify-center" @click="emit('expand')">
        <IconMaximize class="h-4 w-4" />{{ fits ? 'Expand on the map' : 'Open as a map of its own' }}
      </button>
      <p v-if="!fits" class="mt-1.5 text-xs text-muted">Too many cards to open next to the other boxes.</p>
    </div>
    <ErrorBanner class="m-3" :error="list.error.value" :retry="list.retry" />
    <div class="min-h-0 flex-1 overflow-y-auto">
      <LoadState class="p-3" :loading="list.loading.value && !list.items.value.length" :empty="!list.loading.value && !list.error.value && !list.items.value.length" empty-text="No cards in the statuses shown." />
      <ul v-if="list.items.value.length" class="divide-y divide-line">
        <li v-for="c in list.items.value" :key="c.number">
          <CardLink :slug="slug" :number="c.number" class="block px-3 py-2 hover:bg-sunken" @open="emit('card', c.number)">
            <span class="flex items-center gap-2 text-xs text-muted">
              <span class="ident">#{{ c.number }}</span>
              <StatusBadge :status="stateWord(c.kind, c.status, c.drop_reason, c.decision)" class="text-xs" />
              <span v-if="c.lane" class="truncate">{{ c.lane }}</span>
            </span>
            <span class="mt-0.5 block text-sm">{{ c.title }}</span>
          </CardLink>
        </li>
      </ul>
      <div class="p-3">
        <LoadMore :has-more="!!list.next.value" :loading="list.loading.value" @load="list.more" />
      </div>
    </div>
  </aside>
</template>
