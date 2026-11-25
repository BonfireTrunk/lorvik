<script setup lang="ts">
import type { Actor } from '../lib/types'
import { actorName, actorResolved } from '../lib/actors'

defineProps<{ who: Actor | string | undefined | null }>()
</script>

<template>
  <span :class="actorResolved(who) ? '' : 'opacity-60'">{{ actorName(who) }}</span>
</template>
