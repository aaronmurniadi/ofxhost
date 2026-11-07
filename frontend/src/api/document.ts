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
/** LastSessionDTO names the image a relaunch should reopen. */
export interface LastSessionDTO {
  found: boolean;
  path: string;
}

export const api = {
  /**
   * Starts decoding a file into a colour space.
   *
   * The space is the user's choice, taken before the call: a RAW file declares no
   * space of its own, and the choice is what the chain then works in.
   */
  open: (path: string, space: number) => call<OpenResult>("Open", { path, space }),

  /** Reads the open document. */
  current: () => call<DocumentDTO>("Current"),

  /** Renders the open document to the frame server and returns its URL. */
  publishPreview: () => call<PreviewURLResponse>("PublishPreview"),

  /** Records the preview size the user chose. */
  setPreviewResolution: (index: number) => call<string>("SetPreviewResolution", index),

  /** Reports the preview area, so "fit" can follow the panel. */
  setPreviewPanelSize: (width: number, height: number) =>
    call<void>("SetPreviewPanelSize", width, height),

  /** The colour space names, in enum order. */
  colorSpaces: () => call<string[]>("ColorSpaces"),

  /** The preview size names, in enum order. */
  previewResolutions: () => call<string[]>("PreviewResolutions"),

  /**
   * The image that was open when the program last quit.
   *
   * It comes from a directory entry, not from the settings file, and it reports
   * nothing when the file has since been deleted or when this is the first run.
   */
  lastSession: () => call<LastSessionDTO>("LastSession"),

  /** The build version. */
  version: () => call<string>("Version"),

  /** Asks the user for a file and returns its path, or an empty string. */
  pickFile: () => call<string>("PickFile"),
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
  /**
   * The plugin marked this parameter as not to be drawn in a generated interface.
   *
   * It is separate from kind on purpose: a group the plugin hides is still a group,
   * and a panel that reads this as a type loses the whole subtree.
   */
  secret: boolean;
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

  /** Restores one parameter of a node to the default its plugin declared. */
  resetParam: (nodeIndex: number, name: string) =>
    callChain<string>("ResetParam", nodeIndex, name),

  /** Restores every parameter of a node to its default. */
  resetParams: (index: number) =>
    callChain<[number, string]>("ResetParams", index),
};

/** FormatDTO describes one export format and what it can do. */
export interface FormatDTO {
  index: number;
  name: string;
  extension: string;
  bitDepths: number[] | null;
  quality: boolean;
  lossless: boolean;
  carriesICC: boolean;
  carriesAlpha: boolean;
}

/** ExportOptionsDTO is everything the export dialog draws itself from. */
export interface ExportOptionsDTO {
  formats: FormatDTO[] | null;
  colorSpaces: string[] | null;
  intents: string[] | null;
  workingSpace: string;
  defaultName: string;
  defaultFormat: number;
  defaultSpace: number;
  defaultIntent: number;
  defaultDepth: number;
  defaultQuality: number;
  suggestedFolder: string;
}

/** ExportRequest is one export, as the dialog describes it. */
export interface ExportRequest {
  format: number;
  space: number;
  intent: number;
  depth: number;
  quality: number;
  lossless: boolean;
  /**
   * Asks for a sidecar beside the exported file, recording the chain and the colour
   * spaces that produced it.
   *
   * Off by default: most exports do not need one, and a file the user did not ask for
   * is a file they have to notice and delete.
   */
  writeSidecar: boolean;
  path: string;
}

/** ExportResult is the outcome. */
export interface ExportResult {
  ok: boolean;
  path: string;
  width: number;
  height: number;
  notes: string[] | null;
  error: string;
}

const EXPORT_SERVICE = `${SERVICE}.Export`;

/** callExport invokes one export binding method. */
async function callExport<T>(method: string, ...args: unknown[]): Promise<T> {
  return (await Call.ByName(`${EXPORT_SERVICE}.${method}`, ...args)) as T;
}

/** The export methods the frontend uses. */
export const exportApi = {
  /** What the dialog draws itself from. */
  options: () => callExport<ExportOptionsDTO>("Options"),

  /** Asks where to write the file and returns the chosen path, or "". */
  save: (suggested: string) => callExport<string>("Save", suggested),

  /** Writes the file. */
  run: (req: ExportRequest) => callExport<ExportResult>("Run", req),
};
