import { NextResponse, type NextRequest } from "next/server";

/**
 * Edge middleware 路由守卫 + CSP nonce 注入。
 *
 * 三层逻辑：
 *  1. **公开路径**（匹配则直放）：登录/注册/根 redirect/静态资源/代理路径自身。
 *  2. **会话存在性闸门**：检查 SESSION_COOKIE_NAME cookie；不存在 → /login?next=...
 *  3. **管理员路径**（/admin/*）：cookie 存在但角色由 RSC 内服务端读 /v1/auth/me 判定；
 *     middleware 不做角色判断，因为 edge runtime 调用后端会增加首字节时延、
 *     且无法稳定共享 fetch cache；改在 admin layout server component 中通过 requireUser+isAdmin 拦截。
 *
 * CSP（Content Security Policy）：
 *  每个 HTML 请求生成一次性 nonce 写进 `Content-Security-Policy` 头的 `script-src 'nonce-XXX'`。
 *  把 nonce 通过 request header `x-nonce` 透给 Next 服务端，Next 16 会自动给所有内联
 *  `<script>` 标签加上 `nonce` 属性（用于 hydration / chunk 加载等必要内联脚本）。
 *  搭配 `'strict-dynamic'` 指令，nonce 信任会传播到由这些脚本动态加载的子脚本。
 *
 * 安全说明：
 *  - **会话存在 ≠ 会话有效**。即使 cookie 在，后端校验失败（过期/吊销）会在
 *    page-level RSC 调用 /v1/auth/me 时返回 null，进而 redirect 到 /login。
 *    middleware 仅做"无 cookie 直接拦"的快速分支，避免每个请求都打后端。
 *  - 注入 X-Request-Id 便于后端审计日志关联（gateway slog 已识别）。
 */
const PUBLIC_PATH_PREFIXES = [
  "/login",
  "/register",
  "/forgot-password",
  "/reset-password",
  "/verify-email",
  "/activate",
  "/oauth-error",
  "/_next",
  "/favicon",
  "/robots.txt",
  "/api/csrf",
  "/api/proxy",        // 代理自身做 cookie/CSRF 校验
  "/v1/auth/oauth/",   // OAuth start/callback/providers 直通给 gateway (rewrite); 不需要会话
];

function isPublic(pathname: string): boolean {
  if (pathname === "/") return true; // 主页本身做客户端跳转
  return PUBLIC_PATH_PREFIXES.some((p) => pathname.startsWith(p));
}

function newRequestId(): string {
  const buf = new Uint8Array(16);
  crypto.getRandomValues(buf);
  let hex = "";
  for (const b of buf) hex += b.toString(16).padStart(2, "0");
  return hex;
}

// 16 字节随机 → base64; CSP nonce 标准要求每请求唯一
function newNonce(): string {
  const buf = new Uint8Array(16);
  crypto.getRandomValues(buf);
  // Edge runtime 没有 Buffer, 用 btoa + String.fromCharCode 凑
  let bin = "";
  for (const b of buf) bin += String.fromCharCode(b);
  return btoa(bin);
}

// 构造 CSP. nonce + strict-dynamic 是 Next 16 推荐的 SPA 安全模型:
//   - 'nonce-XXX' 让 Next 注入的内联 hydration script 通过校验
//   - 'strict-dynamic' 信任由 nonce 标记的脚本"传播"出来的子脚本 (Next 的 chunk 加载)
//   - 'self' 兜底允许同源 .js 文件 (虽然 strict-dynamic 之下浏览器会忽略它)
function buildCSP(nonce: string, prod: boolean): string {
  return [
    "default-src 'self'",
    `script-src 'self' 'nonce-${nonce}' 'strict-dynamic'${prod ? "" : " 'unsafe-eval'"}`,
    "style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
    "img-src 'self' data: blob: https:",
    "font-src 'self' data: https://fonts.gstatic.com https://*.scalar.com",
    "connect-src 'self' https://*.scalar.com",
    "worker-src 'self' blob:",
    "frame-ancestors 'none'",
    "base-uri 'self'",
    "form-action 'self'",
    "object-src 'none'",
    prod ? "upgrade-insecure-requests" : "",
  ]
    .filter(Boolean)
    .join("; ");
}

export function middleware(req: NextRequest) {
  const { pathname, search } = req.nextUrl;
  const sessionCookie = process.env.SESSION_COOKIE_NAME ?? "sid";
  const reqId = req.headers.get("x-request-id") ?? newRequestId();

  // CSP nonce: 每请求一个; 通过 request header `x-nonce` 透传给 Next, Next 自动给 <script> 加 nonce 属性
  const nonce = newNonce();
  const csp = buildCSP(nonce, process.env.NODE_ENV === "production");

  // request 端 headers (Next.js RSC 会读这些)
  const reqHeaders = new Headers(req.headers);
  reqHeaders.set("x-nonce", nonce);
  reqHeaders.set("x-request-id", reqId);
  // Next.js 看到 Content-Security-Policy 这个 header 会知道当前请求开了 CSP, 会给 inline script 加 nonce
  reqHeaders.set("Content-Security-Policy", csp);

  // 给所有响应（pass-through / redirect / next）统一打上 CSP + reqId
  const decorate = (resp: NextResponse): NextResponse => {
    resp.headers.set("Content-Security-Policy", csp);
    resp.headers.set("x-request-id", reqId);
    return resp;
  };

  if (isPublic(pathname)) {
    return decorate(NextResponse.next({ request: { headers: reqHeaders } }));
  }

  const sid = req.cookies.get(sessionCookie)?.value;
  if (!sid) {
    const url = req.nextUrl.clone();
    url.pathname = "/login";
    url.search = `?next=${encodeURIComponent(pathname + search)}`;
    return decorate(NextResponse.redirect(url));
  }

  return decorate(NextResponse.next({ request: { headers: reqHeaders } }));
}

export const config = {
  matcher: [
    /*
     * 排除以下：
     * - api（路由处理器自带 server-side 逻辑）
     * - _next/static / _next/image / favicon
     * - 静态资源后缀
     */
    "/((?!api|_next/static|_next/image|favicon.ico|.*\\.(?:svg|png|jpg|jpeg|gif|webp|ico|css|js|map)$).*)",
  ],
};
