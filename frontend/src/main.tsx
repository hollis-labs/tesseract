import { applyTheme, getInitialTheme } from "@hollis-labs/sysop-ui";
import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import "./index.css";

// Apply the persisted Sysop UI palette before first paint.
applyTheme(getInitialTheme());

const root = document.getElementById("root");
if (!root) {
  throw new Error("root element #root not found");
}

ReactDOM.createRoot(root).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
