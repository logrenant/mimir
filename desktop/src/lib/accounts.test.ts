import { describe, expect, test } from "vitest";
import type { Account, AccountStatus } from "./daemon";
import { distinctIdentities, identityClashes } from "./accounts";

function account(id: string, label: string): Account {
  return { id, label, config_dir: `/${id}`, is_default: false };
}

const A = account("a", "birinci");
const B = account("b", "ikinci");

function signedIn(email: string): AccountStatus {
  return { logged_in: true, email, subscription_type: "pro" };
}

describe("identityClashes", () => {
  test("two slots on the same account are one identity, and that is worth saying", () => {
    const clashes = identityClashes([A, B], {
      a: signedIn("me@example.com"),
      b: signedIn("me@example.com"),
    });
    expect(clashes).toHaveLength(1);
    expect(clashes[0].email).toBe("me@example.com");
    expect(clashes[0].labels.sort()).toEqual(["birinci", "ikinci"]);
  });

  test("two genuinely different accounts are not a clash", () => {
    expect(
      identityClashes([A, B], { a: signedIn("one@example.com"), b: signedIn("two@example.com") }),
    ).toEqual([]);
  });

  test("a slot that is not signed in has no identity to collide with", () => {
    expect(
      identityClashes([A, B], { a: signedIn("me@example.com"), b: { logged_in: false } }),
    ).toEqual([]);
  });

  test("an unreadable slot is not reported as a duplicate of anything", () => {
    expect(
      identityClashes([A, B], {
        a: signedIn("me@example.com"),
        b: { logged_in: false, error: "keychain locked" },
      }),
    ).toEqual([]);
  });

  test("nothing probed yet says nothing", () => {
    expect(identityClashes([A, B], {})).toEqual([]);
    expect(identityClashes(null, {})).toEqual([]);
  });
});

describe("distinctIdentities", () => {
  test("counts people, not slots — that is what capacity is worth", () => {
    expect(
      distinctIdentities([A, B], { a: signedIn("me@example.com"), b: signedIn("me@example.com") }),
    ).toBe(1);
    expect(
      distinctIdentities([A, B], { a: signedIn("one@example.com"), b: signedIn("two@example.com") }),
    ).toBe(2);
  });

  test("a lapsed login is not a lane", () => {
    expect(distinctIdentities([A, B], { a: signedIn("one@example.com"), b: { logged_in: false } })).toBe(1);
  });
});
