import { describe, expect, test } from "vitest";
import { dataURIFor, MAX_ATTACHMENT_BYTES, splitDataURI, validateFile } from "./attachments";

function file(name: string, type: string, size: number): File {
  const f = new File(["x"], name, { type });
  Object.defineProperty(f, "size", { value: size });
  return f;
}

describe("validateFile", () => {
  test("accepts the four formats the daemon stores", () => {
    for (const type of ["image/png", "image/jpeg", "image/gif", "image/webp"]) {
      expect(validateFile(file("shot", type, 1000))).toBeNull();
    }
  });

  test("refuses anything else before a round trip", () => {
    // The daemon sniffs the bytes and refuses too; this only saves the trip.
    expect(validateFile(file("notes.txt", "text/plain", 10))).toContain("PNG");
  });

  test("refuses what the daemon's body cap would refuse", () => {
    expect(validateFile(file("huge.png", "image/png", MAX_ATTACHMENT_BYTES + 1))).toContain("büyük");
  });
});

describe("data URIs", () => {
  test("the upload route wants the base64 without the prefix", () => {
    expect(splitDataURI("data:image/png;base64,AAAA")).toBe("AAAA");
  });

  test("a value that is already bare is left alone", () => {
    expect(splitDataURI("AAAA")).toBe("AAAA");
  });

  test("and a preview is rebuilt from what the daemon returns", () => {
    // http://127.0.0.1 images are blocked by the app's CSP; data: ones are not.
    expect(dataURIFor("image/png", "AAAA")).toBe("data:image/png;base64,AAAA");
  });
});
