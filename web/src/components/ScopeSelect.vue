<script setup lang="ts">
import { computed } from 'vue'
import { projects, type ListScope } from '../lib/projects'

const model = defineModel<ListScope>({ required: true })

const name = computed(() => projects.list.find((p) => p.slug === projects.current)?.name ?? '')
</script>

<template>
  <div class="cells max-w-full" role="group" aria-label="Scope">
    <button
      type="button"
      class="cell max-w-56 truncate disabled:cursor-not-allowed disabled:opacity-55"
      :aria-pressed="model === 'project'"
      :disabled="!projects.current"
      @click="model = 'project'"
    >
      {{ name || 'No project selected' }}
    </button>
    <button type="button" class="cell" :aria-pressed="model === 'all'" @click="model = 'all'">All projects</button>
  </div>
</template>
