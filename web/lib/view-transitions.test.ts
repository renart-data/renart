import { describe, expect, it } from "vitest";
import { viewTransitionsSupported } from "./view-transitions";

describe("view transitions", () => {
  it("skips them in the desktop window and keeps them in browsers", () => {
    expect(
      viewTransitionsSupported(
        "Mozilla/5.0 (X11; Ubuntu; Linux x86_64) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/60.5 Safari/605.1.15 wails.io/605.1.15",
      ),
    ).toBe(false);
    expect(
      viewTransitionsSupported(
        "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
      ),
    ).toBe(true);
    expect(
      viewTransitionsSupported(
        "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Safari/605.1.15",
      ),
    ).toBe(true);
  });
});
