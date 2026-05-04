import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { serverEnv } from "@/lib/env/server";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

/**
 * 双提交 CSRF 引导端点。
 *
 * 流程：
 *  1. 浏览器 GET /api/csrf；
 *  2. 若 cookie 不存在则使用 Web Crypto 生成 32B 随机值 base64url，写入 cookie；
 *  3. 同时把同样的值返回给前端 JS，由前端塞进后续写请求的 X-CSRF-Token 头。
 *
 * 注意：cookie HttpOnly=false（必须让 JS 读到才能塞 header）；
 *       Secure=生产环境强制 true；SameSite=Strict 防跨站发请求。
 */
function randomToken(byteLen = 32): string {
  const buf = new Uint8Array(byteLen);
  crypto.getRandomValues(buf);
  let s = "";
  for (const b of buf) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

export async function GET() {
  const jar = await cookies();
  let token = jar.get(serverEnv.CSRF_COOKIE_NAME)?.value;
  if (!token || token.length < 32) {
    token = randomToken(32);
    jar.set(serverEnv.CSRF_COOKIE_NAME, token, {
      path: "/",
      httpOnly: false, // 必须让 JS 读
      sameSite: "strict",
      secure: serverEnv.NODE_ENV === "production",
      maxAge: 60 * 60 * 12, // 12h
    });
  }
  return NextResponse.json(
    { token },
    { headers: { "cache-control": "no-store" } },
  );
}
