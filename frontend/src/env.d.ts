/// <reference types="vite/client" />

/**
 * Declares the CSS modules and plain stylesheet imports Vite resolves.
 * A stylesheet import has no type of its own; TypeScript would reject it without
 * this declaration.
 */
declare module "*.css";
