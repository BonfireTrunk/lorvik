import { onBeforeUnmount, onMounted, reactive } from 'vue'

/**
 * Where a key press lands: the page, the card panel over it, or any other
 * dialog or popover (help, palette, prompt, menu) on top.
 */
export type KeyLayer = 'page' | 'panel' | 'dialog'

export interface Binding {
  /** KeyboardEvent.key ('j', '?'), 'mod+k' for Ctrl or Cmd with k, or a two-key sequence ('g b'). */
  keys: string
  label: string
  group: string
  run: (e: KeyboardEvent) => void
  /** The layers it fires in; page and panel when absent. [] lists a key the browser already handles, for the help only. */
  layers?: KeyLayer[]
}

const DEFAULT_LAYERS: KeyLayer[] = ['page', 'panel']
const SEQUENCE_MS = 1000

/** Every binding of the mounted components, in mount order; the help reads it. */
export const bindings = reactive<Binding[]>([])

/** The overlays any component may open: the help and the command palette. */
export const overlays = reactive({ help: false, palette: false })

export function isEditable(t: EventTarget | null): boolean {
  return t instanceof HTMLElement && (!!t.closest('input, textarea, select') || t.isContentEditable)
}

function currentLayer(): KeyLayer {
  const open = document.querySelectorAll('dialog[open]')
  const top = open[open.length - 1]
  if (top && !top.matches('[data-keymap="panel"]')) return 'dialog'
  try {
    if (document.querySelector(':popover-open:not([data-keymap="toast"])')) return 'dialog'
  } catch {
    // A browser without popovers has none open.
  }
  return top ? 'panel' : 'page'
}

/** The key's name as bindings spell it, or '' for a bare modifier or an Alt chord. */
export function keyName(e: Pick<KeyboardEvent, 'key' | 'altKey' | 'ctrlKey' | 'metaKey'>): string {
  if (e.altKey || ['Shift', 'Control', 'Alt', 'Meta'].includes(e.key)) return ''
  return e.ctrlKey || e.metaKey ? `mod+${e.key.toLowerCase()}` : e.key
}

const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)

/** How a binding's keys read in the help and in menus: "Ctrl K", "g then b". */
export function keyLabel(keys: string): string {
  return keys
    .split(' ')
    .map((k) => (k.startsWith('mod+') ? `${isMac ? 'Cmd' : 'Ctrl'} ${k.slice(4).toUpperCase()}` : k))
    .join(' then ')
}

let pending = ''
let pendingTimer: ReturnType<typeof setTimeout> | undefined

function clearPending(): void {
  pending = ''
  clearTimeout(pendingTimer)
}

function onKeydown(e: KeyboardEvent): void {
  if (e.defaultPrevented || e.isComposing || isEditable(e.target)) {
    clearPending()
    return
  }
  const k = keyName(e)
  if (!k) return
  const layer = currentLayer()
  // Newest first, so the latest mount wins a key it shares with an earlier one.
  const active = bindings.filter((b) => (b.layers ?? DEFAULT_LAYERS).includes(layer)).reverse()
  if (pending) {
    const seq = `${pending} ${k}`
    clearPending()
    const b = active.find((x) => x.keys === seq)
    if (b) {
      e.preventDefault()
      b.run(e)
    }
    return
  }
  if (active.some((x) => x.keys.startsWith(`${k} `))) {
    e.preventDefault()
    pending = k
    pendingTimer = setTimeout(clearPending, SEQUENCE_MS)
    return
  }
  const b = active.find((x) => x.keys === k)
  if (b) {
    e.preventDefault()
    b.run(e)
  }
}

/** Installs the one document listener; App calls it once. */
export function useKeymapRoot(): void {
  onMounted(() => document.addEventListener('keydown', onKeydown))
  onBeforeUnmount(() => {
    document.removeEventListener('keydown', onKeydown)
    clearPending()
  })
}

/** Registers bindings for the life of the calling component. */
export function useKeymap(list: Binding[]): void {
  onMounted(() => bindings.push(...list))
  onBeforeUnmount(() => {
    for (const b of list) {
      const i = bindings.indexOf(b)
      if (i >= 0) bindings.splice(i, 1)
    }
  })
}
