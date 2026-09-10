import { describe, expect, it } from "vitest";
import { PreviewRequests, previewStatus } from "@/lib/preview";

describe("preview requests", () => {
  it("keeps a shared request alive until its last consumer leaves", async () => {
    const requests = new PreviewRequests<string>();
    const first = Symbol("canvas"),
      second = Symbol("inspect");
    const pending = requests.run("a", 200, async () => "ok", first);
    const shared = requests.run("a", 100, async () => "unused", second);
    requests.release("a", first);
    expect((await pending)?.value).toBe("ok");
    expect((await shared)?.value).toBe("ok");
    const cancelled = requests.run("a", 400, async () => "late", second);
    requests.release("a", second);
    expect(await cancelled).toBeNull();
  });
  it("coalesces consumers to the largest bound without retaining a query cache", async () => {
    const requests = new PreviewRequests<string>();
    let calls = 0;
    const fetch = async (limit: number) => {
      calls++;
      return String(limit);
    };
    const small = requests.run("asset:env:window", 25, fetch);
    const large = requests.run("asset:env:window", 200, fetch);
    const duplicate = requests.run("asset:env:window", 100, fetch);
    expect(await small).toBeNull();
    expect(await large).toEqual({ value: "200", limit: 200 });
    expect(await duplicate).toEqual({ value: "200", limit: 200 });
    expect(calls).toBe(1);
    await requests.run("asset:env:window", 200, fetch);
    expect(calls).toBe(2);
  });

  it("rejects late replies even when an adapter ignores cancellation", async () => {
    const requests = new PreviewRequests<string>();
    let finish!: (value: string) => void;
    const old = requests.run(
      "a",
      100,
      () =>
        new Promise<string>((resolve) => {
          finish = resolve;
        }),
    );
    await Promise.resolve();
    const current = requests.run("a", 200, async () => "current");
    finish("old");
    expect(await old).toBeNull();
    expect((await current)?.value).toBe("current");
  });

  it("cancels a scope and allows retrying a failed bound", async () => {
    const requests = new PreviewRequests<string>();
    const pending = requests.run("a", 100, async () => "stale");
    requests.cancel("a");
    expect(await pending).toBeNull();
    await expect(
      requests.run("a", 200, async () => {
        throw new Error("offline");
      }),
    ).rejects.toThrow("offline");
    expect(await requests.run("a", 200, async () => "ok")).toEqual({ value: "ok", limit: 200 });
  });
});

describe("preview status", () => {
  it("does not invent an exact total and explains ceilings", () => {
    expect(
      previewStatus({
        returned_rows: 100,
        has_more: true,
        continuation: "replace",
        limit: 100,
        next_limit: 200,
        result_id: "a",
      }),
    ).toBe("Showing 100 rows");
    expect(
      previewStatus({
        returned_rows: 1000,
        has_more: true,
        continuation: "none",
        reason: "row_limit",
        limit: 1000,
        result_id: "a",
      }),
    ).toContain("preview limit");
    expect(
      previewStatus({
        returned_rows: 0,
        has_more: true,
        continuation: "none",
        reason: "byte_limit",
        limit: 100,
        result_id: "a",
      }),
    ).toContain("size limit");
  });
});
