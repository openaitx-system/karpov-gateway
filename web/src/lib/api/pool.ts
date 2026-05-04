"use client";

import { apiFetch } from "@/lib/api/client";
import type {
  AddPoolCredentialInput,
  PoolCredential,
  PoolCredentialList,
  PoolHealth,
} from "@/types/api";

/**
 * Pool admin REST 客户端。
 *
 * 后端契约（gateway/api/proto/v1/pool.proto）：
 *   GET    /v1/admin/pool/credentials?provider=&statusFilter=&limit=&offset=
 *   POST   /v1/admin/pool/credentials               { provider, label, payload(base64), capabilities[] }
 *   DELETE /v1/admin/pool/credentials/{id}
 *   GET    /v1/admin/pool/health/{provider}
 *
 * 浏览器 → /api/proxy/* → 后端；session cookie 由代理透传，CSRF 由 client.ts 自动注入。
 */
export const poolApi = {
  list(params: {
    provider?: string;
    statusFilter?: string;
    limit?: number;
    offset?: number;
  } = {}) {
    const qs = new URLSearchParams();
    if (params.provider) qs.set("provider", params.provider);
    if (params.statusFilter) qs.set("statusFilter", params.statusFilter);
    if (params.limit !== undefined) qs.set("limit", String(params.limit));
    if (params.offset !== undefined) qs.set("offset", String(params.offset));
    const tail = qs.toString();
    const path = `/admin/pool/credentials${tail ? `?${tail}` : ""}`;
    return apiFetch<PoolCredentialList>(path);
  },
  add(input: AddPoolCredentialInput) {
    return apiFetch<PoolCredential>("/admin/pool/credentials", {
      method: "POST",
      body: input,
    });
  },
  remove(id: string) {
    return apiFetch<void>(`/admin/pool/credentials/${encodeURIComponent(id)}`, {
      method: "DELETE",
    });
  },
  setStatus(id: string, status: "active" | "disabled") {
    return apiFetch<PoolCredential>(
      `/admin/pool/credentials/${encodeURIComponent(id)}/status`,
      { method: "PATCH", body: { id, status } },
    );
  },
  health(provider: string) {
    return apiFetch<PoolHealth>(
      `/admin/pool/health/${encodeURIComponent(provider)}`,
    );
  },
  refresh(id: string) {
    return apiFetch<PoolCredential>(
      `/admin/pool/credentials/${encodeURIComponent(id)}/refresh`,
      { method: "POST" },
    );
  },
  test(id: string, capability: string) {
    return apiFetch<{
      success: boolean;
      capability: string;
      latencyMs: number;
      error?: string;
    }>(
      `/admin/pool/credentials/${encodeURIComponent(id)}/test`,
      { method: "POST", body: { capability } },
    );
  },
};

/**
 * 把任意明文（凭证 payload，例如 cookie 字符串 / JSON）编成 base64。
 * 兼容旧浏览器：UTF-8 字符串 → btoa(unescape(encodeURIComponent(s))) 老把戏，
 * 但现代环境直接用 TextEncoder + Uint8Array → btoa。
 */
export function encodePayloadBase64(plain: string): string {
  if (typeof window === "undefined") {
    // SSR 环境用 Buffer 兜底（Next 16 server runtime）
    return Buffer.from(plain, "utf8").toString("base64");
  }
  const bytes = new TextEncoder().encode(plain);
  let bin = "";
  for (let i = 0; i < bytes.byteLength; i++) {
    bin += String.fromCharCode(bytes[i]!);
  }
  return btoa(bin);
}
