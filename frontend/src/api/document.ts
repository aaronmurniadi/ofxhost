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

/** PluginDTO is one discovered plugin, for the picker. */
export interface PluginDTO {
  index: number;
  identifier: string;
  label: string;
  author: string;
}

/** NodeDTO is one node of the chain. */
export interface NodeDTO {
  index: number;
  label: string;
  identifier: string;
  enabled: boolean;
  selected: boolean;
}

/** ParamDTO is one parameter, for the panel. */
export interface ParamDTO {
  name: string;
  label: string;
  hint: string;
  parent: string;
  kind: string;
  dimension: number;
  nums: number[] | null;
  text: string;
  choices: string[] | null;
  displayMin: number;
  displayMax: number;
  hardMin: number;
  hardMax: number;
  enabled: boolean;
}

/** ChainDTO is the whole chain. */
export interface ChainDTO {
  nodes: NodeDTO[] | null;
  selected: number;
  available: boolean;
  message: string;
}

const CHAIN_SERVICE = `${SERVICE}.Chain`;

/** callChain invokes one chain binding method. */
async function callChain<T>(method: string, ...args: unknown[]): Promise<T> {
  return (await Call.ByName(`${CHAIN_SERVICE}.${method}`, ...args)) as T;
}

/** The chain methods the frontend uses. */
export const chainApi = {
  /** The discovered plugins. */
  plugins: () => callChain<PluginDTO[] | null>("Plugins"),

  /** The chain itself. */
  current: () => callChain<ChainDTO>("Current"),

  /** Appends a plugin. */
  add: (pluginIndex: number) =>
    callChain<{ index: number; error: string }>("Add", pluginIndex),

  /** Relocates a node. Returns an empty string on success. */
  move: (from: number, to: number) => callChain<string>("Move", from, to),

  /** Deletes a node. */
  remove: (index: number) => callChain<string>("Remove", index),

  /** Turns a node on or off. */
  setEnabled: (index: number, enabled: boolean) =>
    callChain<string>("SetEnabled", index, enabled),

  /** Selects the node the parameter panel edits. */
  select: (index: number) => callChain<void>("Select", index),

  /** The parameters of a node, with their current values. */
  params: (index: number) => callChain<ParamDTO[] | null>("Params", index),

  /** Writes a numeric parameter. */
  setParam: (nodeIndex: number, name: string, values: number[]) =>
    callChain<string>("SetParam", nodeIndex, name, values),

  /** Writes a text parameter. */
  setParamText: (nodeIndex: number, name: string, value: string) =>
    callChain<string>("SetParamText", nodeIndex, name, value),

  /** Restores every parameter of a node to its default. */
  resetParams: (index: number) =>
    callChain<[number, string]>("ResetParams", index),
};
