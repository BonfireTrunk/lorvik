<script setup lang="ts">
import { ref } from 'vue'
import { useAction, useCanSubmit } from '../../composables/useAction'
import { ApiError, api } from '../../lib/api'
import { sendOnModEnter } from '../../lib/sendKey'
import ErrorBanner from '../ErrorBanner.vue'
import type { Card } from '../../lib/types'
import MarkdownHint from '../MarkdownHint.vue'

const props = defineProps<{ card: Card }>()
const emit = defineEmits<{ changed: [] }>()

const text = ref('')
const decide = ref(false)
const { busy, error, run } = useAction()
const canSubmit = useCanSubmit(busy, text)

async function submit(answer: string): Promise<void> {
  await run(
    async () => {
      await api.answer(props.card.project, props.card.number, {
        version: props.card.version,
        answer,
        decide: decide.value || undefined,
      })
      text.value = ''
      emit('changed')
    },
    { onError: (e) => e instanceof ApiError && e.isConflict && emit('changed') },
  )
}

const onKeydown = sendOnModEnter(() => canSubmit.value, () => void submit(text.value.trim()))
</script>

<template>
  <div class="space-y-2 border-t-2 border-fg pt-3">
    <h2 class="h2">Answer this question</h2>
    <div v-if="card.question?.choices?.length" class="flex flex-wrap gap-x-2 gap-y-1.5">
      <button v-for="c in card.question.choices" :key="c" type="button" class="btn" :disabled="busy" @click="submit(c)">{{ c }}</button>
    </div>
    <form class="space-y-1.5" @submit.prevent="submit(text.trim())">
      <label class="label" for="ans">{{ card.question?.choices?.length ? 'Or write your own' : 'Answer' }}</label>
      <textarea id="ans" v-model="text" class="field" rows="3" aria-describedby="ans-hint" @keydown="onKeydown"></textarea>
      <MarkdownHint id="ans-hint">Ctrl+Enter sends.</MarkdownHint>
      <label class="flex items-center gap-2 text-sm">
        <input v-model="decide" type="checkbox" /> Record as decision
      </label>
      <button type="submit" class="btn btn-primary" :disabled="!canSubmit">Submit answer</button>
    </form>
    <ErrorBanner :error="error" />
  </div>
</template>
