import { Component, type ErrorInfo, type ReactNode } from "react";

import { Button } from "./ui/button";
import { Card, CardBody, CardHeader } from "./ui/card";

/**
 * What a screen does when it throws.
 *
 * Without this, nothing: React unmounts the whole root on an uncaught render
 * error, so one bad field on one tab empties the window — no sidebar, no
 * message, nothing to click. `internal/api/brain.go` already names this
 * failure ("a blank screen with no message") and guards the daemon's half of
 * it; this is the other half, and it is the difference between a bug an
 * operator can report and a bug they can only describe as "it stopped".
 *
 * Two of these are mounted. The outer one, above the providers in `App`, is
 * the last resort. The inner one wraps the screen the Dashboard is showing, so
 * a screen that throws takes itself down and leaves the sidebar — and every
 * other tab — standing.
 *
 * `resetKey` is what makes the inner one recover: switching tabs changes it,
 * which clears the error, so the way out of a broken screen is to leave it.
 */
type Props = {
  children: ReactNode;
  /** Named in the message, so "Brain çöktü" rather than "bir şey çöktü". */
  what?: string;
  /** Changing this clears the error and re-renders the children. */
  resetKey?: string;
};

type State = { error: Error | null };

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // The console is where this is actually read: the shell's WebView
    // inspector is the only place a stack survives, and swallowing it here
    // would trade one silent failure for another.
    console.error("[mimir] screen crashed", error, info.componentStack);
  }

  componentDidUpdate(prev: Props) {
    if (this.state.error && prev.resetKey !== this.props.resetKey) {
      this.setState({ error: null });
    }
  }

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;

    const what = this.props.what ?? "Bu ekran";

    return (
      <div className="grid h-full place-items-center p-8">
        <Card elevation="raised" className="max-w-lg">
          <CardHeader
            title={`${what} çöktü`}
            subtitle="Daemon çalışmaya devam ediyor — kaybolan yalnızca bu görünüm."
          />
          <CardBody className="flex flex-col gap-3">
            <p className="rounded-md bg-sunken px-3.5 py-3 font-mono text-xs leading-relaxed break-words text-bad">
              {error.message || String(error)}
            </p>
            <div className="flex items-center gap-2.5">
              <Button icon="refresh" onClick={() => this.setState({ error: null })}>
                Yeniden dene
              </Button>
              <Button variant="secondary" onClick={() => window.location.reload()}>
                Pencereyi yenile
              </Button>
            </div>
          </CardBody>
        </Card>
      </div>
    );
  }
}
