<script setup lang="ts">
import ErrorBanner from '../ErrorBanner.vue'
import { useAction } from '../../composables/useAction'
import { ApiError, api } from '../../lib/api'
import type { Card } from '../../lib/types'

const props = defineProps<{ card: Card }>()
const emit = defineEmits<{ changed: [] }>()

const { busy, error, run } = useAction()

async function clear(): Promise<void> {
  await run(
    async () => {
      await api.markForPerson(props.card.project, props.card.number, { version: props.card.version, clear: true })
      emit('changed')
    },
    { onError: (e) => e instanceof ApiError && e.isConflict && emit('changed') },
  )
}
</script>

<template>
  <div class="space-y-1.5">
    <p class="state">Only a person may answer this question: no agent can answer it.</p>
    <button type="button" class="btn" :disabled="busy" @click="clear">Let agents answer it</button>
    <ErrorBanner :error="error" />
  </div>
</template>
