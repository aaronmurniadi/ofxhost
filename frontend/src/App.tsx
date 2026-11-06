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
 *   M5       the filmstrip, the settings panel and the saved layout
 */

import { useCallback, useEffect, useState } from "react";
import { api, chainApi, type ChainDTO, type DocumentDTO } from "./api/document";
import OpenDialog from "./panels/OpenDialog";
import ParametersPanel from "./panels/ParametersPanel";
import PluginsPanel from "./panels/PluginsPanel";
import PreviewPanel, { type PreviewState } from "./panels/PreviewPanel";

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
        <span className="app__version">{version}</span>
      </header>

      <main className="app__layout">
        <PreviewPanel
          preview={preview}
          fileName={doc?.fileName ?? ""}
          zoomLabel="100%"
          busy={busy}
          onResize={onResize}
        />

        <PluginsPanel onChange={requestRender} />

        <ParametersPanel selected={chain?.selected ?? -1} onChange={requestRender} />

        <section className="panel panel--filmstrip" aria-label="Filmstrip">
          <p className="panel__placeholder">{message}</p>
        </section>
      </main>

      {pendingPath !== "" ? (
        <OpenDialog
          path={pendingPath}
          onConfirm={(space) => void openWithSpace(space)}
          onCancel={() => setPendingPath("")}
        />
      ) : null}
    </div>
  );
}
