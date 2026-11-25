<script setup lang="ts">
// The loading and empty line of a list or detail. The caller passes what it
// knows; errors stay with ErrorBanner.
defineProps<{ loading: boolean; empty?: boolean; emptyText?: string }>()
</script>

<template>
  <div v-if="loading" class="space-y-2 py-1" role="status">
    <div class="h-4 w-full skeleton"></div>
    <div class="h-4 w-4/5 skeleton"></div>
    <div class="h-4 w-3/5 skeleton"></div>
    <span class="sr-only">Loading…</span>
  </div>
  <p v-else-if="empty" class="state">{{ emptyText ?? 'Nothing here.' }}</p>
</template>
