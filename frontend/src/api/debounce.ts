/**
 * Trailing-edge debounce.
 *
 * A parameter drag produces an event per pointer move. Each one writes through the bridge
 * and asks for a render, so the preview is replaced dozens of times a second and flickers
 * while the plugin chain re-runs. What the user wants to see is the frame after they stop
 * moving, not every frame on the way.
 *
 * Trailing edge rather than leading: the work happens once, when the input settles. A
 * leading edge would still fire for the first event of a drag and again for the last, which
 * is two renders where one is enough.
 */

/** Debounced is a function that runs at most once per quiet period. */
export interface Debounced {
  /** Schedules the work. Repeated calls inside the window collapse into one run. */
  call: () => void;
  /** Cancels a pending run. Call this when the caller goes away. */
  cancel: () => void;
  /** True while a run is pending, for tests and for a caller that wants to show it. */
  pending: () => boolean;
}

/**
 * createDebounced wraps a function so it runs once after the calls stop.
 *
 * `setTimeout` is injected rather than reached for directly, so the behaviour can be tested
 * without waiting in real time.
 */
export function createDebounced(
  work: () => void,
  milliseconds: number,
  // Bare setTimeout rather than window.setTimeout: the browser and the test runner both
  // define it, and reaching for window would tie this module to a document it does not need.
  schedule: (fn: () => void, ms: number) => number = (fn, ms) =>
    setTimeout(fn, ms) as unknown as number,
  unschedule: (handle: number) => void = (handle) => clearTimeout(handle),
): Debounced {
  let handle: number | null = null;

  return {
    call: () => {
      if (handle !== null) {
        unschedule(handle);
      }
      handle = schedule(() => {
        handle = null;
        work();
      }, milliseconds);
    },
    cancel: () => {
      if (handle !== null) {
        unschedule(handle);
        handle = null;
      }
    },
    pending: () => handle !== null,
  };
}
