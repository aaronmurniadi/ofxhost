import { writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import wails from "@wailsio/runtime/plugins/vite";

/**
 * embedPlaceholder keeps dist/ non-empty after a build.
 *
 * `vite build` empties the output directory, which deletes dist/.gitkeep. That file is
 * what lets `go build` succeed on a fresh clone, because go:embed refuses an empty
 * directory. Putting it back from a plugin rather than from an npm script means it
 * survives `npx vite build` too - which is exactly how it went missing before.
 */
function embedPlaceholder(): Plugin {
  return {
    name: "ofxhost-embed-placeholder",
    apply: "build",
    closeBundle() {
      writeFileSync(
        resolve(__dirname, "dist/.gitkeep"),
        `The built frontend lands here.

This file keeps the directory present so the go:embed directive in embed.go has
something to embed before the first npm run build in frontend/.
`,
      );
    },
  };
}

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [react(), wails("./bindings"), embedPlaceholder()],
});
