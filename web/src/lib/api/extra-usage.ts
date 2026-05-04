"use client";

import { apiFetch } from "@/lib/api/client";

export interface ExtraUsageSettings {
  userId: string;
  enabled: boolean;
  monthlyCapCents: number;
  notifyThresholdPct: number;
  updatedAt?: string;
}

export interface ExtraUsageReport {
  userId: string;
  yearMonth: string;
  enabled: boolean;
  monthlyCapCents: number;
  notifyThresholdPct: number;
  planId: string;
  planName: string;
  overagePricePer_1k: number;
  baseMonthlyLimit: number;
  baseMonthlyUsed: number;
  overageCount: number;
  overageWeight: number;
  overageCents: number;
  remainingCapCents: number;
  usedCapPct: number;
  nearLimit: boolean;
  balanceCents: number;
  balanceCurrency: string;
  updatedAt?: string;
}

export interface OverageCharge {
  userId: string;
  yearMonth: string;
  planId: string;
  count: number;
  weightSum: number;
  amountCents: number;
  updatedAt?: string;
}

export interface OverageEvent {
  id: number;
  userId: string;
  provider: string;
  endpoint: string;
  planId: string;
  weight: number;
  priceCents: number;
  timestamp: string;
}

interface Wrapped<T> {
  code: number;
  message: string;
  data: T;
}

interface ListResp<T> {
  items: T[];
  total: number;
}

function unwrap<T>(res: Wrapped<T> | T): T {
  if (res && typeof res === "object" && "data" in (res as object) && "code" in (res as object)) {
    return (res as Wrapped<T>).data;
  }
  return res as T;
}

export interface UpdateExtraUsageBody {
  enabled?: boolean;
  monthlyCapCents?: number;
  notifyThresholdPct?: number;
}

export const extraUsageApi = {
  async getSettings() {
    const res = await apiFetch<Wrapped<ExtraUsageSettings> | ExtraUsageSettings>(
      "/billing/extra-usage",
    );
    return unwrap(res);
  },

  async updateSettings(body: UpdateExtraUsageBody) {
    const res = await apiFetch<Wrapped<ExtraUsageSettings> | ExtraUsageSettings>(
      "/billing/extra-usage",
      { method: "PUT", body },
    );
    return unwrap(res);
  },

  async getReport(month?: string) {
    const qs = month ? `?month=${encodeURIComponent(month)}` : "";
    const res = await apiFetch<Wrapped<ExtraUsageReport> | ExtraUsageReport>(
      `/billing/extra-usage/report${qs}`,
    );
    return unwrap(res);
  },

  async listCharges(limit = 12) {
    const res = await apiFetch<Wrapped<ListResp<OverageCharge>> | ListResp<OverageCharge>>(
      `/billing/extra-usage/charges?limit=${limit}`,
    );
    return unwrap(res);
  },

  async listEvents(limit = 100) {
    const res = await apiFetch<Wrapped<ListResp<OverageEvent>> | ListResp<OverageEvent>>(
      `/billing/extra-usage/events?limit=${limit}`,
    );
    return unwrap(res);
  },
};
