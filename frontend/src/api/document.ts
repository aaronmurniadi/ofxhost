/**
 * The bridge to the Go side.
 *
 * The generated bindings are the source of truth for method names, but they are
 * produced by `wails3 generate bindings`, which cannot run until there is at least
 * one service. This module wraps the raw runtime call so the rest of the frontend
 * depends on one small surface instead of on the generated file directly, which
 * also keeps the app usable before the first generation.
 *
 * Regenerate the bindings after changing a binding method:
 *
 *     wails3 generate bindings -d frontend/bindings ./cmd/ofxhost
 */

import { Call } from "@wailsio/runtime";

/** The service and package the bindings are registered under. */
const SERVICE = "github.com/aaronmurniadi/ofxhost/internal/transport/binds";
const STRUCT = "Document";

/** DocumentDTO describes the open document. */
export interface DocumentDTO {
  open: boolean;
  path: string;
  fileName: string;
  width: number;
  height: number;
  fileSpace: string;
  workingSpace: string;
  inputSpace: string;
  camera: {
    make: string;
    model: string;
    iso: number;
    aperture: number;
    shutter: number;
  };
}

/** OpenResult reports whether a decode was queued. */
export interface OpenResult {
  started: boolean;
  error: string;
}

/** PreviewURLResponse is the answer to a preview request. */
export interface PreviewURLResponse {
  url: string;
  width: number;
  height: number;
  generation: number;
  error: string;
}

/** call invokes one binding method, throwing on a transport failure. */
async function call<T>(method: string, ...args: unknown[]): Promise<T> {
  return (await Call.ByName(`${SERVICE}.${STRUCT}.${method}`, ...args)) as T;
}

/** The methods the frontend uses. */
export const api = {
  /** Starts decoding a file. The decode runs on its own goroutine. */
  open: (path: string) => call<OpenResult>("Open", { path }),

  /** Reads the open document. */
  current: () => call<DocumentDTO>("Current"),

  /** Renders the open document to the frame server and returns its URL. */
  publishPreview: () => call<PreviewURLResponse>("PublishPreview"),

  /** Records the preview size the user chose. */
  setPreviewResolution: (index: number) => call<string>("SetPreviewResolution", index),

  /** Reports the preview area, so "fit" can follow the panel. */
  setPreviewPanelSize: (width: number, height: number) =>
    call<void>("SetPreviewPanelSize", width, height),

  /** Records the space handed to the first plugin. */
  setInputSpace: (index: number) => call<string>("SetInputSpace", index),

  /** The colour space names, in enum order. */
  colorSpaces: () => call<string[]>("ColorSpaces"),

  /** The preview size names, in enum order. */
  previewResolutions: () => call<string[]>("PreviewResolutions"),

  /** The build version. */
  version: () => call<string>("Version"),
};
