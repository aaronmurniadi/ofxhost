import { describe, expect, it, vi } from "vitest";
import { getZoomPercent, setZoomPercent, subscribeZoomPercent } from "./zoomStore";

describe("zoomStore", () => {
  it("reports what was published", () => {
    setZoomPercent(135);
    expect(getZoomPercent()).toBe(135);
    setZoomPercent(100);
  });

  it("notifies a reader once per change", () => {
    const reader = vi.fn();
    const stop = subscribeZoomPercent(reader);
    setZoomPercent(120);
    setZoomPercent(121);
    stop();
    setZoomPercent(122);
    expect(reader).toHaveBeenCalledTimes(2);
    setZoomPercent(100);
  });

  it("stays quiet when the value does not change", () => {
    // A wheel that has reached the end of the range must not wake the readout: the number
    // on screen is already right.
    setZoomPercent(100);
    const reader = vi.fn();
    const stop = subscribeZoomPercent(reader);
    setZoomPercent(100);
    stop();
    expect(reader).not.toHaveBeenCalled();
  });
});
