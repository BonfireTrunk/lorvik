<script setup lang="ts">
defineProps<{ id: string }>()
</script>

<template>
  <p :id="id" class="meta">Markdown works: lists, <code class="font-mono">`code`</code>, links, <code class="font-mono">[[card:N]]</code>. <slot /></p>
</template>
