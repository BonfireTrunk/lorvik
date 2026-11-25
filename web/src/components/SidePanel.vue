<script setup lang="ts">
import { useModalDialog } from '../composables/useModalDialog'
import IconX from '~icons/lucide/x'

defineProps<{ labelledby: string; keymap?: string }>()
const emit = defineEmits<{ close: [] }>()

// A modal <dialog> traps focus, makes the page behind inert and sits in the top
// layer. Escape (the cancel event) and a backdrop click only emit close; the
// owner decides whether that is a route change or a ref, and unmounting the
// panel removes the dialog. An [autofocus] field inside takes the initial
// focus, else the dialog does.
const { dialog: panel, dialogEvents } = useModalDialog(() => emit('close'), {
  focus: () => panel.value?.querySelector<HTMLElement>('[autofocus]') ?? panel.value,
  lockScroll: true,
})
</script>

<template>
  <dialog
    ref="panel"
    tabindex="-1"
    class="m-0 ml-auto flex h-full max-h-none w-full max-w-2xl flex-col border-l-2 border-fg bg-canvas p-0 text-fg backdrop:bg-scrim focus:outline-none"
    :aria-labelledby="labelledby"
    :data-keymap="keymap"
    v-on="dialogEvents"
  >
    <div class="flex items-center gap-1 border-b-2 border-fg px-3 py-1.5">
      <slot name="leading" />
      <button type="button" class="btn btn-quiet ml-auto" aria-label="Close" title="Close (Esc)" aria-keyshortcuts="Escape" @click="emit('close')">
        <kbd class="kbd">Esc</kbd><IconX />
      </button>
    </div>
    <div class="flex-1 overflow-y-auto p-5">
      <slot />
    </div>
  </dialog>
</template>
