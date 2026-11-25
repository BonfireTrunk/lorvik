/**
 * A keydown handler for an editor with a send button: Ctrl+Enter or
 * Cmd+Enter calls `send` when `canSend` allows it. A plain Enter keeps adding
 * a line.
 */
export function sendOnModEnter(canSend: () => boolean, send: () => void): (e: KeyboardEvent) => void {
  return (e) => {
    if (e.key !== 'Enter' || !(e.ctrlKey || e.metaKey)) return
    e.preventDefault()
    if (canSend()) send()
  }
}
