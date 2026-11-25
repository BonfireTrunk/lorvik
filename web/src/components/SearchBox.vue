<script setup lang="ts">
import { ref } from 'vue'
import { keyLabel } from '../composables/useKeymap'
import IconSearch from '~icons/lucide/search'
import IconX from '~icons/lucide/x'

// The "/" key focuses it (the page binds the key); Enter asks for the next match, Escape clears and leaves.
defineProps<{ search: string }>()
const emit = defineEmits<{ search: [string]; next: [] }>()

const input = ref<HTMLInputElement | null>(null)
defineExpose({ focus: () => input.value?.focus() })

function escape(e: KeyboardEvent): void {
  emit('search', '')
  ;(e.target as HTMLInputElement).blur()
}

function clear(): void {
  emit('search', '')
  input.value?.focus()
}
</script>

<template>
  <div class="relative">
    <label class="relative block">
      <span class="sr-only">Search by title or number</span>
      <IconSearch class="pointer-events-none absolute top-1/2 left-2.5 h-4 w-4 -translate-y-1/2 text-muted" />
      <input
        ref="input"
        type="search"
        class="field field-find w-56 pr-14 pl-8"
        placeholder="Search title or #"
        aria-keyshortcuts="/"
        autocomplete="off"
        :value="search"
        @input="emit('search', ($event.target as HTMLInputElement).value)"
        @keydown.enter.prevent="emit('next')"
        @keydown.esc.prevent="escape"
      />
      <kbd v-if="!search" class="kbd absolute top-1/2 right-2.5 -translate-y-1/2">{{ keyLabel('/') }}</kbd>
    </label>
    <button v-if="search" type="button" class="btn btn-quiet btn-icon absolute top-1/2 right-1 -translate-y-1/2" aria-label="Clear the search" title="Clear the search" @click="clear"><IconX /></button>
  </div>
</template>
