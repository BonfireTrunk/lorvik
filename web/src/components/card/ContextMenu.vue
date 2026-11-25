<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import { runCardAction } from '../../composables/useCardActions'
import { cardActions } from '../../lib/cardActions'
import { canIn } from '../../lib/roles'
import type { CardBrief } from '../../lib/types'

// anchor is the tile the menu belongs to: it gets focus back when the menu
// closes, so a prompt opened from the menu returns there too.
const props = defineProps<{ card: CardBrief; slug: string; x: number; y: number; anchor: HTMLElement | null }>()
const emit = defineEmits<{ close: []; open: [number: number]; page: [number: number] }>()

interface Entry {
  key: string
  label: string
  run: () => void
}

// The tile's status may be a moment old; runCardAction reads the card again
// and refuses a move that is no longer allowed.
const entries = computed<Entry[]>(() => {
  const t = { slug: props.slug, number: props.card.number }
  return [
    { key: 'Enter', label: 'Open', run: () => emit('open', props.card.number) },
    { key: '', label: 'Open as page', run: () => emit('page', props.card.number) },
    ...cardActions(props.card.kind, props.card.status, canIn(props.slug, 'member'), !!props.card.parent).map((a) => ({
      key: a.key,
      label: a.label,
      run: () => void runCardAction(a.id, t),
    })),
  ]
})

const menu = ref<HTMLElement | null>(null)
let done = false

function close(): void {
  if (done) return
  done = true
  if (menu.value?.matches(':popover-open')) menu.value.hidePopover()
  props.anchor?.focus()
  emit('close')
}

function choose(e: Entry): void {
  close()
  e.run()
}

function items(): HTMLElement[] {
  return [...(menu.value?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])]
}

function onKeydown(e: KeyboardEvent): void {
  const list = items()
  const i = list.indexOf(document.activeElement as HTMLElement)
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault()
    const step = e.key === 'ArrowDown' ? 1 : -1
    list[(i + step + list.length) % list.length]?.focus()
    return
  }
  if (e.key === 'Home' || e.key === 'End') {
    e.preventDefault()
    list[e.key === 'Home' ? 0 : list.length - 1]?.focus()
    return
  }
  if (e.key === 'Tab') {
    e.preventDefault()
    close()
    return
  }
  // An action's own key runs it from the menu as it does on the tile.
  const hit = entries.value.find((x) => x.key.length === 1 && x.key === e.key)
  if (hit && !e.ctrlKey && !e.metaKey && !e.altKey) {
    e.preventDefault()
    choose(hit)
  }
}

// An auto popover closes on Escape and on a click outside; both arrive here.
function onToggle(e: Event): void {
  if ((e as ToggleEvent).newState === 'closed') close()
}

onMounted(async () => {
  const m = menu.value
  if (!m) return
  m.showPopover()
  await nextTick()
  const r = m.getBoundingClientRect()
  m.style.left = `${Math.max(4, Math.min(props.x, window.innerWidth - r.width - 4))}px`
  m.style.top = `${Math.max(4, Math.min(props.y, window.innerHeight - r.height - 4))}px`
  items()[0]?.focus()
})
</script>

<template>
  <div
    ref="menu"
    popover="auto"
    role="menu"
    :aria-label="`Card #${card.number}`"
    class="float fixed m-0 min-w-52 py-1 text-sm text-fg"
    :style="{ left: `${x}px`, top: `${y}px` }"
    @keydown="onKeydown"
    @toggle="onToggle"
  >
    <button
      v-for="e in entries"
      :key="e.label"
      type="button"
      role="menuitem"
      class="flex h-8 w-full items-center gap-3 px-3 text-left hover:bg-sunken focus-visible:bg-fg focus-visible:text-canvas focus-visible:outline-offset-[-2px]"
      @click="choose(e)"
    >
      <span class="flex-1">{{ e.label }}</span>
      <kbd v-if="e.key" class="kbd">{{ e.key }}</kbd>
    </button>
  </div>
</template>
