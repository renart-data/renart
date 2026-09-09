import { afterEach, expect, it, vi } from "vitest";
import { getDataBrowserConnections } from "./api-data-browser";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

it("bounds metadata discovery and exposes a retryable timeout", async () => {
  vi.useFakeTimers();
  vi.stubGlobal(
    "fetch",
    vi.fn(
      (_url, init) =>
        new Promise((_resolve, reject) => {
          init.signal.addEventListener("abort", () => reject(init.signal.reason), { once: true });
        }),
    ),
  );
  const pending = expect(getDataBrowserConnections("default")).rejects.toThrow(/timed out.*Retry/i);
  await vi.advanceTimersByTimeAsync(30_000);
  await pending;
});

it("cancels an obsolete connection listing without waiting for its timeout", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      (_url, init) =>
        new Promise((_resolve, reject) => {
          init.signal.addEventListener("abort", () => reject(init.signal.reason), { once: true });
        }),
    ),
  );
  const abort = new AbortController();
  const pending = getDataBrowserConnections("default", abort.signal);
  const rejected = expect(pending).rejects.toMatchObject({ name: "AbortError" });
  abort.abort();
  await rejected;
});
