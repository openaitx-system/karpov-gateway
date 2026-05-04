import type { NextRequest } from "next/server";

import { proxyToBackend } from "@/lib/api/proxy";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

interface RouteContext {
  params: Promise<{ path: string[] }>;
}

/**
 * 文档中心 "Try it" 专用代理。
 *
 * 与 /api/proxy 的区别：
 * - /api/proxy 会自动把 backend 路径前缀拼成 /v1/<seg>（业务前端用，paths 不带 v1）。
 * - /api/docs-proxy 不加前缀，原样转发（OpenAPI spec 里的 path 已经包含 /v1/，
 *   或者像 /healthz 这种就是要打到根路径）。
 *
 * 安全/CSRF/cookie 透传逻辑与主代理一致。
 */
async function handler(req: NextRequest, ctx: RouteContext) {
  const { path } = await ctx.params;
  // 开发期日志：让用户/我们能从 next dev 控制台一眼看到 Try it 触发了哪条 URL
  if (process.env.NODE_ENV !== "production") {
    // eslint-disable-next-line no-console
    console.log(
      `[docs-proxy] ${req.method} /${(path ?? []).join("/")}${req.nextUrl.search}`,
    );
  }
  return proxyToBackend(req, { pathSegments: path ?? [], pathPrefix: "/" });
}

export {
  handler as GET,
  handler as POST,
  handler as PUT,
  handler as PATCH,
  handler as DELETE,
  handler as HEAD,
};
