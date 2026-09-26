import { atom } from "jotai";
import type { UsageAnalyticsStatus } from "@/lib/generated/api-types";

// Disposable UI snapshots. The Go-owned per-user preferences remain authoritative.
export const usageAnalyticsStatusAtom = atom<UsageAnalyticsStatus | null>(null);
export const usageAnalyticsRevisionAtom = atom(0);
export const usageAnalyticsBusyAtom = atom(false);
export const usageAnalyticsErrorAtom = atom<string | null>(null);
export const usageAnalyticsSaveErrorAtom = atom<string | null>(null);
