/**
 * The application shell.
 *
 * It holds the state the panels share and nothing else: the open document, the
 * preview URL, and whether a decode is in flight. Every change is a call to the Go
 * side and a state update from its answer, so there is no second copy of the
 * document in JavaScript.
 *
 * What this shell does not have yet, and where it goes:
 *
 *   MS-peek  the preview URL is fetched, shown, and replaced on demand
 *   M3       the chain list and the parameter controls
 *   M4       the export dialog with the colour space and intent
 *   M5       the settings panel and the saved layout
 */

import { useCallback, useEffect, useRef, useState } from "react";
import { api, chainApi, type ChainDTO, type DocumentDTO } from "./api/document";
import { createDebounced, type Debounced } from "./api/debounce";
import { DEFAULT_PANELS, sessionApi, type PanelsDTO } from "./api/session";
import Workspace from "./layout/Workspace";
import ExportDialog from "./panels/ExportDialog";
import OpenDialog from "./panels/OpenDialog";
import ParametersPanel from "./panels/ParametersPanel";
import PluginsPanel from "./panels/PluginsPanel";
import PreviewPanel, { type PreviewState } from "./panels/PreviewPanel";
import SettingsDialog from "./panels/SettingsDialog";
import Select from "./widgets/Select";
import TooltipHint from "./widgets/Tooltip";

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

/** How often the shell asks whether a decode has landed. */
const POLL_INTERVAL_MS = 120;

/**
 * How many polls before the shell gives up.
 *
 * A 25 MP RAW takes a couple of seconds to develop, so the cap is generous: it is
 * there so a real failure ends with a message rather than an endless "Opening…".
 */
const MAX_POLLS = 600;

export default function App() {
  const [doc, setDoc] = useState<DocumentDTO | null>(null);
  const [preview, setPreview] = useState<PreviewState | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("Open a RAW file to see the preview.");
  const [version, setVersion] = useState("");
  const [chain, setChain] = useState<ChainDTO | null>(null);
  // renderTick asks for another preview, which is what every chain and parameter
  // edit does.
  const [renderTick, setRenderTick] = useState(0);
  // rendering is true while the chain is being run for a preview. It is separate from busy,
  // which is a decode: the status bar shows the spinner for either, because from the user's
  // side both mean the program is working and the frame on screen is not final.
  const [rendering, setRendering] = useState(false);
  // pendingPath is the file waiting for a colour space. Decision D8: the space is an
  // input to the decode, so it is asked before the file opens.
  const [pendingPath, setPendingPath] = useState("");
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
  // zoomPercent is reported by the preview and shown in the status bar.
  const [zoomPercent, setZoomPercent] = useState(100);
  // The render size is chrome, not part of the picture, so it lives here beside the other
  // status information rather than floating over the image.
  const [resolutions, setResolutions] = useState<string[]>([]);
  const [resolution, setResolution] = useState(0);

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
    // One attribute drives both our tokens and Kumo's components.
    root.dataset.mode = theme.toLowerCase();
    root.dataset.theme = theme.toLowerCase();
    if (fontPt > 0) {
      root.style.setProperty("--font-size", `${fontPt}px`);
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
    // The working space is not restored from disk: a chain is not saved with the image
    // (only an export can record one), so the space the file is developed into is asked
    // for again rather than guessed. Reopening a file the user did not choose would be
    // worse than starting at the picker, so this asks first.
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
  useEffect(() => {
    if (!busy) {
      return;
    }

    let attempts = 0;
    let cancelled = false;

    const tick = async () => {
      if (cancelled) {
        return;
      }
      attempts += 1;

      const current = await api.current();
      if (cancelled) {
        return;
      }

      if (!current.open) {
        if (attempts >= MAX_POLLS) {
          setMessage("The image did not open. See the log for the reason.");
          setBusy(false);
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
        setBusy(false);
        return;
      }

      setDoc(current);
      setPreview({ url: published.url, width: published.width, height: published.height });
      setMessage("");
      setBusy(false);
      // The chain of the image that just landed is the one recorded for it, rebuilt by
      // the Go side while the decode was finishing. The list on screen was read before
      // any file was open, so it is empty and has to be read again here.
      reloadChain();
    };

    window.setTimeout(tick, POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
    };
  }, [busy, reloadChain]);

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

  // chooseFile asks the user for a file and then for the space to develop it into.
  const chooseFile = useCallback(async () => {
    const path = await api.pickFile();
    if (path === "") {
      return;
    }
    setPendingPath(path);
  }, []);

  // openWithSpace starts the decode once the space is known.
  const openWithSpace = useCallback(async (space: number) => {
    const path = pendingPath;
    setPendingPath("");
    if (path === "") {
      return;
    }
    setBusy(true);
    setMessage(`Opening ${path}…`);
    const result = await api.open(path, space);
    if (result.error !== "") {
      setMessage(result.error);
      setBusy(false);
    }
  }, [pendingPath]);

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
      {/* The bar carries actions only. What is open and how it is being shown lives in
          the status bar, and repeating it here made two places say the same thing. */}
      {/* One bar: the actions on the left, everything about what is on screen on the
          right, and the process indicator at the far right where it is always in the same
          place. There is no second line of chrome. */}
      <header className="app__bar">
        <button className="button button--primary" onClick={() => void chooseFile()}>
          Open
        </button>
        {lastImage !== "" && !doc?.open ? (
          <TooltipHint text={lastImage}>
            <button className="button" onClick={() => setPendingPath(lastImage)}>
              Reopen last
            </button>
          </TooltipHint>
        ) : null}
        <TooltipHint text={doc?.open ? "Export the chain at full resolution" : "Open a file first"}>
          <button className="button" onClick={() => void setExporting(true)} disabled={!doc?.open}>
            Export
          </button>
        </TooltipHint>
        <button className="button" onClick={() => setSettingsOpen(true)}>
          Settings
        </button>

        <div className="app__status">
          {/* The zoom leads: it is the one value that changes while the user works, and a
              number that moves should stay in the same place so the eye finds it. */}
          {doc?.open ? (
            <>
              <span className="app__status-zoom" aria-label={`Zoom ${zoomPercent}%`}>
                {zoomPercent}%
              </span>
              <span className="app__status-sep" aria-hidden>
                ·
              </span>
            </>
          ) : null}
          <span className="app__status-name" title={doc?.path ?? ""}>
            {doc?.open ? doc.fileName : "No image"}
          </span>
          {doc?.open ? (
            <>
              <span className="app__status-sep" aria-hidden>
                ·
              </span>
              <span className="app__status-size">
                {doc.width}×{doc.height}
              </span>
            </>
          ) : null}
          {resolutions.length > 0 ? (
            <Select
              id="preview-resolution"
              label="Preview resolution"
              value={String(resolution)}
              onChange={(next) => void chooseResolution(Number(next))}
              options={resolutions.map((name, index) => ({
                label: name,
                value: String(index),
              }))}
            />
          ) : null}

          {/* The outcome of the last action. It truncates rather than pushing the indicator
              out of its place at the right. */}
          <span className="app__status-message" role="status" aria-live="polite">
            {message}
          </span>

          <span className="app__version">{version}</span>

          {/* A spinner while a render is in flight, a quiet dot when idle. The dot is what
              makes the spinner mean something: an indicator that always moves says nothing
              about whether the program is busy. */}
          {busy || rendering ? (
            <span className="app__spinner" role="status" aria-label="Rendering" />
          ) : (
            <span className="app__status-idle" aria-hidden />
          )}
        </div>
      </header>

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
                onResize={onResize}
                onChange={requestRender}
                onZoomChange={setZoomPercent}
              />
            }
            plugins={
              <PluginsPanel
                onChange={requestRender}
                hasDocument={doc?.open === true}
                documentPath={doc?.path ?? ""}
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

      {pendingPath !== "" ? (
        <OpenDialog
          path={pendingPath}
          onConfirm={(space) => void openWithSpace(space)}
          onCancel={() => setPendingPath("")}
        />
      ) : null}

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
