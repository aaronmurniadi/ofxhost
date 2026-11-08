/**
 * The preview's zoom, read by the status line.
 *
 * A wheel or a trackpad produces tens of events a second, and every one of them used to
 * travel into the shell's state. That re-rendered the whole window - the parameter tree
 * included, which for a plugin with 830 parameters is hundreds of controls - several times
 * per wheel notch, and the picture was the thing that slowed down.
 *
 * So the number has its own store. The preview writes it; one span reads it. A zoom step
 * now re-renders that span and nothing else.
 */

const listeners = new Set<() => void>();

let percent = 100;

/** setZoomPercent publishes the zoom the preview is showing. */
export function setZoomPercent(next: number): void {
  if (next === percent) {
    return;
  }
  percent = next;
  for (const listener of listeners) {
    listener();
  }
}

/** getZoomPercent reads the current zoom. It is the store's snapshot. */
export function getZoomPercent(): number {
  return percent;
}

/** subscribe registers a reader, for `useSyncExternalStore`. */
export function subscribeZoomPercent(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
