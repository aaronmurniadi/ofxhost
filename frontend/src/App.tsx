/**
 * The application shell.
 *
 * At milestone M0 this is a placeholder that shows the four panel areas the
 * finished window will have, so the layout can be judged before there is
 * anything in it. Each panel becomes a real component from M2 onward:
 *
 *   Plugins     the chain list and the plugin picker
 *   Preview     the image, with CSS pan and zoom
 *   Parameters  the controls of the selected plugin
 *   Filmstrip   the thumbnails of the open directory
 *
 * The panel layout is persisted in localStorage under `ofxhost.layout.v1`
 * (see WAILS-PLAN.md §7.4). Nothing writes it yet.
 */
export default function App() {
  return (
    <div className="app">
      <header className="app__bar">
        <span className="app__title">OfxHost</span>
        <span className="app__hint">Milestone M0 — no image loading yet</span>
      </header>

      <main className="app__layout">
        <section className="panel panel--preview" aria-label="Preview">
          <p className="panel__placeholder">Open a RAW file to see the preview.</p>
        </section>

        <section className="panel panel--plugins" aria-label="Plugins">
          <h2 className="panel__title">Plugins</h2>
          <p className="panel__placeholder">No plugins discovered.</p>
        </section>

        <section className="panel panel--parameters" aria-label="Parameters">
          <h2 className="panel__title">Parameters</h2>
          <p className="panel__placeholder">Select a plugin in the chain.</p>
        </section>

        <section className="panel panel--filmstrip" aria-label="Filmstrip">
          <p className="panel__placeholder">Open a directory to see its images.</p>
        </section>
      </main>
    </div>
  );
}
