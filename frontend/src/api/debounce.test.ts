import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { createDebounced } from "./debounce";

describe("createDebounced", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("runs once after a burst of calls", () => {
    const work = vi.fn();
    const debounced = createDebounced(work, 100);

    // Twenty pointer moves, which is what a drag produces.
    for (let i = 0; i < 20; i += 1) {
      debounced.call();
      vi.advanceTimersByTime(10);
    }
    // Still inside the quiet window: nothing has run.
    expect(work).not.toHaveBeenCalled();

    vi.advanceTimersByTime(100);
    expect(work).toHaveBeenCalledTimes(1);
  });

  it("runs again for a second burst after the first settles", () => {
    const work = vi.fn();
    const debounced = createDebounced(work, 100);

    debounced.call();
    vi.advanceTimersByTime(100);
    expect(work).toHaveBeenCalledTimes(1);

    debounced.call();
    vi.advanceTimersByTime(100);
    expect(work).toHaveBeenCalledTimes(2);
  });

  it("does not run when cancelled", () => {
    const work = vi.fn();
    const debounced = createDebounced(work, 100);

    debounced.call();
    debounced.cancel();
    vi.advanceTimersByTime(500);

    expect(work).not.toHaveBeenCalled();
    expect(debounced.pending()).toBe(false);
  });

  it("reports whether a run is pending", () => {
    const debounced = createDebounced(() => {}, 100);
    expect(debounced.pending()).toBe(false);
    debounced.call();
    expect(debounced.pending()).toBe(true);
    vi.advanceTimersByTime(100);
    expect(debounced.pending()).toBe(false);
  });

  it("keeps the delay it was given, not the first call's position", () => {
    // The run happens a full window after the *last* call, which is what makes a slow drag
    // still end in exactly one render.
    const work = vi.fn();
    const debounced = createDebounced(work, 100);

    debounced.call();
    vi.advanceTimersByTime(90);
    debounced.call(); // resets the window
    vi.advanceTimersByTime(90);
    expect(work).not.toHaveBeenCalled();
    vi.advanceTimersByTime(20);
    expect(work).toHaveBeenCalledTimes(1);
  });
});
