<script setup lang="ts">
import { computed } from 'vue'
import { RIGHTS, isRightLocked, toggledRights } from '../lib/rights'
import type { AgentRight } from '../lib/types'

// `modelValue` is the set this control edits. `inherited` rights are shown checked and locked
// ("from the agent"). `required` keeps the last right from being unchecked.
const props = defineProps<{
  modelValue: readonly string[]
  slug: string | undefined
  label: string
  inherited?: readonly string[]
  required?: boolean
  disabled?: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [rights: AgentRight[]] }>()

const checked = computed(() => new Set([...props.modelValue, ...(props.inherited ?? [])]))

const isLocked = (right: AgentRight): boolean => isRightLocked(props, right)
const toggle = (right: AgentRight, on: boolean): void => emit('update:modelValue', toggledRights(props.modelValue, right, on))
</script>

<template>
  <span class="flex flex-wrap items-center gap-x-3 gap-y-1" role="group" :aria-label="`Rights: ${label}`">
    <label
      v-for="r in RIGHTS"
      :key="r.value"
      class="flex items-center gap-x-1.5"
      :title="inherited?.includes(r.value) ? `From the agent. ${r.tip}` : r.tip"
    >
      <input
        type="checkbox"
        :checked="checked.has(r.value)"
        :disabled="isLocked(r.value)"
        :aria-label="`${r.label}, ${label}`"
        @change="toggle(r.value, ($event.target as HTMLInputElement).checked)"
      />
      <span>{{ r.label }}</span>
    </label>
  </span>
</template>
