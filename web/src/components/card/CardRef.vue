<script setup lang="ts">
import StatusBadge from '../StatusBadge.vue'
import type { CardStatus } from '../../lib/types'

// A link to a card of a project, with its status and an optional relation label.
defineProps<{ slug: string; number: number; title: string; status: CardStatus; label?: string }>()
</script>

<template>
  <li>
    <span v-if="label" class="mr-2 text-sm text-muted">{{ label }}</span>
    <router-link class="lnk" :to="{ name: 'card', params: { slug, number } }"><span class="ident">#{{ number }}</span> {{ title }}</router-link>
    <StatusBadge class="ml-3" :status="status" />
  </li>
</template>
