import { onBeforeUnmount, onMounted, watch } from 'vue'
import { subscribeEvents } from '../lib/stream'
import type { LorvikEvent } from '../lib/types'

interface Options {
  /** The project whose events are followed. */
  slug: () => string
  /** Values that, when they change, resubscribe. */
  source: () => unknown[]
  /** Return false to ignore an event. */
  match?: (e: LorvikEvent) => boolean
  /** Runs once events have been quiet for `delay` ms. */
  reload: () => void
  delay: number
}

/** Calls reload shortly after a matching event arrives on a project's stream. */
export function useEventRefresh(o: Options): void {
  let stop: (() => void) | undefined
  let timer: ReturnType<typeof setTimeout> | undefined

  function connect(): void {
    stop?.()
    stop = subscribeEvents(
      o.slug(),
      '',
      (e) => {
        if (o.match && !o.match(e)) return
        clearTimeout(timer)
        timer = setTimeout(o.reload, o.delay)
      },
      () => undefined,
    )
  }

  onMounted(connect)
  watch(o.source, () => {
    clearTimeout(timer)
    connect()
  })
  onBeforeUnmount(() => {
    stop?.()
    clearTimeout(timer)
  })
}
