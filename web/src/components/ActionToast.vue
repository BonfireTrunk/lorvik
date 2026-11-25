<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { cardActionState } from '../composables/useCardActions'
import IconX from '~icons/lucide/x'

// A manual popover sits in the top layer, so the toast shows over the card
// panel's modal dialog too. Re-showing it moves it above any dialog opened
// since. Outcomes fade on their own; errors stay longer so they can be read.
const el = ref<HTMLElement | null>(null)
let timer: ReturnType<typeof setTimeout> | undefined

watch(
  () => cardActionState.toast,
  async (t) => {
    clearTimeout(timer)
    await nextTick()
    const p = el.value
    if (!p) return
    if (p.matches(':popover-open')) p.hidePopover()
    if (!t) return
    p.showPopover()
    timer = setTimeout(() => (cardActionState.toast = null), t.error ? 8000 : 3000)
  },
)
</script>

<template>
  <div
    ref="el"
    popover="manual"
    data-keymap="toast"
    role="status"
    class="float inset-auto right-4 bottom-4 m-0 max-w-sm px-3 py-2 text-sm text-fg"
    :class="cardActionState.toast?.error ? 'border-danger text-danger' : ''"
  >
    <div class="flex items-start gap-2">
      <span class="flex-1">{{ cardActionState.toast?.text }}</span>
      <button type="button" class="btn btn-icon btn-quiet" aria-label="Dismiss" title="Dismiss" @click="cardActionState.toast = null"><IconX /></button>
    </div>
  </div>
</template>
