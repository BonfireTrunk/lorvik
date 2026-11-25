<script setup lang="ts">
import { computed } from 'vue'
import { bindings, keyLabel, type Binding } from '../composables/useKeymap'
import { useModalDialog } from '../composables/useModalDialog'
import IconX from '~icons/lucide/x'

const emit = defineEmits<{ close: [] }>()

// One row per key and label: a binding registered by two components shows once.
const groups = computed(() => {
  const out = new Map<string, Binding[]>()
  for (const b of bindings) {
    const rows = out.get(b.group) ?? []
    if (!rows.some((r) => r.keys === b.keys && r.label === b.label)) rows.push(b)
    out.set(b.group, rows)
  }
  return [...out]
})

const { dialog: dlg, dialogEvents } = useModalDialog(() => emit('close'))
</script>

<template>
  <dialog
    ref="dlg"
    class="float m-auto w-full max-w-lg p-5 text-fg backdrop:bg-scrim"
    aria-label="Keyboard shortcuts"
    v-on="dialogEvents"
  >
    <div class="mb-3 flex items-center gap-2">
      <h2 class="page-title">Keyboard shortcuts</h2>
      <button type="button" class="btn ml-auto" @click="emit('close')"><IconX />Close</button>
    </div>
    <section v-for="[group, rows] in groups" :key="group" class="mb-3">
      <h3 class="h2">{{ group }}</h3>
      <dl class="grid grid-cols-[8rem_1fr] gap-x-3 gap-y-1 text-sm">
        <template v-for="b in rows" :key="b.keys + b.label">
          <dt><kbd class="kbd">{{ keyLabel(b.keys) }}</kbd></dt>
          <dd>{{ b.label }}</dd>
        </template>
      </dl>
    </section>
    <p class="meta">Keys do nothing while you type in a field. Esc closes this.</p>
  </dialog>
</template>
