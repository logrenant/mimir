import { describe, expect, test } from "vitest";

import { sameOrigin } from "./sameOrigin";

const req = (headers: Record<string, string | undefined>) => ({ headers });

describe("sameOrigin", () => {
  // The case the guard exists for: another page the developer has open, asking
  // the harness to spend the daemon token on a side effect it never needs to
  // read the reply of.
  test("refuses a cross-site request", () => {
    expect(
      sameOrigin(req({ origin: "https://evil.example", "sec-fetch-site": "cross-site" })),
    ).toBe(false);
  });

  test("refuses a cross-site request that sends no Origin", () => {
    expect(sameOrigin(req({ "sec-fetch-site": "cross-site" }))).toBe(false);
  });

  test("refuses a same-site-but-not-same-origin request", () => {
    expect(sameOrigin(req({ "sec-fetch-site": "same-site" }))).toBe(false);
  });

  test("refuses an Origin that is not the harness, whatever it claims", () => {
    expect(sameOrigin(req({ origin: "http://localhost:5173" }))).toBe(false);
    expect(sameOrigin(req({ origin: "http://evil.localhost:5174" }))).toBe(false);
  });

  test("allows the harness page itself", () => {
    expect(sameOrigin(req({ origin: "http://localhost:5174", "sec-fetch-site": "same-origin" })))
      .toBe(true);
    expect(sameOrigin(req({ origin: "http://127.0.0.1:5174" }))).toBe(true);
  });

  // A navigation and a terminal curl both look like this, and neither is the
  // request this guard is for.
  test("allows a request with no origin and no fetch metadata", () => {
    expect(sameOrigin(req({}))).toBe(true);
    expect(sameOrigin(req({ "sec-fetch-site": "none" }))).toBe(true);
  });
});
