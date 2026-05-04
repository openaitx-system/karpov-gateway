import { redirect } from "next/navigation";

import { getCurrentUser } from "@/lib/auth/session";

/**
 * 根路径：已登录跳 /dashboard，未登录跳 /login。
 *
 * 服务端组件，不挂载客户端 JS；middleware 已经做了无 cookie 拦截，
 * 这里再读取一次 /v1/auth/me 防止 cookie 过期但仍存在的情况。
 */
export default async function Home() {
  const user = await getCurrentUser();
  if (!user) {
    redirect("/login");
  }
  redirect("/dashboard");
}
