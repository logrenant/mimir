import { describe, expect, it } from "vitest";

import {
  addPath,
  cleanPath,
  coveredBy,
  describePolicy,
  isDirty,
  removePath,
  shortPath,
} from "./scanPolicy";

describe("coveredBy", () => {
  // The bug this exists to prevent: a string prefix match makes `/a/b` cover
  // `/a/bravo`, and the operator ends up with a folder silently excluded that
  // they never named.
  it("matches whole segments, not string prefixes", () => {
    const list = ["/Users/x/private"];
    expect(coveredBy(list, "/Users/x/private")).toBe("/Users/x/private");
    expect(coveredBy(list, "/Users/x/private/deep/a.go")).toBe("/Users/x/private");
    expect(coveredBy(list, "/Users/x/privateer")).toBeNull();
    expect(coveredBy(list, "/Users/x/private-notes/a.go")).toBeNull();
  });

  it("ignores trailing separators on either side", () => {
    expect(coveredBy(["/a/b/"], "/a/b")).toBe("/a/b");
    expect(coveredBy(["/a/b"], "/a/b/")).toBe("/a/b");
  });
});

describe("addPath", () => {
  it("keeps the list sorted and free of duplicates", () => {
    let list: string[] = [];
    list = addPath(list, "/Users/x/docs");
    list = addPath(list, "/Users/x/dev");
    list = addPath(list, "/Users/x/docs");
    expect(list).toEqual(["/Users/x/dev", "/Users/x/docs"]);
  });

  // Adding a file under an already-excluded folder is a click that must leave
  // the list alone: a line that can never fire is a line the operator has to
  // reason about for nothing.
  it("does not add a path already covered by a parent", () => {
    const list = ["/Users/x/private"];
    expect(addPath(list, "/Users/x/private/keys.txt")).toEqual(list);
    expect(addPath(list, "/Users/x/private")).toEqual(list);
  });

  // The other direction: excluding a folder should absorb the files under it
  // that were listed separately.
  it("supersedes entries the new path covers", () => {
    const list = ["/Users/x/private/a.env", "/Users/x/private/b.env", "/Users/x/dev"];
    expect(addPath(list, "/Users/x/private")).toEqual(["/Users/x/dev", "/Users/x/private"]);
  });

  it("ignores blank input", () => {
    expect(addPath(["/a"], "   ")).toEqual(["/a"]);
  });
});

describe("removePath", () => {
  it("removes exactly the entry named, not what it covers", () => {
    const list = ["/a/b", "/a/b/c"];
    expect(removePath(list, "/a/b")).toEqual(["/a/b/c"]);
  });

  it("tolerates a trailing separator", () => {
    expect(removePath(["/a/b"], "/a/b/")).toEqual([]);
  });
});

describe("isDirty", () => {
  it("is false for an untouched draft", () => {
    const saved = { roots: ["/a"], excludes: ["/a/b"] };
    expect(isDirty({ roots: ["/a"], excludes: ["/a/b"] }, saved)).toBe(false);
  });

  it("notices a removal as well as an addition", () => {
    const saved = { roots: ["/a", "/b"], excludes: [] };
    expect(isDirty({ roots: ["/a"], excludes: [] }, saved)).toBe(true);
    expect(isDirty({ roots: ["/a", "/b", "/c"], excludes: [] }, saved)).toBe(true);
  });

  it("notices an exclusion change with the roots untouched", () => {
    const saved = { roots: ["/a"], excludes: [] };
    expect(isDirty({ roots: ["/a"], excludes: ["/a/secret"] }, saved)).toBe(true);
  });
});

describe("shortPath", () => {
  it("keeps the last two segments", () => {
    expect(shortPath("/Users/x/development/mimir")).toBe("…/development/mimir");
  });

  it("leaves a short path alone", () => {
    expect(shortPath("/Users/x")).toBe("/Users/x");
  });
});

describe("describePolicy", () => {
  // A scan with no roots reads as broken. It has to be named as a setting, or
  // the operator goes looking for a fault that is not there.
  it("calls out an empty root list", () => {
    expect(describePolicy({ roots: [], excludes: [] })).toBe("hiçbir klasör taranmıyor");
  });

  it("counts rather than listing", () => {
    expect(describePolicy({ roots: ["/a", "/b"], excludes: [] })).toBe("2 klasör");
    expect(describePolicy({ roots: ["/a"], excludes: ["/a/x"] })).toBe("1 klasör · 1 hariç");
  });
});

describe("cleanPath", () => {
  it("leaves the root separator alone", () => {
    expect(cleanPath("/")).toBe("/");
  });

  it("strips trailing separators", () => {
    expect(cleanPath("/a/b///")).toBe("/a/b");
  });
});
