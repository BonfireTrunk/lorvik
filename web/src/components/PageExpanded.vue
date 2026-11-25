<script setup lang="ts">
import { useModalDialog } from '../composables/useModalDialog'
import { pageSrcdoc } from '../lib/pageFrame'
import IconMinimize from '~icons/lucide/minimize-2'

defineProps<{ html: string; label: string }>()
const emit = defineEmits<{ close: [] }>()

// A modal dialog over the card panel: Escape closes this one only, and the
// keymap treats it as a dialog layer so card keys stay off.
const { dialog, dialogEvents } = useModalDialog(() => emit('close'), { focus: () => dialog.value })
</script>

<template>
  <dialog
    ref="dialog"
    tabindex="-1"
    class="m-0 flex h-full max-h-none w-full max-w-none flex-col bg-canvas p-0 text-fg backdrop:bg-scrim focus:outline-none"
    :aria-label="label"
    v-on="dialogEvents"
  >
    <div class="flex items-center gap-2 border-b-2 border-fg px-3 py-1.5">
      <span class="text-sm font-medium">{{ label }}</span>
      <button type="button" class="btn btn-quiet ml-auto" title="Back to the card (Esc)" aria-keyshortcuts="Escape" @click="emit('close')">
        <kbd class="kbd">Esc</kbd><IconMinimize />Collapse
      </button>
    </div>
    <iframe class="block w-full flex-1 border-0 bg-white" sandbox="allow-scripts" referrerpolicy="no-referrer" :title="label" :srcdoc="pageSrcdoc(html)"></iframe>
  </dialog>
</template>
