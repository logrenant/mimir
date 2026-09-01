import { useState } from "react";
import { Connection } from "./screens/Connection";
import { Dashboard } from "./screens/Dashboard";

/**
 * One gate, then the master screen. Nothing that talks to the daemon renders
 * until the handshake has actually succeeded; after that the Dashboard is the
 * whole app — its sidebar switches between the Coding runner and Lead-gen
 * modules, there is no separate top-level tab bar.
 */
export default function App() {
  const [connected, setConnected] = useState(false);

  if (!connected) return <Connection onReady={() => setConnected(true)} />;

  return <Dashboard />;
}
