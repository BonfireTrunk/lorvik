<script setup lang="ts">
import { ref } from 'vue'
import { useAction } from '../../composables/useAction'
import { ApiError, api } from '../../lib/api'
import ErrorBanner from '../ErrorBanner.vue'
import type { Card } from '../../lib/types'

const props = defineProps<{ card: Card }>()
const emit = defineEmits<{ changed: []; close: [] }>()

const branch = ref(props.card.branch ?? '')
const lane = ref(props.card.lane ?? '')
const { busy, error, run } = useAction()

async function save(): Promise<void> {
  await run(
    async () => {
      await api.updateCard(props.card.project, props.card.number, {
        version: props.card.version,
        branch: branch.value.trim(),
        lane: lane.value.trim(),
      })
      emit('changed')
      emit('close')
    },
    { onError: (e) => e instanceof ApiError && e.isConflict && emit('changed') },
  )
}
</script>

<template>
  <form class="space-y-2 bg-sunken p-3" @submit.prevent="save">
    <div>
      <label class="label" for="card-branch">Branch</label>
      <input id="card-branch" v-model="branch" class="field font-mono" maxlength="200" placeholder="card-42-fix-login" />
    </div>
    <div>
      <label class="label" for="card-lane">Lane</label>
      <input id="card-lane" v-model="lane" class="field font-mono" maxlength="40" placeholder="schema, generated, goldens, web" />
      <p class="meta mt-0.5">
        No two cards in one lane are handed out at once: <code class="font-mono">lorvik next</code> skips a card whose lane has a working card.
      </p>
    </div>
    <div class="flex gap-x-2">
      <button type="submit" class="btn btn-primary" :disabled="busy">Save</button>
      <button type="button" class="btn btn-quiet" @click="emit('close')">Cancel</button>
    </div>
    <ErrorBanner :error="error" />
  </form>
</template>
