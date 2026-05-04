"use client";

import { apiFetch } from "@/lib/api/client";

export interface Plan {
  id: string;
  code?: string;
  name: string;
  priceCents: number;
  currency?: string;
  period: string;
  qps?: number;
  softLimitPct?: number;
  dailyLimit?: number;
  monthlyLimit?: number;
  payAsYouGo?: boolean;
  overagePricePer_1k?: number;
}

export interface SubscribeResponse {
  orderId: string;
  payUrl: string;
  amount: string;
  currency: string;
  status: string;
  planName: string;
}

export interface Order {
  id: string;
  userId: string;
  planId: string;
  amount: string;
  currency: string;
  status: string;
  paidAt?: string;
}

interface Wrapped<T> { code: number; message: string; data: T }

async function unwrap<T>(promise: Promise<Wrapped<T> | T>): Promise<T> {
  const res = await promise;
  if (res && typeof res === "object" && "data" in res && "code" in res) {
    return (res as Wrapped<T>).data;
  }
  return res as T;
}

export interface MyPlan {
  userId: string;
  planId: string;
  planName?: string;
  priceCents: number;
  currency?: string;
  period?: string;
  qps: number;
  dailyLimit: number;
  monthlyLimit: number;
  softLimitPct: number;
  payAsYouGo: boolean;
  overagePricePer_1k: number;
}

/** 用户端可见的支付渠道（无密钥）。 */
export interface PaymentChannelPublic {
  provider: string;
  displayName: string;
  description?: string;
  icon?: string;
}

export const billingApi = {
  listPlans() {
    return unwrap(apiFetch<Wrapped<{ plans: Plan[] }> | { plans: Plan[] }>("/billing/plans"));
  },
  myPlan() {
    return unwrap(apiFetch<Wrapped<MyPlan> | MyPlan>("/billing/me/plan"));
  },
  subscribe(planId: string, paymentProvider: string) {
    // 每次点击生成全新 idempotencyKey；网络抖动重试同一笔由 caller 复用同一 key 即可。
    // 后端按此 key 在 CreateOrder 层 dedup —— 没有 nonce 会让第二次订阅命中老订单,
    // 把老 order.ID 喂给上游支付（LDC）触发 unique constraint 报错。
    const idempotencyKey =
      typeof crypto !== "undefined" && "randomUUID" in crypto
        ? crypto.randomUUID()
        : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
    return unwrap(
      apiFetch<Wrapped<SubscribeResponse> | SubscribeResponse>("/billing/orders", {
        method: "POST",
        body: {
          planId,
          paymentProvider,
          successUrl: `${window.location.origin}/billing?payment=success`,
          idempotencyKey,
        },
      }),
    );
  },
  getOrder(id: string) {
    return unwrap(
      apiFetch<Wrapped<Order> | Order>(`/billing/orders/${encodeURIComponent(id)}`),
    );
  },
  listOrders() {
    return unwrap(
      apiFetch<Wrapped<{ items: Order[]; total: number }> | { items: Order[]; total: number }>("/billing/orders"),
    );
  },
  /** 列举当前已启用、可下单的支付渠道（无密钥；下单 / 充值对话框用）。 */
  listPaymentChannels() {
    return unwrap(
      apiFetch<
        | Wrapped<{ channels: PaymentChannelPublic[] }>
        | { channels: PaymentChannelPublic[] }
      >("/billing/payment-channels"),
    );
  },
};
