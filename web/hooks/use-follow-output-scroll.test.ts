import { describe, expect, it } from "vitest";

import { isNearScrollBottom } from "@/hooks/use-follow-output-scroll";

describe("isNearScrollBottom", () => {
  it("keeps following while the reader is at or close to the bottom", () => {
    expect(isNearScrollBottom({ scrollHeight: 500, clientHeight: 200, scrollTop: 300 })).toBe(true);
    expect(isNearScrollBottom({ scrollHeight: 500, clientHeight: 200, scrollTop: 280 })).toBe(true);
  });

  it("stops following once the reader scrolls beyond the bottom threshold", () => {
    expect(isNearScrollBottom({ scrollHeight: 500, clientHeight: 200, scrollTop: 275 })).toBe(
      false,
    );
  });
});
