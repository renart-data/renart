"use client";

import { useAtomValue } from "jotai";
import { useCallback, useEffect, useRef, useState } from "react";

import { getAssetCreationProfile } from "@/lib/api-assets";
import { selectedEnvironmentAtom } from "@/lib/atoms/domains/workspace";
import { getPinnedProjectId } from "@/lib/project-context";
import type { AssetCreationProfile } from "@/lib/types";

// The profile is large and changes only with connections or pipeline
// defaults. Opening a creation surface shows the last profile for its
// pipeline and environment at once and revalidates it in the background.
const maxCachedProfiles = 16;
const profileCache = new Map<string, AssetCreationProfile>();
const inFlightProfiles = new Map<string, Promise<AssetCreationProfile>>();

function profileCacheKey(pipelineId: string, environment: string) {
  return [getPinnedProjectId() ?? "", pipelineId, environment.trim()].join("\n");
}

function cacheProfile(key: string, profile: AssetCreationProfile) {
  profileCache.delete(key);
  profileCache.set(key, profile);
  while (profileCache.size > maxCachedProfiles) {
    const oldest = profileCache.keys().next().value;
    if (oldest === undefined) break;
    profileCache.delete(oldest);
  }
}

// One request serves every surface that asks for the same profile at once. A
// forced refresh (after creating a connection) never reuses an older request.
function fetchProfile(key: string, pipelineId: string, environment: string, force = false) {
  const existing = inFlightProfiles.get(key);
  if (existing && !force) return existing;
  const request = getAssetCreationProfile(pipelineId, environment)
    .then((profile) => {
      cacheProfile(key, profile);
      return profile;
    })
    .finally(() => {
      if (inFlightProfiles.get(key) === request) inFlightProfiles.delete(key);
    });
  inFlightProfiles.set(key, request);
  return request;
}

export function useAssetCreationProfile(pipelineId: string | undefined, enabled = true) {
  const environment = useAtomValue(selectedEnvironmentAtom) ?? "";
  const cacheKey = pipelineId ? profileCacheKey(pipelineId, environment) : "";
  const [profile, setProfile] = useState<AssetCreationProfile | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const requestRef = useRef(0);

  const refresh = useCallback(
    async (signal?: AbortSignal) => {
      if (!pipelineId || !enabled) return null;
      const request = ++requestRef.current;
      const cached = profileCache.get(cacheKey);
      if (cached) setProfile(cached);
      setLoading(!cached);
      setError("");
      try {
        // Explicit refreshes come without a signal and must see new connections.
        const next = await fetchProfile(cacheKey, pipelineId, environment, !signal);
        if (request === requestRef.current && !signal?.aborted) setProfile(next);
        return next;
      } catch (cause) {
        if (request === requestRef.current && !signal?.aborted && !cached) {
          setError(
            cause instanceof Error ? cause.message : "Could not load compatible connections.",
          );
        }
        return cached ?? null;
      } finally {
        if (request === requestRef.current && !signal?.aborted) setLoading(false);
      }
    },
    [cacheKey, enabled, environment, pipelineId],
  );

  useEffect(() => {
    if (!pipelineId || !enabled) {
      requestRef.current += 1;
      setProfile(null);
      setLoading(false);
      setError("");
      return;
    }
    setProfile(profileCache.get(cacheKey) ?? null);
    const controller = new AbortController();
    void refresh(controller.signal);
    return () => controller.abort();
  }, [cacheKey, enabled, pipelineId, refresh]);

  return { profile, loading, error, refresh };
}

// Warms the cache before a creation surface opens, so it renders compatible
// connections without waiting.
export function prefetchAssetCreationProfile(pipelineId: string, environment: string) {
  const key = profileCacheKey(pipelineId, environment);
  if (profileCache.has(key)) return;
  void fetchProfile(key, pipelineId, environment).catch(() => undefined);
}
