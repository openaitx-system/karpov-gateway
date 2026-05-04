"use client";

import { apiFetch } from "@/lib/api/client";

export interface UsageSummary {
  dayUsed: number;
  monthUsed: number;
  dayLimit: number;
  monthLimit: number;
  date: string;
  month: string;
}

export interface UsageDayRecord {
  date: string;
  provider: string;
  count: number;
  weightSum: number;
}

export interface UsageHistory {
  days: UsageDayRecord[];
  total: number;
}

export interface RealtimeMetrics {
  qpm: number;
  qps: number;
  avgLatencyMs: number;
  maxLatencyMs: number;
  minLatencyMs: number;
  totalToday: number;
  totalMonth: number;
  successRate: number;
  errorCount: number;
  dayUsed: number;
  dayLimit: number;
  monthUsed: number;
  monthLimit: number;
  timestamp: string;
}

interface Wrapped<T> {
  code: number;
  message: string;
  data: T;
}

export const usageApi = {
  async summary() {
    const res = await apiFetch<Wrapped<UsageSummary> | UsageSummary>("/usage/summary");
    return "data" in res && "code" in res ? (res as Wrapped<UsageSummary>).data : (res as UsageSummary);
  },
  async history(days = 30) {
    const res = await apiFetch<Wrapped<UsageHistory> | UsageHistory>(`/usage/history?days=${days}`);
    return "data" in res && "code" in res ? (res as Wrapped<UsageHistory>).data : (res as UsageHistory);
  },
  async realtime() {
    const res = await apiFetch<Wrapped<RealtimeMetrics> | RealtimeMetrics>("/usage/realtime");
    return "data" in res && "code" in res ? (res as Wrapped<RealtimeMetrics>).data : (res as RealtimeMetrics);
  },
};
