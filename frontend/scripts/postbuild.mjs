/**
 * Puts the embed placeholder back after a build.
 *
 * `vite build` empties `dist/`, which deletes `dist/.gitkeep`. Without it a clone that
 * runs `go build` before `npm run build` fails, because go:embed refuses an empty
 * directory. Restoring it here means the file is never lost, instead of relying on
 * whoever commits to notice it went missing.
 */
import { writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const body = `The built frontend lands here.

This file keeps the directory present so the go:embed directive in embed.go has
something to embed before the first npm run build in frontend/.
`;
writeFileSync(join(root, "dist", ".gitkeep"), body);
