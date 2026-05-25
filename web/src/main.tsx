import React from "react";
import ReactDOM from "react-dom/client";
import "@cloudscape-design/global-styles/index.css";
import "./i18n";
import { initTheme } from "./hooks/useTheme";
import App from "./App";

initTheme();

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
