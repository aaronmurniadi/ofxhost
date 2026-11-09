/**
 * The application shell.
 *
 * It holds the state the panels share and nothing else: the open document, the
 * preview URL, and whether a decode is in flight. Every change is a call to the Go
 * side and a state update from its answer, so there is no second copy of the
 * document in JavaScript.
 *
 * There is no bar above the panels (D12). The commands and the status line live at the
 * top of the Plugins panel, so this file draws the panels and the dialogs and nothing
 * else.
 *
 * What this shell does not have yet, and where it goes:
 *
 *   M7       Windows and Linux packaging
 */

import { useCallback, useEffect, useRef, useState } from "react";
import { api, chainApi, type ChainDTO, type DocumentDTO } from "./api/document";
import { createDebounced, type Debounced } from "./api/debounce";
import { DEFAULT_PANELS, sessionApi, type PanelsDTO } from "./api/session";
import Workspace from "./layout/Workspace";
import ExportDialog from "./panels/ExportDialog";
import ParametersPanel from "./panels/ParametersPanel";
import PluginsPanel from "./panels/PluginsPanel";
import PreviewPanel, { type PreviewState } from "./panels/PreviewPanel";
import SettingsDialog from "./panels/SettingsDialog";
import { normaliseTheme } from "./panels/themes";

/**
 * How long a render request waits for the input to settle.
 *
 * Long enough to swallow a drag's worth of pointer moves, short enough that a single click
 * feels immediate. A drag produces events every few milliseconds, so anything above about
 * 80 ms collapses them; this leaves a little room for a jerky trackpad without the result
 * arriving late enough to notice.
 */
const RENDER_DEBOUNCE_MS = 140;

/**
 * How long a resize waits before the preview is re-rendered.
 *
 * Longer than an edit's, because dragging a splitter emits a size for every pixel of the
 * drag and the user is not looking at the picture while they do it. The frame is refitted on
 * screen immediately; only the render at the new resolution waits.
 */
const RESIZE_DEBOUNCE_MS = 300;

/** The Fit option, whose render size follows the panel rather than a number. */
const FIT_RESOLUTION = 0;

/**
 * The space every file is developed into until the user picks another one.
 *
 * A Rec.2020 space is a wide gamut, so it is the honest choice for a RAW file: the sensor
 * resolves more than sRGB, and developing into sRGB throws that away before the chain sees
 * it. The name is read from the Go side's list at startup rather than hard-coded as an
 * index, because the index is an implementation detail of the enum.
 */
const DEFAULT_SPACE_NAME = "Linear Rec.2020";

/**
 * Where the chosen space is remembered.
 *
 * The key is the one the colour space dialog used, so a choice made in an earlier build
 * survives this change instead of being reset to the default.
 */
const SPACE_STORAGE_KEY = "ofxhost.workingSpace.v1";

/** How often the shell asks whether a decode has landed. */
const POLL_INTERVAL_MS = 120;

/**
 * How many polls before the shell gives up.
 *
 * A 25 MP RAW takes a couple of seconds to develop, so the cap is generous: it is
 * there so a real failure ends with a message rather than an endless "Opening…".
 */
const MAX_POLLS = 600;

/**
 * Opening is a decode in flight.
 *
 * The space travels beside the path because "a document is open" is not enough to know
 * that the decode landed: a new request leaves the previous document in place until its
 * own decode finishes, so the file already on screen would answer for the new one.
 */
interface Opening {
  /** The file being developed. */
  path: string;
  /** The space it was asked for, by name. Empty when the space list could not be read. */
  spaceName: string;
}

export default function App() {
  const [doc, setDoc] = useState<DocumentDTO | null>(null);
  const [preview, setPreview] = useState<PreviewState | null>(null);
  // opening is the decode that is in flight, or null when none is. It carries the space
  // by name so the poll can tell a new frame from the one already on screen.
  const [opening, setOpening] = useState<Opening | null>(null);
  // busy is true from the pick of a file until its frame is on screen. It is derived from
  // opening, so the two cannot disagree.
  const busy = opening !== null;
  const [message, setMessage] = useState("Open a RAW file to see the preview.");
  const [version, setVersion] = useState("");
  const [chain, setChain] = useState<ChainDTO | null>(null);
  // renderTick asks for another preview, which is what every chain and parameter
  // edit does.
  const [renderTick, setRenderTick] = useState(0);
  // rendering is true while the chain is being run for a preview. Together with busy, which
  // is a decode, it decides whether the preview draws its spinner: from the user's side both
  // mean the program is working and the frame on screen is not final.
  const [rendering, setRendering] = useState(false);
  // exporting shows the export dialog, which is only reachable once a file is open.
  const [exporting, setExporting] = useState(false);
  // panels is the window layout. It starts at the defaults so the first draw has
  // something to show, and is replaced by the stored layout as soon as it loads.
  const [panels, setPanels] = useState<PanelsDTO>(DEFAULT_PANELS);
  // settingsReady holds the workspace back for one frame.
  //
  // react-resizable-panels reads defaultSize only when it mounts, so a layout that
  // arrives after the first draw would never be applied. Waiting for the settings
  // attempt to finish - success or failure - costs one frame and makes a restored
  // layout actually take effect.
  const [settingsReady, setSettingsReady] = useState(false);
  // lastImage is the file that was open last time, offered as a one-click restore.
  const [lastImage, setLastImage] = useState("");
  // settingsOpen shows the settings dialog.
  const [settingsOpen, setSettingsOpen] = useState(false);
  // The render size is chrome, not part of the picture, so it lives here beside the other
  // status information rather than floating over the image.
  const [resolutions, setResolutions] = useState<string[]>([]);
  const [resolution, setResolution] = useState(0);
  // inputSpace is the index into spaces that the next open is developed into.
  const [spaces, setSpaces] = useState<string[]>([]);
  const [inputSpace, setInputSpace] = useState(0);

  // The sizes are read once, and only when a file could be opened with one of them.
  useEffect(() => {
    void (async () => {
      try {
        setResolutions((await api.previewResolutions()) ?? []);
      } catch {
        // The control stays hidden when the list cannot be read.
      }
    })();
  }, []);

  // The colour spaces are read once as well. The chosen index is the space every open is
  // developed into, and it is remembered, so a user who picked another space is not put
  // back on the default at the next launch.
  useEffect(() => {
    void (async () => {
      try {
        const available = await api.colorSpaces();
        setSpaces(available);
        const stored = window.localStorage.getItem(SPACE_STORAGE_KEY);
        if (stored !== null) {
          const index = Number(stored);
          if (Number.isInteger(index) && index >= 0 && index < available.length) {
            setInputSpace(index);
            return;
          }
        }
        const preferred = available.indexOf(DEFAULT_SPACE_NAME);
        if (preferred >= 0) {
          setInputSpace(preferred);
        }
      } catch {
        // The control stays hidden when the list cannot be read.
      }
    })();
  }, []);

  const chooseResolution = useCallback(async (index: number) => {
    setResolution(index);
    await api.setPreviewResolution(index);
    // Ask for the frame again at the new size; the old one is now the wrong one.
    setRenderTick((tick) => tick + 1);
  }, []);

  // applyAppearance repaints the shell.
  //
  // The theme is an attribute and the text size is a token, so a change costs one
  // style recalculation and no React re-render of the panels.
  const applyAppearance = useCallback((theme: string, fontPt: number) => {
    const root = document.documentElement;
    // The theme name is the value the stylesheet selects on, so it is reduced to that form
    // here as well as when a settings file is read. A name this build does not know falls
    // back to the default instead of leaving the window unstyled.
    root.dataset.theme = normaliseTheme(theme);
    if (fontPt > 0) {
      // Points, not pixels. The setting says points and the token is read by every
      // relative size in the stylesheet, so a change here scales the whole interface
      // rather than the paragraphs that happen to inherit it.
      root.style.setProperty("--font-size", `${fontPt}pt`);
    } else {
      // Zero means follow the system, which is the stylesheet's own default.
      root.style.removeProperty("--font-size");
    }
  }, []);

  // Load the stored layout once.
  //
  // A failure here must not stop the window: the defaults are already drawn, so a
  // settings file that cannot be read costs the user their layout and nothing else.
  useEffect(() => {
    void (async () => {
      try {
        const settings = await sessionApi.settings();
        if (settings?.panels) {
          setPanels(settings.panels);
        }
        if (settings?.appearance) {
          applyAppearance(settings.appearance.theme, settings.appearance.fontPt);
        }
      } catch {
        // Keep the defaults.
      } finally {
        setSettingsReady(true);
      }
    })();

    // Return to the picture that was open last time.
    //
    // Only the path is offered, and the file is developed when the button is pressed: the
    // pixels cost a decode, and reopening a file the user did not ask for at launch would
    // spend seconds of their time before they chose anything. The space it will open in is
    // the one in the control, which is the stored choice or the default.
    void (async () => {
      try {
        const session = await api.lastSession();
        if (session?.found && session.path) {
          setLastImage(session.path);
        }
      } catch {
        // No session to restore.
      }
    })();
  }, [applyAppearance]);

  // reloadChain reads the chain the Go side holds now.
  //
  // It is declared before the effects that use it, because a dependency array is read
  // during the render that follows this declaration.
  const reloadChain = useCallback(() => {
    chainApi.current().then(setChain).catch(() => setChain(null));
  }, []);

  // Poll while a decode is in flight.
  //
  // The Go side decodes on its own goroutine, so the window never blocks on a RAW
  // file; the decode becomes visible through Current() once it lands. A repeating
  // timer is what waits for that, and the attempt cap is what keeps a genuine
  // failure from looking like an endless wait.
  //
  // The wait is for the requested space, not only for "a document is open". A new Open
  // leaves the previous document in place until its own decode lands, so a space chosen
  // for the file already on screen would otherwise be answered by the old pixels at once.
  useEffect(() => {
    if (opening === null) {
      return;
    }

    let attempts = 0;
    let cancelled = false;

    const landed = (current: DocumentDTO): boolean => {
      if (!current.open || current.path !== opening.path) {
        return false;
      }
      // An unreadable space list cannot be compared against, so the path is the only
      // evidence available and the frame is taken as it arrives.
      if (opening.spaceName === "") {
        return true;
      }
      return current.workingSpace === opening.spaceName;
    };

    const tick = async () => {
      if (cancelled) {
        return;
      }
      attempts += 1;

      const current = await api.current();
      if (cancelled) {
        return;
      }

      if (!landed(current)) {
        if (attempts >= MAX_POLLS) {
          setMessage("The image did not open. See the log for the reason.");
          setOpening(null);
          return;
        }
        window.setTimeout(tick, POLL_INTERVAL_MS);
        return;
      }

      const published = await api.publishPreview();
      if (cancelled) {
        return;
      }
      if (published.error) {
        setMessage(published.error);
        setOpening(null);
        return;
      }

      setDoc(current);
      setPreview({ url: published.url, width: published.width, height: published.height });
      setMessage("");
      setOpening(null);
      // The chain of the image that just landed is the one recorded for it, rebuilt by
      // the Go side while the decode was finishing. The list on screen was read before
      // any file was open, so it is empty and has to be read again here.
      reloadChain();
    };

    window.setTimeout(tick, POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
    };
  }, [opening, reloadChain]);

  useEffect(() => {
    api.version().then(setVersion).catch(() => setVersion(""));
    chainApi.current().then(setChain).catch(() => setChain(null));
  }, []);

  // A parameter drag asks for a render per pointer move. Each one re-runs the plugin chain
  // and replaces the frame, so the preview flickers while the user is still moving. The
  // request is collapsed into one render per quiet period instead; the edit itself still
  // lands immediately, so nothing lags behind the pointer.
  const renderDebouncer = useRef<Debounced | null>(null);
  if (renderDebouncer.current === null) {
    renderDebouncer.current = createDebounced(() => setRenderTick((tick) => tick + 1), RENDER_DEBOUNCE_MS);
  }

  const requestRender = useCallback(() => {
    renderDebouncer.current?.call();
  }, []);

  const resizeDebouncer = useRef<Debounced | null>(null);
  if (resizeDebouncer.current === null) {
    resizeDebouncer.current = createDebounced(
      () => setRenderTick((tick) => tick + 1),
      RESIZE_DEBOUNCE_MS,
    );
  }

  // A pending render must not fire after the window has gone.
  useEffect(
    () => () => {
      renderDebouncer.current?.cancel();
      resizeDebouncer.current?.cancel();
    },
    [],
  );

  // A chain or parameter change republishes the preview. The Go side runs the chain
  // and encodes the frame, so the frontend only swaps the URL.
  useEffect(() => {
    if (renderTick === 0) {
      return;
    }
    void (async () => {
      setRendering(true);
      try {
        const published = await api.publishPreview();
        if (published.error) {
          setMessage(published.error);
          return;
        }
        setPreview({ url: published.url, width: published.width, height: published.height });
        reloadChain();
      } finally {
        setRendering(false);
      }
    })();
  }, [renderTick, reloadChain]);

  // openInto starts a decode of one file into one space.
  //
  // The space is part of the request and not a setting applied afterwards: it is where
  // the pixels are developed (D8), so it has to be known before the call. A failure here
  // is a message and no decode, which is why the in-flight mark is cleared by the caller
  // that sees the error rather than left to the poll.
  const openInto = useCallback(
    async (path: string, space: number) => {
      if (path === "") {
        return;
      }
      setOpening({ path, spaceName: spaces[space] ?? "" });
      setMessage(`Opening ${path}…`);
      const result = await api.open(path, space);
      if (result.error !== "") {
        setMessage(result.error);
        setOpening(null);
      }
    },
    [spaces],
  );

  // chooseFile asks the user for a file and develops it into the chosen space.
  const chooseFile = useCallback(async () => {
    const path = await api.pickFile();
    if (path === "") {
      return;
    }
    await openInto(path, inputSpace);
  }, [openInto, inputSpace]);

  // chooseSpace records the space and develops the open file again.
  //
  // The space is decided by the decode (PLAN.md §7.5), so a pixel buffer that was built
  // for the old one cannot be relabelled into the new one. Decoding the same path again
  // is what makes the change visible in the picture rather than only in the choice; the
  // chain comes back from the record the Go side keeps for that path.
  const chooseSpace = useCallback(
    (index: number) => {
      setInputSpace(index);
      window.localStorage.setItem(SPACE_STORAGE_KEY, String(index));
      if (doc === null || doc.open !== true) {
        return;
      }
      if (doc.path === "") {
        return;
      }
      void openInto(doc.path, index);
    },
    [doc, openInto],
  );

  // The last size reported to the host, so a repeated measurement is not treated as a change.
  const lastPanelSize = useRef({ width: 0, height: 0 });

  const onResize = useCallback(
    (width: number, height: number) => {
      if (width === lastPanelSize.current.width && height === lastPanelSize.current.height) {
        return;
      }
      lastPanelSize.current = { width, height };
      // The Go side stores two numbers and nothing else, so a resize stream is cheap.
      void api.setPreviewPanelSize(width, height);

      // Fit means the render follows the panel, so the stored number alone changes nothing:
      // without a fresh render the frame keeps the size it was last drawn at, and "Fit" only
      // takes effect the next time something else asks for a frame. A fixed resolution does
      // not depend on the panel, so it is left alone.
      if (resolution === FIT_RESOLUTION && doc?.open === true) {
        resizeDebouncer.current?.call();
      }
    },
    [resolution, doc?.open],
  );

  return (
    <div className="app">
      <main className="app__main">
        {settingsReady ? (
          <Workspace
            panels={panels}
            onPanelsChange={setPanels}
            preview={
              <PreviewPanel
                preview={preview}
                fileName={doc?.fileName ?? ""}
                busy={busy}
                rendering={rendering}
                onResize={onResize}
                onChange={requestRender}
              />
            }
            plugins={
              <PluginsPanel
                onChange={requestRender}
                hasDocument={doc?.open === true}
                documentPath={doc?.path ?? ""}
                onOpen={() => void chooseFile()}
                lastImage={lastImage}
                onReopenLast={() => void openInto(lastImage, inputSpace)}
                onExport={() => void setExporting(true)}
                onOpenSettings={() => setSettingsOpen(true)}
                status={{
                  fileName: doc?.fileName ?? "",
                  width: doc?.width ?? 0,
                  height: doc?.height ?? 0,
                  space: doc?.workingSpace ?? "",
                  message,
                  version,
                }}
                resolutions={resolutions}
                resolution={resolution}
                onChooseResolution={(index) => void chooseResolution(index)}
                spaces={spaces}
                inputSpace={inputSpace}
                onChooseSpace={chooseSpace}
              />
            }
            parameters={
              <ParametersPanel
                selected={chain?.selected ?? -1}
                onChange={requestRender}
                documentPath={doc?.path ?? ""}
              />
            }
          />
        ) : null}
      </main>

      {settingsOpen ? (
        <SettingsDialog
          onClose={() => setSettingsOpen(false)}
          onAppearanceChange={applyAppearance}
        />
      ) : null}

      {exporting ? (
        <ExportDialog
          onClose={() => setExporting(false)}
          onExported={(result) => {
            setMessage(`Exported ${result.path}`);
            setExporting(false);
          }}
        />
      ) : null}
    </div>
  );
}
