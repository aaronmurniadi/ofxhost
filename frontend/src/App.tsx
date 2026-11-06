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

import { useCallback, useEffect, useState } from "react";
import { api, chainApi, type ChainDTO, type DocumentDTO } from "./api/document";
import { DEFAULT_PANELS, sessionApi, type PanelsDTO } from "./api/session";
import Workspace from "./layout/Workspace";
import ExportDialog from "./panels/ExportDialog";
import OpenDialog from "./panels/OpenDialog";
import ParametersPanel from "./panels/ParametersPanel";
import PluginsPanel from "./panels/PluginsPanel";
import PreviewPanel, { type PreviewState } from "./panels/PreviewPanel";
import SettingsDialog from "./panels/SettingsDialog";

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

  // applyAppearance repaints the shell.
  //
  // The theme is an attribute and the text size is a token, so a change costs one
  // style recalculation and no React re-render of the panels.
  const applyAppearance = useCallback((theme: string, fontPt: number) => {
    const root = document.documentElement;
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
    };

    window.setTimeout(tick, POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
    };
  }, [busy]);

  useEffect(() => {
    api.version().then(setVersion).catch(() => setVersion(""));
    chainApi.current().then(setChain).catch(() => setChain(null));
  }, []);

  const reloadChain = useCallback(() => {
    chainApi.current().then(setChain).catch(() => setChain(null));
  }, []);

  const requestRender = useCallback(() => {
    setRenderTick((tick) => tick + 1);
  }, []);

  // A chain or parameter change republishes the preview. The Go side runs the chain
  // and encodes the frame, so the frontend only swaps the URL.
  useEffect(() => {
    if (renderTick === 0) {
      return;
    }
    void (async () => {
      const published = await api.publishPreview();
      if (published.error) {
        setMessage(published.error);
        return;
      }
      setPreview({ url: published.url, width: published.width, height: published.height });
      reloadChain();
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

  const onResize = useCallback((width: number, height: number) => {
    // The Go side stores two numbers and nothing else, so a resize stream is cheap.
    void api.setPreviewPanelSize(width, height);
  }, []);

  return (
    <div className="app">
      <header className="app__bar">
        <span className="app__title">OfxHost</span>
        <span className="app__hint">
          {doc?.open ? `${doc.fileName} — ${doc.width}×${doc.height}` : "No image open"}
        </span>
        <button className="button" onClick={() => void chooseFile()}>
          Open…
        </button>
        {lastImage !== "" && !doc?.open ? (
          <button
            className="button"
            onClick={() => setPendingPath(lastImage)}
            title={lastImage}
          >
            Reopen last
          </button>
        ) : null}
        <button
          className="button"
          onClick={() => setExporting(true)}
          disabled={!doc?.open}
          title={doc?.open ? "Export the chain at full resolution" : "Open a file first"}
        >
          Export…
        </button>
        <button className="button" onClick={() => setSettingsOpen(true)}>
          Settings
        </button>
        <span className="app__version">{version}</span>
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
            />
          }
          plugins={<PluginsPanel onChange={requestRender} hasDocument={doc?.open === true} />}
          parameters={
            <ParametersPanel selected={chain?.selected ?? -1} onChange={requestRender} />
          }
        />
        ) : null}
      </main>

      {/* The status line carries the result of the last action, or why it failed.
          It is always present so the layout does not jump when a message appears. */}
      <footer className="app__status" role="status" aria-live="polite">
        {busy ? <span className="app__status--busy">Working…</span> : null}
        <span>{message}</span>
      </footer>

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
