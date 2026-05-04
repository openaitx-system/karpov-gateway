import { NextRequest, NextResponse } from "next/server";

import { serverEnv } from "@/lib/env/server";

/**
 * 通用 server-side 代理：把浏览器到 /api/proxy/* 的请求转发到 BACKEND_URL/v1/*。
 *
 * 设计要点（安全相关）：
 * 1. **单向白名单**：只允许 /v1/ 下的路径；防 SSRF 提权穿透到后端管理面。
 * 2. **CSRF 校验**：所有写方法（POST/PUT/PATCH/DELETE）必须带 cookie csrf_token
 *    + 对应 X-CSRF-Token header（double-submit）；这一层由 gateway 也校验，
 *    前端这里再校验一次属于纵深防御。
 * 3. **Cookie 透传白名单**：只透传 sid / csrf_token，其他浏览器 cookie 一律剥掉，
 *    避免污染后端会话。
 * 4. **响应头清洗**：去掉 set-cookie 之外的 hop-by-hop 头；保留后端 set-cookie 让
 *    浏览器更新 session/csrf。
 * 5. **超时**：AbortController + serverEnv.PROXY_TIMEOUT_MS。
 * 6. **请求体大小限制**：默认 1MB（防 DoS）。
 * 7. **方法白名单**：拒绝 CONNECT / TRACE / OPTIONS（OPTIONS 由 Next 自身处理 CORS）。
 * 8. **真实 IP 透传**：剥掉浏览器伪造的 X-Forwarded-*，由代理层重新注入可信
 *    X-Forwarded-For / X-Real-IP / X-Forwarded-Proto / X-Forwarded-Host；
 *    若 TRUST_PROXY=true（前置反代），保留前端反代设置的链并 append 本节点观察 IP。
 */

const ALLOWED_METHODS = new Set([
  "GET",
  "POST",
  "PUT",
  "PATCH",
  "DELETE",
  "HEAD",
]);
const WRITE_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"]);
const COOKIE_PASSTHROUGH = new Set<string>([
  serverEnv.SESSION_COOKIE_NAME,
  serverEnv.CSRF_COOKIE_NAME,
]);
const MAX_BODY_BYTES = 1024 * 1024; // 1MB

const HOP_BY_HOP = new Set([
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailers",
  "transfer-encoding",
  "upgrade",
]);

/**
 * 代理层错误码到「面向用户的中文友好提示」的映射。
 *
 * 设计原则：
 * - **不暴露内部实现细节**（不写"upstream"、"gateway"、IP 这类术语）。
 * - **告诉用户能做什么**（重试 / 稍后再试 / 刷新页面）。
 * - 保留 `error` 机器码不变，UI/埋点可继续按 code 分支；新增 `message` 给人看。
 */
const FRIENDLY_MESSAGES: Record<string, string> = {
  method_not_allowed: "当前操作不被支持，请刷新页面后重试。",
  invalid_path: "请求地址不合法，请刷新页面后重试。",
  csrf_invalid: "登录状态已过期或被篡改，请刷新页面后重新操作。",
  payload_too_large: "提交的内容过大，请精简后重试（上限 1 MB）。",
  upstream_timeout: "服务响应超时，请稍后重试。",
  upstream_unreachable: "暂时无法连接到服务，请检查网络或稍后重试。",
  upstream_bad_gateway: "服务暂时不可用，请稍后重试。",
  upstream_unavailable: "服务正在维护或负载过高，请稍后重试。",
};

export interface ProxyOptions {
  /** Next dynamic params 解析后得到的 path 段数组（按 pathPrefix 默认是不含 /v1 的） */
  pathSegments: string[];
  /**
   * 拼到 BACKEND_URL 后面的前缀。默认 "/v1/"，意味着前端 /api/proxy/<seg> → backend/v1/<seg>。
   * 文档中心的 "Try it" 需要 path 段已经包含 v1（来自原始 OpenAPI），所以传 "/"。
   */
  pathPrefix?: string;
}

/** 生成 request id（与 middleware.newRequestId 同算法，用于 /api/* 不经过 middleware 的情况）。 */
function newRequestId(): string {
  const buf = new Uint8Array(16);
  crypto.getRandomValues(buf);
  let hex = "";
  for (const b of buf) hex += b.toString(16).padStart(2, "0");
  return hex;
}

/**
 * 统一构造代理层错误响应。
 *
 * 响应体结构与客户端 `ApiError` 兼容：
 * - `error`：稳定的机器码（保持向后兼容，UI 可按 code 分支）。
 * - `message`：面向用户的中文提示（被 ApiError.message 直接展示）。
 * - `request_id`：便于用户向支持反馈 / 后端日志关联。
 */
function errorResponse(
  code: keyof typeof FRIENDLY_MESSAGES | string,
  status: number,
  opts: { requestId: string; extraHeaders?: HeadersInit; messageOverride?: string } = {
    requestId: "",
  },
): NextResponse {
  const message =
    opts.messageOverride ?? FRIENDLY_MESSAGES[code] ?? "请求未能完成，请稍后重试。";
  const headers = new Headers(opts.extraHeaders);
  headers.set("content-type", "application/json; charset=utf-8");
  headers.set("cache-control", "no-store");
  if (opts.requestId) headers.set("x-request-id", opts.requestId);
  return new NextResponse(
    JSON.stringify({ error: code, message, request_id: opts.requestId || undefined }),
    { status, headers },
  );
}

function buildTargetUrl(
  req: NextRequest,
  segments: string[],
  prefix: string,
): URL | null {
  // 路径段防穿越：禁止 ..、空段、绝对路径前缀
  for (const seg of segments) {
    if (!seg || seg === "." || seg === "..") return null;
    if (seg.includes("/") || seg.includes("\\")) return null;
  }
  const base = serverEnv.BACKEND_URL.replace(/\/+$/, "");
  // prefix 必须以 / 开头并以 / 结尾，便于直接拼接
  const p = prefix.startsWith("/") ? prefix : `/${prefix}`;
  const pn = p.endsWith("/") ? p : `${p}/`;
  const url = new URL(`${base}${pn}${segments.map(encodeURIComponent).join("/")}`);
  // 透传查询字符串
  for (const [k, v] of req.nextUrl.searchParams.entries()) {
    url.searchParams.append(k, v);
  }
  return url;
}

/**
 * 解析客户端真实 IP。
 *
 * - 信任反代（TRUST_PROXY=true）：取 X-Forwarded-For 链最左端（原始客户端）；
 *   退化到 X-Real-IP；再退化到 NextRequest 内置 IP（运行时填充）。
 * - 不信任反代：忽略请求里所有 X-Forwarded-*（防伪造），仅取 NextRequest 内置 IP。
 */
function pickClientIp(req: NextRequest): string | null {
  if (serverEnv.TRUST_PROXY) {
    const xff = req.headers.get("x-forwarded-for");
    if (xff) {
      const first = xff.split(",")[0]?.trim();
      if (first && isLikelyIp(first)) return first;
    }
    const xri = req.headers.get("x-real-ip");
    if (xri && isLikelyIp(xri.trim())) return xri.trim();
  }
  // NextRequest.ip 在 Next 14+ 由 runtime 填充（vercel / edge / node serverless）；
  // 自托管 node server 下可能为空——降级到无 IP 也安全。
  const direct = (req as unknown as { ip?: string }).ip;
  if (direct && isLikelyIp(direct)) return direct;
  return null;
}

function isLikelyIp(s: string): boolean {
  // 粗粒度 IPv4 / IPv6 / IPv6-with-zone 校验，仅用于挡明显垃圾
  if (s.length < 3 || s.length > 64) return false;
  // IPv4
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(s)) return true;
  // IPv6（含双冒号缩写、含 zone-id）
  if (/^[0-9a-fA-F:]+(%[A-Za-z0-9]+)?$/.test(s) && s.includes(":")) return true;
  return false;
}

function buildForwardedHeaders(req: NextRequest): Headers {
  const out = new Headers();
  for (const [name, value] of req.headers.entries()) {
    const lower = name.toLowerCase();
    if (HOP_BY_HOP.has(lower)) continue;
    if (lower === "host") continue;
    if (lower === "cookie") continue; // 单独处理
    if (lower === "content-length") continue; // fetch 自动算
    if (lower.startsWith("x-forwarded-") || lower === "x-real-ip" || lower === "forwarded") {
      // 由本层重新注入；浏览器伪造或反代设置稍后统一处理
      continue;
    }
    out.append(name, value);
  }

  // 注入 X-Forwarded-* 链
  const clientIp = pickClientIp(req);
  if (serverEnv.TRUST_PROXY) {
    // 保留前置反代的链（已被上面剥掉），重新拼装
    const upstreamXff = req.headers.get("x-forwarded-for");
    if (upstreamXff && clientIp) {
      out.set("x-forwarded-for", `${upstreamXff}, ${clientIp}`);
    } else if (upstreamXff) {
      out.set("x-forwarded-for", upstreamXff);
    } else if (clientIp) {
      out.set("x-forwarded-for", clientIp);
    }
  } else if (clientIp) {
    out.set("x-forwarded-for", clientIp);
  }
  if (clientIp) out.set("x-real-ip", clientIp);

  // 协议 / Host：始终用本层观察值（注意 host 用浏览器视角的 host，不是 BACKEND）
  const proto = (req.nextUrl.protocol || "https:").replace(":", "");
  out.set("x-forwarded-proto", proto);
  const host = req.headers.get("host");
  if (host) out.set("x-forwarded-host", host);

  // RFC 7239 Forwarded 头（gateway slog 也可读）
  const forwardedParts: string[] = [];
  if (clientIp) forwardedParts.push(`for=${quoteForwardedValue(clientIp)}`);
  if (host) forwardedParts.push(`host=${quoteForwardedValue(host)}`);
  forwardedParts.push(`proto=${proto}`);
  if (forwardedParts.length > 0) {
    out.set("forwarded", forwardedParts.join(";"));
  }

  return out;
}

function quoteForwardedValue(v: string): string {
  // RFC 7239：含 IPv6 / 端口 / 特殊字符时用引号包裹并转义
  if (/^[A-Za-z0-9._-]+$/.test(v)) return v;
  return `"${v.replace(/"/g, '\\"')}"`;
}

function filterCookies(rawCookie: string | null): string {
  if (!rawCookie) return "";
  const parts = rawCookie.split(";");
  const kept: string[] = [];
  for (const p of parts) {
    const idx = p.indexOf("=");
    if (idx < 0) continue;
    const name = p.slice(0, idx).trim();
    if (COOKIE_PASSTHROUGH.has(name)) {
      kept.push(p.trim());
    }
  }
  return kept.join("; ");
}

export async function proxyToBackend(
  req: NextRequest,
  { pathSegments, pathPrefix = "/v1/" }: ProxyOptions,
): Promise<NextResponse> {
  // /api/* 路由不经过 middleware（matcher 已排除），request id 在本层生成或继承。
  const requestId = req.headers.get("x-request-id") ?? newRequestId();

  const method = req.method.toUpperCase();
  if (!ALLOWED_METHODS.has(method)) {
    return errorResponse("method_not_allowed", 405, {
      requestId,
      extraHeaders: { allow: [...ALLOWED_METHODS].join(", ") },
    });
  }

  const target = buildTargetUrl(req, pathSegments, pathPrefix);
  if (!target) {
    return errorResponse("invalid_path", 400, { requestId });
  }

  // CSRF：写方法必须 header == cookie 值（double-submit）。
  if (WRITE_METHODS.has(method)) {
    const csrfCookie = req.cookies.get(serverEnv.CSRF_COOKIE_NAME)?.value;
    const csrfHeader = req.headers.get(serverEnv.CSRF_HEADER_NAME);
    if (!csrfCookie || !csrfHeader || csrfCookie !== csrfHeader) {
      return errorResponse("csrf_invalid", 403, { requestId });
    }
  }

  // body 读取（带大小上限）
  let body: BodyInit | null = null;
  if (method !== "GET" && method !== "HEAD") {
    const ab = await req.arrayBuffer();
    if (ab.byteLength > MAX_BODY_BYTES) {
      return errorResponse("payload_too_large", 413, { requestId });
    }
    body = ab.byteLength === 0 ? null : ab;
  }

  const headers = buildForwardedHeaders(req);
  const cookieStr = filterCookies(req.headers.get("cookie"));
  if (cookieStr) headers.set("cookie", cookieStr);
  // 把 request id 一并下发到后端，方便日志串联
  headers.set("x-request-id", requestId);

  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), serverEnv.PROXY_TIMEOUT_MS);

  let upstream: Response;
  try {
    upstream = await fetch(target, {
      method,
      headers,
      body,
      redirect: "manual",
      signal: ctrl.signal,
      cache: "no-store",
    });
  } catch (err) {
    clearTimeout(timer);
    const aborted = err instanceof Error && err.name === "AbortError";
    return errorResponse(
      aborted ? "upstream_timeout" : "upstream_unreachable",
      aborted ? 504 : 502,
      { requestId },
    );
  }
  clearTimeout(timer);

  // 上游 5xx 但响应体不是 JSON（典型：前置 nginx 返回的 502/503/504 HTML 页面）。
  // 直接流回会让 UI 解析失败、最终展示 `HTTP 502` 这种没有上下文的字样。
  // 这里替换为本层的友好 JSON；如果上游返回的是 JSON（后端自己生成的错误），仍然透传，
  // 以保留后端的 error/message/字段细节。
  if (upstream.status >= 500) {
    const upstreamCt = upstream.headers.get("content-type") ?? "";
    if (!upstreamCt.toLowerCase().includes("application/json")) {
      // 释放底层连接，避免内存泄露
      try {
        await upstream.body?.cancel();
      } catch {
        /* noop */
      }
      const code =
        upstream.status === 502
          ? "upstream_bad_gateway"
          : upstream.status === 503
            ? "upstream_unavailable"
            : upstream.status === 504
              ? "upstream_timeout"
              : "upstream_unreachable";
      const extra: Record<string, string> = {};
      const retryAfter = upstream.headers.get("retry-after");
      if (retryAfter) extra["retry-after"] = retryAfter;
      return errorResponse(code, upstream.status, {
        requestId,
        extraHeaders: extra,
      });
    }
  }

  // 流式回写
  const respHeaders = new Headers();
  upstream.headers.forEach((value, key) => {
    const lower = key.toLowerCase();
    if (HOP_BY_HOP.has(lower)) return;
    if (lower === "content-encoding" || lower === "content-length") return;
    respHeaders.append(key, value);
  });
  // set-cookie 单独以 append 方式保留多个（getSetCookie 在 Node 20+ 可用）
  const setCookies: string[] =
    typeof (upstream.headers as { getSetCookie?: () => string[] }).getSetCookie === "function"
      ? (upstream.headers as { getSetCookie: () => string[] }).getSetCookie()
      : [];
  if (setCookies.length > 0) {
    respHeaders.delete("set-cookie");
    for (const c of setCookies) respHeaders.append("set-cookie", c);
  }
  respHeaders.set("cache-control", "no-store");
  // request id 始终回写，方便用户向支持反馈
  if (!respHeaders.has("x-request-id")) respHeaders.set("x-request-id", requestId);

  return new NextResponse(upstream.body, {
    status: upstream.status,
    statusText: upstream.statusText,
    headers: respHeaders,
  });
}
