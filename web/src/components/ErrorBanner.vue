<script setup lang="ts">
import { computed } from 'vue'
import { errorText } from '../lib/api'

// retry, when given, adds a Retry button that re-runs the failed load.
const props = defineProps<{ error: unknown; retry?: () => void }>()
const text = computed(() => errorText(props.error))
</script>

<template>
  <div
    v-if="error"
    role="alert"
    class="panel flex items-start gap-3 border-danger px-3 py-2 text-sm"
  >
    <div class="min-w-0 flex-1">
      <p class="font-medium text-danger">{{ text.detail }}</p>
      <p v-if="text.hint" class="mt-0.5 text-muted">{{ text.hint }}</p>
    </div>
    <button v-if="retry" type="button" class="btn shrink-0" @click="retry">Retry</button>
  </div>
</template>
