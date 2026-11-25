<script setup lang="ts">
import IconLayoutDashboard from '~icons/lucide/layout-dashboard'
import IconMap from '~icons/lucide/map'

defineProps<{ slug: string; current: 'board' | 'map' }>()
</script>

<template>
  <nav class="cells" aria-label="View">
    <router-link class="cell inline-flex items-center gap-1.5 no-underline" :to="{ name: 'board', params: { slug } }" :aria-current="current === 'board' ? 'page' : undefined">
      <IconLayoutDashboard class="h-4 w-4" />Board
    </router-link>
    <router-link class="cell inline-flex items-center gap-1.5 no-underline" :to="{ name: 'map', params: { slug } }" :aria-current="current === 'map' ? 'page' : undefined">
      <IconMap class="h-4 w-4" />Map
    </router-link>
  </nav>
</template>
