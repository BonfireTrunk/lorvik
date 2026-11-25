<script setup lang="ts">
import { computed } from 'vue'
import type { Priority } from '../lib/types'

const props = defineProps<{ priority: Priority }>()
const tone = computed(() =>
  props.priority === 'urgent' || props.priority === 'high' ? 'badge-signal' : '',
)
</script>

<template>
  <span class="badge" :class="[tone, priority === 'urgent' ? 'font-semibold' : '']" title="Priority">{{ priority }}</span>
</template>
