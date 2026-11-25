<script setup lang="ts">
import type { CardKind } from '../lib/types'

defineProps<{ kind: CardKind }>()
</script>

<template>
  <span class="badge" :class="kind === 'question' || kind === 'decision' ? 'badge-question' : ''"><slot>{{ kind }}</slot></span>
</template>
