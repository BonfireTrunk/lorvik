<script setup lang="ts">
import { RIGHTS, isRightLocked, toggledRights } from '../lib/rights'
import type { AgentRight } from '../lib/types'

// One table of rights: a column per right, a row per project. A row follows the same rules as RightsChecks.
export interface RightsGridRow {
  key: string
  label: string
  modelValue: readonly string[]
  slug: string | undefined
  inherited?: readonly string[]
  required?: boolean
  disabled?: boolean
  /** Names the row's checkboxes: "<Right>, <groupLabel>". */
  groupLabel: string
  title?: string
}

defineProps<{ rows: RightsGridRow[]; firstColumn?: string }>()
const emit = defineEmits<{ change: [key: string, rights: AgentRight[]] }>()
</script>

<template>
  <div class="max-w-full overflow-x-auto">
    <table class="text-xs">
      <thead>
        <tr class="text-muted">
          <th scope="col" class="py-1 pr-4 text-left font-normal">{{ firstColumn ?? 'Project' }}</th>
          <th v-for="r in RIGHTS" :key="r.value" scope="col" class="px-2 py-1 text-center font-normal" :title="r.tip">{{ r.label }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in rows" :key="row.key" :title="row.title">
          <th scope="row" class="py-1 pr-4 text-left font-mono font-normal">{{ row.label }}</th>
          <td
            v-for="r in RIGHTS"
            :key="r.value"
            class="px-2 py-1 text-center"
            :title="row.inherited?.includes(r.value) ? `From the agent. ${r.tip}` : r.tip"
          >
            <input
              type="checkbox"
              :checked="row.modelValue.includes(r.value) || !!row.inherited?.includes(r.value)"
              :disabled="isRightLocked(row, r.value)"
              :aria-label="`${r.label}, ${row.groupLabel}`"
              @change="emit('change', row.key, toggledRights(row.modelValue, r.value, ($event.target as HTMLInputElement).checked))"
            />
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
