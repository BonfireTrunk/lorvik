import { onBeforeUnmount, onMounted, ref } from 'vue'

interface ModalOptions {
  focus?: () => HTMLElement | null
  restoreFocus?: 'always' | 'if-unfocused'
  lockScroll?: boolean
}

export function useModalDialog(close: () => void, options: ModalOptions = {}) {
  const dialog = ref<HTMLDialogElement | null>(null)
  let opener: HTMLElement | null = null
  let overflow = ''

  onMounted(() => {
    opener = document.activeElement instanceof HTMLElement ? document.activeElement : null
    dialog.value?.showModal()
    options.focus?.()?.focus()
    if (options.lockScroll) {
      overflow = document.documentElement.style.overflow
      document.documentElement.style.overflow = 'hidden'
    }
  })
  onBeforeUnmount(() => {
    if (options.lockScroll) document.documentElement.style.overflow = overflow
    // A chosen page or panel takes focus itself; only a plain close returns it.
    if (options.restoreFocus !== 'if-unfocused' || document.activeElement === document.body || !document.activeElement) opener?.focus()
  })

  const dialogEvents = {
    cancel: (e: Event) => {
      e.preventDefault()
      close()
    },
    click: (e: MouseEvent) => {
      if (e.target === e.currentTarget) close()
    },
    close,
  }

  return { dialog, dialogEvents }
}
