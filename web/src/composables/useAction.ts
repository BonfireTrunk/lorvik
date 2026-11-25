import { computed, ref, type Ref } from 'vue'

interface RunOptions {
  /** Called after the failure is stored in `error`, for caller-specific handling. */
  onError?: (err: unknown) => void
}

/**
 * The busy/error/try/catch/finally shell of a form submit. `error` holds the
 * raw failure; ErrorBanner turns it into the message (and hint) to show.
 * `run` resolves true when `fn` succeeded and false when it failed, and never
 * rejects.
 */
export function useAction() {
  const busy = ref(false)
  const error = ref<unknown>(null)

  async function run(fn: () => Promise<void>, opts: RunOptions = {}): Promise<boolean> {
    busy.value = true
    error.value = null
    try {
      await fn()
      return true
    } catch (e) {
      error.value = e
      opts.onError?.(e)
      return false
    } finally {
      busy.value = false
    }
  }

  return { busy, error, run }
}

/** Whether a form whose only required input is `text` may be submitted now: not busy and the text is not blank. */
export function useCanSubmit(busy: Ref<boolean>, text: Ref<string>) {
  return computed(() => !busy.value && !!text.value.trim())
}
