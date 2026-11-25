<script setup lang="ts">
import type { DropFormFields } from '../../composables/useDropForm'
import { DROP_REASONS } from '../../lib/cards'

defineProps<{ idPrefix: string; inline?: boolean; autofocus?: boolean; showCascade?: boolean }>()
const fields = defineModel<DropFormFields>({ required: true })
</script>

<template>
  <div>
    <label class="label" :for="`${idPrefix}-reason`">Reason</label>
    <select :id="`${idPrefix}-reason`" v-model="fields.reason" :class="inline ? 'field field-auto' : 'field'" :autofocus="autofocus">
      <option v-for="reason in DROP_REASONS" :key="reason">{{ reason }}</option>
    </select>
  </div>
  <div v-if="fields.reason === 'duplicate'">
    <label class="label" :for="`${idPrefix}-duplicate`">Duplicate of #</label>
    <input :id="`${idPrefix}-duplicate`" v-model="fields.duplicate" :class="inline ? 'field field-auto w-24' : 'field'" inputmode="numeric" required />
  </div>
  <div :class="inline ? 'min-w-40 flex-1' : undefined">
    <label class="label" :for="`${idPrefix}-note`">Note (optional)</label>
    <input :id="`${idPrefix}-note`" v-model="fields.note" class="field" />
  </div>
  <label v-if="showCascade" class="flex items-center gap-1.5 text-sm">
    <input v-model="fields.cascade" type="checkbox" /> Drop its open children too
  </label>
</template>
