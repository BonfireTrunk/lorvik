<script setup lang="ts">
import { computed, ref, type Component } from 'vue'
import IconArchive from '~icons/lucide/archive'
import IconBan from '~icons/lucide/ban'
import IconCheck from '~icons/lucide/check'
import IconCircleDot from '~icons/lucide/circle-dot'
import IconPause from '~icons/lucide/pause'
import IconPlay from '~icons/lucide/play'
import IconRotateCcw from '~icons/lucide/rotate-ccw'
import IconUndo from '~icons/lucide/undo-2'
import IconX from '~icons/lucide/x'
import { moveCard } from '../../composables/useCardActions'
import { useAction } from '../../composables/useAction'
import { useDropForm } from '../../composables/useDropForm'
import { useFoldForm } from '../../composables/useFoldForm'
import { ApiError } from '../../lib/api'
import { auth } from '../../lib/auth'
import { cardActions, isFoldOptional, type CardAction, type CardActionId } from '../../lib/cardActions'
import { isClosed, STATUS_ORDER } from '../../lib/cards'
import { canHumanIn, canIn } from '../../lib/roles'
import ErrorBanner from '../ErrorBanner.vue'
import DropFields from './DropFields.vue'
import FoldFields from './FoldFields.vue'
import type { Card, CardStatus, TransitionCard } from '../../lib/types'

const props = defineProps<{ card: Card }>()
const emit = defineEmits<{ changed: [] }>()

const { busy, error, run } = useAction()
const dropping = ref(false)
const folding = ref(false)
const dropForm = useDropForm(() => props.card)
const { fields: dropFields, ready: dropReady, showCascade } = dropForm
const foldOptional = computed(() => isFoldOptional(props.card.kind))
const foldForm = useFoldForm(() => foldOptional.value)
const { fields: foldFields, canSubmit: foldCanSubmit } = foldForm
const isDecision = computed(() => props.card.kind === 'decision')
const canDecide = computed(() => canHumanIn(props.card.project, 'reviewer'))
const isProposer = computed(() => {
  const me = auth.me?.actor
  return !!me && me.kind === props.card.created_by.kind && me.id === props.card.created_by.id
})
const canMove = computed(() => canIn(props.card.project, 'member'))
// Every child settled: closing with the fold is what the story needs next.
const storySettled = computed(() => {
  const p = props.card.progress
  return props.card.kind === 'story' && !!p && p.total > 0 && p.done === p.total
})

interface Action extends CardAction {
  primary: boolean
  icon: Component
}

const ICONS: Partial<Record<CardActionId, Component>> = {
  backlog: IconArchive,
  ready: IconCircleDot,
  start: IconPlay,
  wait: IconPause,
  done: IconCheck,
  drop: IconBan,
  accept: IconCheck,
  reject: IconX,
  revoke: IconUndo,
  repropose: IconRotateCcw,
}

// The move a card usually takes next is the one cobalt button: Done while it
// is being worked, else Start, else Ready. A started story's status follows its
// children, so no plain move advances it: Ready would only step it back.
function forward(s: CardStatus, offered: CardStatus[], kind: Card['kind']): CardStatus | undefined {
  if (kind === 'story' && s !== 'backlog') return undefined
  if (s === 'working' && offered.includes('done')) return 'done'
  if (offered.includes('working')) return 'working'
  if (offered.includes('ready')) return 'ready'
  return undefined
}

const actions = computed<Action[]>(() => {
  const offered = cardActions(props.card.kind, props.card.status, canMove.value, !!props.card.parent, {
    canDecide: canDecide.value,
    isProposer: isProposer.value,
    dropReason: props.card.drop_reason,
  }).filter((a) => a.id !== 'comment')
  if (!isDecision.value) {
    offered.sort((a, b) => STATUS_ORDER.indexOf(a.to!) - STATUS_ORDER.indexOf(b.to!))
  }
  const next = forward(props.card.status, offered.filter((a) => !a.prompt || a.foldOptional).map((a) => a.to!), props.card.kind)
  return offered.map((a) => ({
    ...a,
    primary: a.id === 'accept' || (a.prompt === 'fold' && storySettled.value) || (!isDecision.value && (!a.prompt || !!a.foldOptional) && a.to === next),
    icon: a.id === 'ready' && isClosed(props.card.status) ? IconRotateCcw : ICONS[a.id]!,
  }))
})
const canClose = computed(() => actions.value.some((a) => a.prompt === 'fold'))

async function transition(body: Omit<TransitionCard, 'version'>): Promise<void> {
  const ok = await run(() => moveCard(props.card, body), {
    onError: (e) => {
      if (e instanceof ApiError && e.isConflict) emit('changed')
    },
  })
  if (!ok) return
  dropping.value = false
  folding.value = false
  foldForm.reset()
  dropForm.clearAfterMove()
  emit('changed')
}

function act(a: Action): void {
  if (a.prompt === 'fold') folding.value = !folding.value
  else if (a.prompt === 'drop') dropping.value = !dropping.value
  else void transition({ status: a.to!, drop_reason: a.reason })
}

function drop(): void {
  const body = dropForm.body()
  if (body) void transition(body)
}

function close(): void {
  const body = foldForm.doneBody()
  if (body) void transition(body)
}
</script>

<template>
  <div v-if="actions.length" class="space-y-2">
    <div class="flex flex-wrap items-center gap-1.5">
      <button
        v-for="a in actions"
        :key="a.id"
        type="button"
        :class="a.prompt === 'drop' ? 'btn btn-danger ml-auto' : a.primary ? 'btn btn-primary' : 'btn'"
        :disabled="busy"
        :aria-expanded="a.prompt === 'fold' ? folding : a.prompt === 'drop' ? dropping : undefined"
        :aria-keyshortcuts="a.key || undefined"
        @click="act(a)"
      >
        <component :is="a.icon" />{{ a.label }}<kbd v-if="a.key" class="kbd">{{ a.key }}</kbd>
      </button>
    </div>
    <form v-if="folding && canClose" class="flex flex-wrap items-end gap-2 bg-sunken p-3" @submit.prevent="close">
      <FoldFields v-model="foldFields" id-prefix="tb-fold" inline />
      <button type="submit" class="btn btn-primary" :disabled="busy || !foldCanSubmit">{{ foldOptional ? 'Mark done' : 'Close story' }}</button>
    </form>
    <form v-if="dropping" class="flex flex-wrap items-end gap-2 bg-sunken p-3" @submit.prevent="drop">
      <DropFields v-model="dropFields" id-prefix="tb-drop" inline :show-cascade="showCascade" />
      <button type="submit" class="btn btn-danger" :disabled="busy || !dropReady">Drop card</button>
    </form>
    <ErrorBanner :error="error" />
  </div>
</template>
