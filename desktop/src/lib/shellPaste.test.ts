import { describe, expect, test } from "vitest";
import { PASTE_IMAGE_KEY, shellPasteInput } from "./shellPaste";

describe("shellPasteInput", () => {
  test("an image alone becomes the keystroke the CLI reads the pasteboard on", () => {
    expect(shellPasteInput({ text: "", types: ["image/png"] })).toBe(PASTE_IMAGE_KEY);
  });

  test("text is left to xterm, which can type it", () => {
    expect(shellPasteInput({ text: "go test ./...", types: [] })).toBeNull();
  });

  test("a screenshot copied with its text is a text paste", () => {
    // Copying from a browser puts both flavours on the pasteboard. Typing the
    // text is what the operator asked for; ^V would swallow it.
    expect(shellPasteInput({ text: "caption", types: ["image/png", "text/html"] })).toBeNull();
  });

  test("a non-image file is not a paste this terminal can do anything with", () => {
    expect(shellPasteInput({ text: "", types: ["application/pdf"] })).toBeNull();
  });

  test("an empty pasteboard types nothing", () => {
    expect(shellPasteInput({ text: "", types: [] })).toBeNull();
  });
});
