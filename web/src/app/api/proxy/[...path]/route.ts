import type { NextRequest } from "next/server";

import { proxyToBackend } from "@/lib/api/proxy";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

interface RouteContext {
  params: Promise<{ path: string[] }>;
}

async function handler(req: NextRequest, ctx: RouteContext) {
  const { path } = await ctx.params;
  return proxyToBackend(req, { pathSegments: path ?? [] });
}

export {
  handler as GET,
  handler as POST,
  handler as PUT,
  handler as PATCH,
  handler as DELETE,
  handler as HEAD,
};
