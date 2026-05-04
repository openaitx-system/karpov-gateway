"use client";

import { apiFetch } from "@/lib/api/client";
import type { Balance, BalanceTransaction } from "@/lib/api/balance";

export interface AdminUser {
  id: string;
  email: string;
  status: string;
  role: string;
  planId: string;
  totpEnabled: boolean;
  createdAt: string;
}

export interface AdminUserListResp {
  items: AdminUser[];
  total: number;
  limit: number;
  offset: number;
}

export interface AdminAPIKeySummary {
  id: string;
  name: string;
  prefix: string;
  planId: string;
  status: string;
  createdAt: string;
  expiresAt?: string;
  lastUsedAt?: string;
}

export interface AdminUserDetail {
  user: AdminUser;
  balance?: Balance;
  apiKeys: AdminAPIKeySummary[];
  planCounts: Record<string, number>;
}

export interface ListUsersParams {
  email?: string;
  role?: string;
  status?: string;
  limit?: number;
  offset?: number;
}

export type BalanceOp = "credit" | "debit" | "set";

export interface AdjustBalanceBody {
  operation: BalanceOp;
  amountCents: number;
  description?: string;
  reference?: string;
}

interface Wrapped<T> {
  code: number;
  message: string;
  data: T;
}

interface ListResp<T> {
  items: T[];
  total: number;
  limit?: number;
  offset?: number;
}

function unwrap<T>(res: Wrapped<T> | T): T {
  if (
    res &&
    typeof res === "object" &&
    "data" in (res as object) &&
    "code" in (res as object)
  ) {
    return (res as Wrapped<T>).data;
  }
  return res as T;
}

function buildQS(params: Record<string, string | number | undefined>) {
  const usp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === "") continue;
    usp.set(k, String(v));
  }
  const s = usp.toString();
  return s ? `?${s}` : "";
}

export const adminUsersApi = {
  async list(params: ListUsersParams = {}) {
    const qs = buildQS(params as Record<string, string | number | undefined>);
    const res = await apiFetch<Wrapped<AdminUserListResp> | AdminUserListResp>(
      `/admin/users${qs}`,
    );
    return unwrap(res);
  },

  async detail(id: string) {
    const res = await apiFetch<Wrapped<AdminUserDetail> | AdminUserDetail>(
      `/admin/users/${encodeURIComponent(id)}`,
    );
    return unwrap(res);
  },

  async update(id: string, body: { status?: string; role?: string }) {
    const res = await apiFetch<Wrapped<AdminUser> | AdminUser>(
      `/admin/users/${encodeURIComponent(id)}`,
      { method: "PATCH", body },
    );
    return unwrap(res);
  },

  async resetPassword(id: string, newPassword: string) {
    const res = await apiFetch<Wrapped<{ ok: boolean }> | { ok: boolean }>(
      `/admin/users/${encodeURIComponent(id)}/password`,
      { method: "POST", body: { newPassword } },
    );
    return unwrap(res);
  },

  async getBalance(id: string) {
    const res = await apiFetch<Wrapped<Balance> | Balance>(
      `/admin/users/${encodeURIComponent(id)}/balance`,
    );
    return unwrap(res);
  },

  async adjustBalance(id: string, body: AdjustBalanceBody) {
    const res = await apiFetch<Wrapped<Balance> | Balance>(
      `/admin/users/${encodeURIComponent(id)}/balance`,
      { method: "POST", body },
    );
    return unwrap(res);
  },

  async listBalanceTransactions(id: string, limit = 50, offset = 0) {
    const res = await apiFetch<
      Wrapped<ListResp<BalanceTransaction>> | ListResp<BalanceTransaction>
    >(
      `/admin/users/${encodeURIComponent(id)}/balance/transactions?limit=${limit}&offset=${offset}`,
    );
    return unwrap(res);
  },

  async listKeys(id: string) {
    const res = await apiFetch<
      Wrapped<ListResp<AdminAPIKeySummary>> | ListResp<AdminAPIKeySummary>
    >(`/admin/users/${encodeURIComponent(id)}/keys`);
    return unwrap(res);
  },

  async setPlan(id: string, planId: string) {
    const res = await apiFetch<
      | Wrapped<{ planId: string; updatedKeys: number; totalKeys: number }>
      | { planId: string; updatedKeys: number; totalKeys: number }
    >(`/admin/users/${encodeURIComponent(id)}/plan`, {
      method: "POST",
      body: { planId },
    });
    return unwrap(res);
  },
};
