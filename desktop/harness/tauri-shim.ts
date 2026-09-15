/**
 * The Tauri IPC surface, as the browser can serve it.
 *
 * This exists for one reason: no visual claim about this app could be measured.
 * `design-review` needs the page in a browser to read a contrast ratio or a
 * computed font size off it, and the app does not open in one — `main.tsx`
 * calls `getCurrentWindow()` at module scope, and every REST call goes through
 * the Rust shell's `daemon_request`.
 *
 * It is aliased in over `@tauri-apps/api/*` by `vite.harness.config.ts` and it
 * is never part of a build: nothing under `desktop/src` imports it, and the
 * production Vite config does not know it exists. `lib/daemon.ts` stays the one
 * door this app talks to the daemon through — what changes is only what is
 * behind that door while a designer is looking at the page.
 *
 * The token never reaches the browser. `lib/daemon.ts`'s own comment explains
 * why a direct `fetch` would not work anyway — the daemon answers `OPTIONS`
 * with 401 because it carries no CORS headers on purpose — and the answer to
 * both problems is the same one: the dev server proxies `/daemon/*` in Node and
 * adds the `Authorization` header there. So this shim calls a same-origin path
 * and holds no secret at all.
 */

type ProxyResponse = { status: number; body: string };

const PREFIX = "/daemon";

async function daemonRequest(args: {
  method?: string;
  path?: string;
  body?: string | null;
}): Promise<ProxyResponse> {
  const method = args.method ?? "GET";
  const res = await fetch(PREFIX + (args.path ?? "/"), {
    method,
    headers: args.body == null ? undefined : { "Content-Type": "application/json" },
    body: args.body ?? undefined,
  });
  return { status: res.status, body: await res.text() };
}

/**
 * The shell commands that are not the daemon.
 *
 * They answer rather than throw, because a screen that renders an error banner
 * is not the screen anybody came here to look at. What they cannot do — reveal
 * a file in Finder, quit the app — they say in the console instead of faking.
 */
export async function invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  switch (cmd) {
    case "daemon_request":
      return (await daemonRequest((args ?? {}) as never)) as T;
    case "get_daemon_endpoint":
      // The proxy is the base url, and it needs no token on this side.
      return { state: "ready", base_url: PREFIX, token: "" } as T;
    case "webview_heartbeat":
      return undefined as T;
    case "restart_daemon":
      console.info("[harness] restart_daemon: the harness does not own the daemon");
      return undefined as T;
    default:
      console.info(`[harness] ${cmd} is a shell command and does nothing here`, args);
      return undefined as T;
  }
}

/** The main window, always. The tray panel is not what a design review reads. */
export function getCurrentWindow() {
  return {
    label: "main",
    async setFocus() {},
    async hide() {},
    async show() {},
  };
}

/** No shell events reach a browser tab; nothing here subscribes to one twice. */
export async function listen(): Promise<() => void> {
  return () => {};
}

/**
 * The rest of the module's surface, because a Tauri plugin imports it.
 *
 * `@tauri-apps/plugin-notification` pulls `addPluginListener` out of
 * `@tauri-apps/api/core` at module scope, and an alias that does not export it
 * fails the dependency optimizer rather than failing at the call. These are
 * stubs on purpose: a browser tab has no plugin host, and the alternative —
 * leaving them out — is a harness that will not start.
 */
export class Channel<T = unknown> {
  onmessage: (message: T) => void = () => {};
  toJSON() {
    return "__CHANNEL__:harness";
  }
}

export class PluginListener {
  constructor(
    readonly plugin: string,
    readonly event: string,
    readonly channelId: number,
  ) {}
  async unregister(): Promise<void> {}
}

export async function addPluginListener(
  plugin: string,
  event: string,
  _cb: (payload: unknown) => void,
): Promise<PluginListener> {
  return new PluginListener(plugin, event, 0);
}

export function convertFileSrc(filePath: string): string {
  return filePath;
}

export function transformCallback(callback?: (response: unknown) => void): number {
  void callback;
  return 0;
}

export async function once(): Promise<() => void> {
  return () => {};
}

export async function emit(): Promise<void> {}
