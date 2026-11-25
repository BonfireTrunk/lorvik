<script setup lang="ts">
import CardDetail from '../components/card/CardDetail.vue'

defineProps<{ slug: string; number: string }>()
</script>

<template>
  <div class="page">
    <router-link class="lnk text-sm" :to="{ name: 'project', params: { slug } }">&larr; Board</router-link>
    <CardDetail :slug="slug" :number="number" />
  </div>
</template>
