<script setup lang="ts">
import { remainingLabel } from '../lib/api'

// remaining is left out by lists that report no count.
defineProps<{ hasMore: boolean; loading: boolean; remaining?: number }>()
const emit = defineEmits<{ load: [] }>()
</script>

<template>
  <button v-if="hasMore" type="button" class="btn" :disabled="loading" @click="emit('load')">
    Load more<template v-if="remaining !== undefined"> ({{ remainingLabel(remaining) }} left)</template>
  </button>
</template>
