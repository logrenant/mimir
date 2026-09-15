import { useState } from "react";
import { AccountsProvider } from "./components/AccountsProvider";
import { AgentsProvider } from "./components/AgentPicker";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { RunsProvider } from "./components/RunsProvider";
import { TerminalsProvider } from "./components/TerminalsProvider";
import { Connection } from "./screens/Connection";
import { Dashboard } from "./screens/Dashboard";

/**
 * One gate, then the master screen. Nothing that talks to the daemon renders
 * until the handshake has actually succeeded; after that the Dashboard is the
 * whole app — its sidebar switches between the Board, the Terminals and the
 * Coding runner / Lead-gen modules, there is no separate top-level tab bar.
 *
 * The terminal sessions are held here, above Dashboard, so a run's socket
 * outlives the screen that opened it: switching from Terminals to the Board and
 * back must show what happened in between, not a panel that starts again.
 *
 * The accounts sit inside those: the picker, the manager and the dashboard all
 * need the same slots *and* the same live identities, and four copies of that
 * meant four `/accounts` fetches and a manager that could disagree with the
 * picker about the same slot.
 *
 * The run poll sits just inside them, for two reasons: the dashboard and the
 * board would otherwise each fan out one request per project on their own
 * timer, and each refresh is also what tells the terminals which running jobs
 * still have no console.
 */
export default function App() {
  const [connected, setConnected] = useState(false);

  if (!connected) return <Connection onReady={() => setConnected(true)} />;

  return (
    // Outside the providers, because a provider that throws while setting up
    // takes the whole tree with it, and an empty window is the one failure the
    // operator cannot report.
    <ErrorBoundary what="Mimir">
      <TerminalsProvider>
        <RunsProvider>
          <AccountsProvider>
            <AgentsProvider>
              <Dashboard />
            </AgentsProvider>
          </AccountsProvider>
        </RunsProvider>
      </TerminalsProvider>
    </ErrorBoundary>
  );
}
