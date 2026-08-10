"use client";

import { useCallback, useLayoutEffect, useRef, type UIEventHandler } from "react";

const DEFAULT_BOTTOM_THRESHOLD = 24;

export function isNearScrollBottom(
  element: Pick<HTMLElement, "scrollHeight" | "clientHeight" | "scrollTop">,
  threshold = DEFAULT_BOTTOM_THRESHOLD,
) {
  return element.scrollHeight - element.clientHeight - element.scrollTop <= threshold;
}

/**
 * Follows appended output while the reader remains near the bottom. Scrolling
 * upward opts out until they return to the bottom or select a different run.
 */
export function useFollowOutputScroll(change: unknown, resetKey: unknown) {
  const viewportRef = useRef<HTMLDivElement | null>(null);
  const followingRef = useRef(true);
  const previousResetKeyRef = useRef(resetKey);

  const onViewportScroll = useCallback<UIEventHandler<HTMLDivElement>>((event) => {
    followingRef.current = isNearScrollBottom(event.currentTarget);
  }, []);

  useLayoutEffect(() => {
    if (previousResetKeyRef.current !== resetKey) {
      previousResetKeyRef.current = resetKey;
      followingRef.current = true;
    }

    const viewport = viewportRef.current;
    if (!viewport || !followingRef.current) return;
    viewport.scrollTop = viewport.scrollHeight;
  }, [change, resetKey]);

  return { viewportRef, onViewportScroll };
}
