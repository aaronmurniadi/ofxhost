/**
 * The panel layout.
 *
 * Decision D9: the panels resize and toggle; they do not dock. That is the whole
 * model, and it is why this file is a Group and some Panels rather than a docking
 * manager.
 *
 * The sizes are not owned here. They come from the settings file and go back to it,
 * so a relaunch restores the window, and the View menu and a splitter drag write the
 * same three numbers. A panel collapsed to zero is a hidden panel: there is no
 * separate boolean that could disagree with the splitter.
 */

import { useCallback, useRef } from "react";
import {
  Group,
  Panel,
  Separator,
  type Layout,
  type PanelImperativeHandle,
} from "react-resizable-panels";
import { sessionApi, type PanelsDTO } from "../api/session";

/** The smallest a panel may be dragged to, in pixels. */
const MIN_PX = 120;

export interface WorkspaceProps {
  panels: PanelsDTO;
  onPanelsChange: (panels: PanelsDTO) => void;
  preview: React.ReactNode;
  plugins: React.ReactNode;
  parameters: React.ReactNode;
}

export default function Workspace({
  panels,
  onPanelsChange,
  preview,
  plugins,
  parameters,
}: WorkspaceProps) {
  // The View menu is drawn by the shell, which is outside this tree, so the refs
  // leave through the ref object the shell passes down. Holding them here and
  // publishing a toggle function is what keeps one state for a panel rather than a
  // menu flag that can disagree with the splitter.
  const rightRef = useRef<PanelImperativeHandle | null>(null);

  // save writes the whole layout, not one panel.
  //
  // A drag moves two panels at once, and each Group reports only its own children,
  // so a partial write would store a window that never existed. The other sizes are
  // carried over from the value already loaded.
  const save = useCallback(
    (layout: Layout) => {
      const next: PanelsDTO = {
        right: sizeOf(layout, "right", panels.right),
        plugins: sizeOf(layout, "plugins", panels.plugins),
      };
      if (next.right === panels.right && next.plugins === panels.plugins) {
        return;
      }
      void sessionApi
        .setPanels(next)
        .then(onPanelsChange)
        .catch(() => {
          // A failed save is reported by the Go side. The user keeps the size they
          // dragged to; the next successful drag writes it again.
        });
    },
    [onPanelsChange, panels],
  );

  return (
    <div className="workspace">
      <Group orientation="horizontal" className="workspace__group" onLayoutChanged={save}>
        <Panel id="preview" minSize={MIN_PX}>
          {preview}
        </Panel>

        <Separator className="separator separator--vertical" />

        <Panel
          id="right"
          panelRef={rightRef}
          minSize={MIN_PX}
          collapsible
          // Collapsing to zero is hiding. A zero stored in the settings file
          // therefore restores as a hidden panel with no special case.
          collapsedSize={0}
          defaultSize={`${panels.right}%`}
        >
          <Group orientation="vertical" onLayoutChanged={save}>
            <Panel
              id="plugins"
              minSize={MIN_PX}
              collapsible
              collapsedSize={0}
              defaultSize={`${panels.plugins}%`}
            >
              {plugins}
            </Panel>
            <Separator className="separator separator--horizontal" />
            <Panel id="parameters" minSize={MIN_PX}>
              {parameters}
            </Panel>
          </Group>
        </Panel>
      </Group>
    </div>
  );
}

/**
 * sizeOf reads one panel's size out of a layout, keeping the known value when the
 * group that reported the layout does not contain that panel.
 */
function sizeOf(layout: Layout, id: string, fallback: number): number {
  const value = layout[id];
  if (value === undefined) {
    return fallback;
  }
  // One decimal is enough for a percentage and keeps a saved file free of the
  // float noise a drag produces.
  return Math.round(value * 10) / 10;
}
