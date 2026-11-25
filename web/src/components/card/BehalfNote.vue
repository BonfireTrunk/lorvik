<script setup lang="ts">
import { computed, ref } from 'vue'
import ActorName from '../ActorName.vue'
import AnswerForm from './AnswerForm.vue'
import ErrorBanner from '../ErrorBanner.vue'
import NoteBody from '../NoteBody.vue'
import TimeAgo from '../TimeAgo.vue'
import { useAction } from '../../composables/useAction'
import { ApiError, api } from '../../lib/api'
import { behalfBy } from '../../lib/actors'
import { auth } from '../../lib/auth'
import { behalfVerb } from '../../lib/cards'
import type { Behalf, Card } from '../../lib/types'

// A card an agent settled for a person: who, for whom, why, and until the person looks, Keep and Change.
// A question is changed with the answer form; a decision with Revoke or Re-propose in the transition bar.
const props = defineProps<{ card: Card; behalf: Behalf; canAnswer: boolean }>()
const emit = defineEmits<{ changed: [] }>()

const changing = ref(false)
const { busy, error, run } = useAction()

const isDecision = computed(() => props.card.kind === 'decision')
const forMe = computed(() => props.behalf.for.id === auth.me?.actor.id)
const lowerVerb = computed(() => behalfVerb(props.card.kind, props.card.status).toLowerCase())

async function keep(): Promise<void> {
  await run(
    async () => {
      await api.keepCard(props.card.project, props.card.number, { version: props.card.version })
      emit('changed')
    },
    { onError: (e) => e instanceof ApiError && e.isConflict && emit('changed') },
  )
}

function changed(): void {
  changing.value = false
  emit('changed')
}
</script>

<template>
  <section class="space-y-2">
    <h2 class="h2">{{ behalfVerb(card.kind, card.status) }} by {{ behalfBy(behalf) }} for <ActorName :who="behalf.for" /></h2>
    <div class="space-y-1 bg-sunken p-3">
      <NoteBody v-if="!isDecision && card.question?.answer" :text="card.question.answer" :project="card.project" />
      <p class="break-words"><span class="font-medium">Reason:</span> {{ behalf.reason }}</p>
      <p class="meta">
        <template v-if="behalf.kept_at">Kept <TimeAgo :at="behalf.kept_at" /></template>
        <template v-else>Not looked at yet.</template>
      </p>
    </div>
    <template v-if="!behalf.kept_at">
      <div class="flex flex-wrap items-center gap-2">
        <button v-if="forMe" type="button" class="btn" :disabled="busy" @click="keep">Keep</button>
        <button v-if="!isDecision && canAnswer && !changing" type="button" class="btn" @click="changing = true">Change</button>
      </div>
      <p v-if="isDecision && canAnswer" class="meta">To change it, use {{ lowerVerb === 'accepted' ? 'Revoke' : 'Re-propose' }} above.</p>
      <template v-if="changing">
        <AnswerForm :card="card" @changed="changed" />
        <button type="button" class="btn" @click="changing = false">Cancel</button>
      </template>
    </template>
    <ErrorBanner :error="error" />
  </section>
</template>
