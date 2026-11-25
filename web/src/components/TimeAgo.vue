<script setup lang="ts">
import { absoluteTime, relativeTime, timeRemaining } from '../lib/time'

defineProps<{ at: string; now?: number; remaining?: boolean }>()
</script>

<template>
  <time :datetime="at" :title="absoluteTime(at)">{{ remaining ? timeRemaining(at, now) : relativeTime(at, now) }}</time>
</template>
