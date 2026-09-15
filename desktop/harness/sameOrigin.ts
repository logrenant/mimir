/**
 * Whether a request to the harness proxy came from the harness page itself.
 *
 * This is the one control standing between a dev server that signs requests
 * with the daemon's bearer token and any other page the developer happens to
 * have open. It lives in its own file rather than inline in
 * `vite.harness.config.ts` because a Vite config is outside `tsconfig.json`'s
 * `include` and cannot be typechecked — so the function that decides whether to
 * spend the token would have been the one piece of this app no gate ever read.
 *
 * `Sec-Fetch-Site` is the primary check: every browser that can make the
 * dangerous request also sends it. A missing `Origin` is allowed because that
 * is what a same-origin navigation and a terminal `curl` both look like, and
 * what it is *not* is a cross-site `fetch` — the browser always labels those.
 */
export const HARNESS_PORT = 5174;

const ALLOWED = new Set([
  `http://localhost:${HARNESS_PORT}`,
  `http://127.0.0.1:${HARNESS_PORT}`,
]);

type Headers = Record<string, string | string[] | undefined>;

export function sameOrigin(req: { headers: Headers }): boolean {
  const site = req.headers["sec-fetch-site"];
  if (typeof site === "string" && site !== "same-origin" && site !== "none") {
    return false;
  }
  const origin = req.headers.origin;
  if (typeof origin === "string" && origin !== "") return ALLOWED.has(origin);
  return true;
}
