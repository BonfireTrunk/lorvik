<script setup lang="ts">
import { Handle, Position } from '@vue-flow/core'
</script>

<template>
  <Handle type="target" :position="Position.Left" class="map-handle" />
  <Handle type="source" :position="Position.Right" class="map-handle" />
</template>
