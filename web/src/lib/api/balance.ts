"use client";

import { apiFetch } from "@/lib/api/client";

export interface Balance {
  userId: string;
  balanceCents: number;
  currency: string;
  version: number;
  updatedAt?: string;
}

export type BalanceTxnKind =
  | "topup"
  | "overage"
  | "refund"
  | "adjust"
  | "subscription";

export interface BalanceTransaction {
  id: number;
  userId: string;
  amountCents: number; // signed
  kind: BalanceTxnKind;
  orderId?: string;
  reference?: string;
  description?: string;
  balanceAfterCents: number;
  createdAt: string;
}

export interface ListTxnsResp {
  items: BalanceTransaction[];
  total: number;
  limit: number;
  offset: number;
}

export interface CreateTopupBody {
  amountCents: number;
  paymentProvider: string;
  successUrl?: string;
}

export interface CreateTopupResp {
  orderId: string;
  payUrl: string;
  amount: string;
  amountCents: number;
  currency: string;
  status: string;
  purpose: string;
}

interface Wrapped<T> {
  code: number;
  message: string;
  data: T;
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

export const balanceApi = {
  async get() {
    const res = await apiFetch<Wrapped<Balance> | Balance>("/billing/balance");
    return unwrap(res);
  },

  async listTransactions(limit = 50, offset = 0) {
    const res = await apiFetch<Wrapped<ListTxnsResp> | ListTxnsResp>(
      `/billing/balance/transactions?limit=${limit}&offset=${offset}`,
    );
    return unwrap(res);
  },

  async topup(body: CreateTopupBody) {
    const res = await apiFetch<Wrapped<CreateTopupResp> | CreateTopupResp>(
      "/billing/balance/topup",
      { method: "POST", body },
    );
    return unwrap(res);
  },
};
