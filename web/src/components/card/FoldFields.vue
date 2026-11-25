<script setup lang="ts">
import type { FoldFormFields } from '../../composables/useFoldForm'

defineProps<{ idPrefix: string; inline?: boolean; autofocus?: boolean }>()
const fields = defineModel<FoldFormFields>({ required: true })
</script>

<template>
  <div :class="inline ? 'min-w-48 flex-1' : undefined">
    <label class="label" :for="`${idPrefix}-path`">Folded into (document path)</label>
    <input :id="`${idPrefix}-path`" v-model="fields.path" class="field" placeholder="docs/requirements/board.md" :disabled="fields.none" :autofocus="autofocus" />
  </div>
  <label class="flex items-center gap-1.5 text-sm"><input v-model="fields.none" type="checkbox" /> No contract change</label>
</template>
