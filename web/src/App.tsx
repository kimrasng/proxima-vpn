import { Suspense } from "react";
import { Spinner } from "@cloudscape-design/components";
import { RouterProvider } from "react-router-dom";
import { router } from "./router";

function App() {
  return <Suspense fallback={<div style={{ padding: "2rem", textAlign: "center" }}><Spinner /></div>}><RouterProvider router={router} /></Suspense>;
}

export default App;
