import { NextResponse } from "next/server";
import yaml from "js-yaml";

import { getCurrentUser } from "@/lib/auth/session";
import { isAdmin } from "@/lib/auth/rbac";
import { loadOpenApiYaml, parseSpec, stripAdminEndpoints } from "@/lib/docs/spec";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

/**
 * 下发 OpenAPI 规格 YAML：
 * - 文件源：仓库内 gateway/.../openapi.yaml（不依赖 gateway 二进制是否最新）
 * - 角色过滤：未登录或普通用户 → 剔除"管理员"分类下的接口；管理员 → 原文
 */
export async function GET() {
  const raw = await loadOpenApiYaml();
  if (!raw) {
    return new NextResponse("openapi.yaml not found", {
      status: 404,
      headers: { "Content-Type": "text/plain; charset=utf-8" },
    });
  }

  const user = await getCurrentUser();
  const admin = isAdmin(user?.role);

  // 管理员或解析失败时直接回原文，避免无谓改写
  if (admin) {
    return new NextResponse(raw, {
      status: 200,
      headers: {
        "Content-Type": "application/yaml; charset=utf-8",
        "Cache-Control": "private, max-age=60",
      },
    });
  }

  const parsed = parseSpec(raw);
  if (!parsed) {
    return new NextResponse(raw, {
      status: 200,
      headers: {
        "Content-Type": "application/yaml; charset=utf-8",
        "Cache-Control": "private, max-age=60",
      },
    });
  }
  const filtered = stripAdminEndpoints(parsed);
  const dumped = yaml.dump(filtered, { lineWidth: 120, noRefs: true });

  return new NextResponse(dumped, {
    status: 200,
    headers: {
      "Content-Type": "application/yaml; charset=utf-8",
      "Cache-Control": "private, max-age=60",
    },
  });
}
