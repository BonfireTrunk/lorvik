<script setup lang="ts">
import SidePanel from './SidePanel.vue'
import NewProjectForm from './NewProjectForm.vue'
import type { Project } from '../lib/types'

const emit = defineEmits<{ created: [project: Project]; close: [] }>()
</script>

<template>
  <SidePanel labelledby="new-project-title" @close="emit('close')">
    <template #leading>
      <h2 id="new-project-title" class="px-2 font-semibold">New project</h2>
    </template>
    <NewProjectForm @created="emit('created', $event)" @close="emit('close')" />
  </SidePanel>
</template>
