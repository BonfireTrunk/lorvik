<script setup lang="ts">
import { computed } from 'vue'
import { actorName, holderChat } from '../../lib/actors'
import { decisionWord } from '../../lib/cards'
import type { CardBrief } from '../../lib/types'
import IconPage from '~icons/lucide/app-window'
import ActorName from '../ActorName.vue'
import KindBadge from '../KindBadge.vue'
import CardLink from './CardLink.vue'
import PriorityBadge from '../PriorityBadge.vue'

// stories maps a story's number to its title, for the parent chip.
const props = defineProps<{
  card: CardBrief
  slug: string
  stories?: Map<number, string>
  /** What the viewer must do on this card; absent when it does not wait on them. */
  needs?: string
  newReply?: boolean
  /** The tile matches the board's search. */
  hit?: boolean
}>()
const emit = defineEmits<{ open: [number: number] }>()

const holder = computed(() => props.card.claim?.actor ?? props.card.assignee)
// The chat holding the claim as the tile names it: its name, or its label when
// it has none. It leads the line because the line is cut at the tile's width
// and the chat is what tells one agent's cards apart (#343 A).
const chat = computed(() => props.card.claim?.session_name || props.card.claim?.session || '')
const holderTitle = computed(() => {
  const claim = props.card.claim
  if (!claim) return 'Assigned'
  const by = claim.session ? `${actorName(claim.actor)} (${holderChat(claim.session, claim.session_name)})` : actorName(claim.actor)
  return `Claimed (lease held) by ${by}`
})
const isDecision = computed(() => props.card.kind === 'decision')
const parentTitle = computed(() => (props.card.parent ? props.stories?.get(props.card.parent) : undefined))
</script>

<template>
  <CardLink
    :slug="slug"
    :number="card.number"
    :data-card="card.number"
    class="block no-underline"
    :class="[needs ? 'needs-frame' : 'panel', 'hover:border-fg', { 'card-hit': hit }]"
    @open="emit('open', card.number)"
  >
    <div v-if="needs" class="needs-plate">Needs you: {{ needs }}</div>
    <div class="px-3 py-2.5">
      <div class="flex items-center gap-2 text-sm">
        <span class="ident text-muted">#{{ card.number }}</span>
        <KindBadge v-if="card.kind !== 'task'" class="badge-tight" :kind="card.kind" :title="isDecision ? 'Decision' : card.kind === 'story' ? 'Story' : undefined">{{ isDecision ? decisionWord(card.status, card.drop_reason, card.decision) : card.kind }}</KindBadge>
        <span v-if="card.page_bytes" class="inline-flex text-muted" title="Has a page"><IconPage class="h-3.5 w-3.5" aria-hidden="true" /><span class="sr-only">Has a page</span></span>
        <span v-if="newReply" class="badge badge-tight badge-question" title="Someone replied since you last opened this card">New reply</span>
        <span class="ml-auto"><PriorityBadge v-if="card.priority !== 'normal'" class="badge-tight" :priority="card.priority" /></span>
      </div>
      <div v-if="card.parent" class="mt-1.5 flex min-w-0 items-center gap-1.5 text-sm text-muted" title="Parent story">
        <span class="ident shrink-0">S#{{ card.parent }}</span>
        <span v-if="parentTitle" class="truncate">{{ parentTitle }}</span>
      </div>
      <div class="mt-2 text-base leading-snug font-medium break-words">{{ card.title }}</div>
      <div v-if="card.lane || card.branch || holder" class="mt-3 space-y-2">
        <div v-if="card.lane || card.branch" class="flex flex-wrap items-center gap-2 text-sm text-muted">
          <span v-if="card.lane" class="badge badge-tight" title="Lane: one working card at a time">{{ card.lane }}</span>
          <span v-if="card.branch" class="font-mono break-all" title="Git branch">{{ card.branch }}</span>
        </div>
        <div v-if="holder" class="flex items-center gap-1.5 text-sm text-muted" :title="holderTitle">
          <span v-if="card.claim" class="swatch h-2 w-2" style="--c: var(--color-accent)" aria-hidden="true"></span>
          <span class="sr-only">{{ card.claim ? 'Claimed by' : 'Assigned to' }}</span>
          <span class="truncate"><template v-if="chat">{{ chat }}<span aria-hidden="true"> · </span></template><ActorName :who="holder" /></span>
        </div>
      </div>
    </div>
  </CardLink>
</template>
