"use client";

/**
 * 客户端 fetch 封装：统一走 /api/proxy/* → 后端 /v1/*。
 *
 * 安全/正确性约定：
 * 1. 默认 credentials: 'same-origin'，浏览器自动带本站 cookie 给 Next 代理。
 * 2. 写方法自动注入 X-CSRF-Token（从 csrf_token cookie 读取，与代理层校验一致）。
 * 3. 永不透传 Authorization 头到非 /api/proxy 域；本封装只对自家 /api/proxy 工作。
 * 4. 401/403 抛 ApiError，让 UI 决定跳登录还是 toast。
 */
import { clientEnv } from "@/lib/env/client";

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    message: string,
    public readonly body?: unknown,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export type HttpMethod = "GET" | "HEAD" | "POST" | "PUT" | "PATCH" | "DELETE";

export interface ApiOptions {
  method?: HttpMethod;
  body?: unknown;
  signal?: AbortSignal;
  /** 默认 true；GET 时无效 */
  withCsrf?: boolean;
}

function readCookie(name: string): string | null {
  if (typeof document === "undefined") return null;
  const target = `${name}=`;
  const parts = document.cookie.split(";");
  for (const p of parts) {
    const t = p.trim();
    if (t.startsWith(target)) return decodeURIComponent(t.slice(target.length));
  }
  return null;
}

let csrfPrimed = false;

/** 首次调用时确保 csrf_token cookie 存在；幂等。 */
async function ensureCsrf(): Promise<string | null> {
  const existing = readCookie("csrf_token");
  if (existing) return existing;
  if (csrfPrimed) return readCookie("csrf_token");
  csrfPrimed = true;
  try {
    const res = await fetch("/api/csrf", {
      method: "GET",
      credentials: "same-origin",
      cache: "no-store",
    });
    if (!res.ok) return null;
    const json = (await res.json()) as { token?: string };
    return json.token ?? readCookie("csrf_token");
  } catch {
    return null;
  }
}

export async function apiFetch<T = unknown>(
  path: string,
  opts: ApiOptions = {},
): Promise<T> {
  const method = opts.method ?? "GET";
  const isWrite = method !== "GET" && method !== "HEAD";

  const headers = new Headers();
  headers.set("accept", "application/json");
  if (opts.body !== undefined && !(opts.body instanceof FormData)) {
    headers.set("content-type", "application/json");
  }
  if (isWrite && opts.withCsrf !== false) {
    const tok = (await ensureCsrf()) ?? readCookie("csrf_token");
    if (tok) headers.set("X-CSRF-Token", tok);
  }
  // 关联日志
  headers.set("X-Client-App", clientEnv.NEXT_PUBLIC_APP_NAME);

  const url = path.startsWith("/api/") ? path : `/api/proxy/${path.replace(/^\/+/, "")}`;
  const res = await fetch(url, {
    method,
    headers,
    body: opts.body === undefined
      ? undefined
      : opts.body instanceof FormData
        ? opts.body
        : JSON.stringify(opts.body),
    credentials: "same-origin",
    cache: "no-store",
    signal: opts.signal,
  });

  const ct = res.headers.get("content-type") ?? "";
  const parse = async () => {
    if (ct.includes("application/json")) return res.json();
    const text = await res.text();
    return text ? { message: text } : null;
  };

  if (!res.ok) {
    const body = await parse().catch(() => null);
    const message =
      (typeof body === "object" && body && "message" in body
        ? String((body as { message: unknown }).message)
        : null) ?? `HTTP ${res.status}`;
    throw new ApiError(res.status, message, body);
  }

  if (res.status === 204) return undefined as T;
  return (await parse()) as T;
}
