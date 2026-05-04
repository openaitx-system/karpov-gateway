import "server-only";

import { cookies, headers } from "next/headers";
import { cache } from "react";

import { serverEnv } from "@/lib/env/server";
import type { User } from "@/types/api";

/**
 * 服务端调用 /v1/auth/me 解析当前会话用户。
 *
 * - 用 React `cache` 包裹，单次 RSC 渲染中自动去重；
 * - 透传 cookie 给后端 grpc-gateway，由后端 SessionMiddleware 决定 401 / 200；
 * - 透传客户端真实 IP 给后端审计日志（X-Forwarded-For / X-Real-IP）；
 * - 永不缓存到磁盘 / CDN（fetch 强制 no-store）；
 * - 失败一律返回 null，调用方决定是否跳转登录页（避免 RSC 渲染抛出）。
 */
export const getCurrentUser = cache(async (): Promise<User | null> => {
  try {
    const cookieStore = await cookies();
    const sid = cookieStore.get(serverEnv.SESSION_COOKIE_NAME)?.value;
    if (!sid) return null;

    const hdrs = await headers();
    const ua = hdrs.get("user-agent") ?? "";
    const ip = pickClientIp(hdrs);
    const proto =
      hdrs.get("x-forwarded-proto") ??
      (hdrs.get("origin")?.startsWith("https") ? "https" : "http");
    const host = hdrs.get("host") ?? "";

    const reqHeaders: Record<string, string> = {
      accept: "application/json",
      cookie: `${serverEnv.SESSION_COOKIE_NAME}=${sid}`,
      "user-agent": ua,
      "x-forwarded-proto": proto,
    };
    if (host) reqHeaders["x-forwarded-host"] = host;
    if (ip) {
      reqHeaders["x-forwarded-for"] = ip;
      reqHeaders["x-real-ip"] = ip;
    }

    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), serverEnv.PROXY_TIMEOUT_MS);

    const res = await fetch(`${serverEnv.BACKEND_URL}/v1/auth/me`, {
      method: "GET",
      headers: reqHeaders,
      cache: "no-store",
      signal: ctrl.signal,
    });
    clearTimeout(timer);

    if (!res.ok) return null;
    const user = (await res.json()) as User;
    if (!user?.id) return null;
    return user;
  } catch {
    return null;
  }
});

function pickClientIp(hdrs: Headers): string | null {
  if (serverEnv.TRUST_PROXY) {
    const xff = hdrs.get("x-forwarded-for");
    if (xff) {
      const first = xff.split(",")[0]?.trim();
      if (first) return first;
    }
    const xri = hdrs.get("x-real-ip");
    if (xri) return xri.trim();
  }
  return null;
}

/**
 * requireUser：服务端守卫——未登录则 throw。
 * 由调用页面通过 redirect() 处理；middleware 已做拦截，这是兜底防御。
 */
export async function requireUser(): Promise<User> {
  const user = await getCurrentUser();
  if (!user) {
    throw new Error("UNAUTHENTICATED");
  }
  return user;
}
