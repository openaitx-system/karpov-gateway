"use client";

import { apiFetch } from "@/lib/api/client";

export interface PaymentChannel {
  provider: string;
  enabled: boolean;
  yipayBaseUrl?: string;
  yipayMerchantId?: string;
  yipayKey?: string;
  yipayPaymentType?: string;
  hupijiaoBaseUrl?: string;
  hupijiaoAppId?: string;
  hupijiaoKey?: string;
  hupijiaoWapName?: string;
  // Linux Credit (linux.do) 官方 LDC 协议（type=ldcpay，Ed25519 签名）
  ldcBaseUrl?: string;
  ldcClientId?: string;
  ldcClientSecret?: string;
  ldcMerchantPrivateKey?: string;
  ldcPlatformPublicKey?: string;
}

interface Wrapped<T> {
  code: number;
  message: string;
  data: T;
}

type ChannelsPayload = { channels: PaymentChannel[] };

async function unwrap<T>(promise: Promise<Wrapped<T> | T>): Promise<T> {
  const res = await promise;
  if (res && typeof res === "object" && "data" in res && "code" in res) {
    return (res as Wrapped<T>).data;
  }
  return res as T;
}

/** 单条汇率：1 单位 code 货币 = rate 单位的基准币（rate 用字符串避免 float 精度坑）。 */
export interface CurrencyRate {
  code: string;
  rate: string;
}

export interface CurrencyConfig {
  defaultCurrency: string;
  rates: CurrencyRate[];
  updatedAt?: string;
}

type CurrencyPayload = { config: CurrencyConfig };

export interface CurrencyConfigInput {
  defaultCurrency: string;
  rates: CurrencyRate[];
}

export const settingsApi = {
  getPaymentChannels() {
    return unwrap(apiFetch<Wrapped<ChannelsPayload> | ChannelsPayload>("/admin/settings/payment"));
  },
  updatePaymentChannels(channels: PaymentChannel[]) {
    return unwrap(
      apiFetch<Wrapped<ChannelsPayload> | ChannelsPayload>("/admin/settings/payment", {
        method: "PUT",
        body: { channels },
      }),
    );
  },
  getCurrency() {
    return unwrap(
      apiFetch<Wrapped<CurrencyPayload> | CurrencyPayload>("/admin/settings/currency"),
    );
  },
  updateCurrency(input: CurrencyConfigInput) {
    return unwrap(
      apiFetch<Wrapped<CurrencyPayload> | CurrencyPayload>("/admin/settings/currency", {
        method: "PUT",
        body: input,
      }),
    );
  },
};
