// localStorage that never throws: private windows and blocked storage read as
// empty and writes are dropped, so the app still works for the page view.

export function readStored(key: string): string {
  try {
    return localStorage.getItem(key) ?? ''
  } catch {
    return ''
  }
}

export function writeStored(key: string, value: string): void {
  try {
    localStorage.setItem(key, value)
  } catch {
    // storage unavailable: the value lasts for this page view only
  }
}

export function removeStored(key: string): void {
  try {
    localStorage.removeItem(key)
  } catch {
    // storage unavailable: nothing to remove
  }
}

export function storedKeys(prefix: string): string[] {
  try {
    return Object.keys(localStorage).filter((k) => k.startsWith(prefix))
  } catch {
    return []
  }
}
