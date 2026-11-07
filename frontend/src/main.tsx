import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { TooltipProvider } from "@cloudflare/kumo/components/tooltip";
import App from "./App";

// Kumo's compiled stylesheet first, ours second.
//
// Import order is what decides a conflict, and Kumo ships the whole of Tailwind v4
// including preflight, which resets headings, margins and box sizing. Loading it first
// lets our own rules win on equal specificity instead of fighting a reset that arrives
// last. The alternative - picking over Kumo's stylesheet - would break on every update.
import "@cloudflare/kumo/styles/standalone";
import "./styles/app.css";

const root = document.getElementById("root");
if (!root) {
  throw new Error("the root element is missing from index.html");
}

createRoot(root).render(
  <StrictMode>
    {/* One provider for the whole window, so moving along a row of icon buttons shows
        each tooltip immediately after the first opens instead of waiting the full delay
        again for every one. */}
    <TooltipProvider>
      <App />
    </TooltipProvider>
  </StrictMode>,
);
