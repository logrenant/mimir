import { describe, expect, test } from "vitest";

import { isTypingTarget } from "./keys";

function el(html: string): HTMLElement {
  const host = document.createElement("div");
  host.innerHTML = html;
  return host.firstElementChild as HTMLElement;
}

describe("isTypingTarget", () => {
  test("a text field is one, whatever its shape", () => {
    expect(isTypingTarget(el("<input>"))).toBe(true);
    expect(isTypingTarget(el('<input type="text">'))).toBe(true);
    expect(isTypingTarget(el('<input type="search">'))).toBe(true);
    expect(isTypingTarget(el("<textarea></textarea>"))).toBe(true);
    expect(isTypingTarget(el("<select></select>"))).toBe(true);
    expect(isTypingTarget(el('<div contenteditable="true"></div>'))).toBe(true);
    // The caret is in a child, so the guard has to look up the tree.
    expect(
      isTypingTarget(
        el('<div contenteditable="true"><p>bir</p></div>').querySelector("p"),
      ),
    ).toBe(true);
    // And an explicit opt-out is honoured.
    expect(isTypingTarget(el('<div contenteditable="false"></div>'))).toBe(false);
  });

  // The whole point of the row bindings these guards protect: a checkbox is
  // what Space is *supposed* to reach. Guarding it would break the feature the
  // guard exists to keep safe.
  test("a checkbox, a radio and a button are not", () => {
    expect(isTypingTarget(el('<input type="checkbox">'))).toBe(false);
    expect(isTypingTarget(el('<input type="radio">'))).toBe(false);
    expect(isTypingTarget(el('<input type="button">'))).toBe(false);
    expect(isTypingTarget(el("<button></button>"))).toBe(false);
    expect(isTypingTarget(el("<tr></tr>"))).toBe(false);
    expect(isTypingTarget(el("<div></div>"))).toBe(false);
  });

  test("nothing, and something that is not an element, are not", () => {
    expect(isTypingTarget(null)).toBe(false);
    expect(isTypingTarget(window as unknown as EventTarget)).toBe(false);
  });
});
