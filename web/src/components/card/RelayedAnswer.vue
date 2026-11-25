<script setup lang="ts">
import { ref } from 'vue'
import ActorName from '../ActorName.vue'
import AnswerForm from './AnswerForm.vue'
import NoteBody from '../NoteBody.vue'
import TimeAgo from '../TimeAgo.vue'
import type { Card } from '../../lib/types'

defineProps<{ card: Card; canAnswer: boolean }>()
const emit = defineEmits<{ changed: [] }>()

const changing = ref(false)

function changed(): void {
  changing.value = false
  emit('changed')
}
</script>

<template>
  <section class="space-y-2">
    <h2 class="h2">Answer relayed by <ActorName :who="card.question?.answered_by" /></h2>
    <div class="bg-sunken p-3">
      <NoteBody :text="card.question?.answer ?? ''" :project="card.project" />
      <p class="meta mt-1">
        <TimeAgo v-if="card.question?.answered_at" :at="card.question.answered_at" />
      </p>
    </div>
    <template v-if="canAnswer">
      <div v-if="!changing" class="flex flex-wrap gap-2">
        <button type="button" class="btn" @click="changing = true">Change</button>
      </div>
      <template v-else>
        <AnswerForm :card="card" @changed="changed" />
        <button type="button" class="btn" @click="changing = false">Cancel</button>
      </template>
    </template>
  </section>
</template>
