import { redirect } from "next/navigation";

import { isAdmin } from "@/lib/auth/rbac";
import { requireUser } from "@/lib/auth/session";

/**
 * 管理员路由守卫（服务端 RSC）。
 *
 * - middleware 已经做了"未登录直接拦截"；这里二次校验角色。
 * - 后端 AdminAuthMiddleware 也会再做一次校验（X-Admin-Key OR session role），
 *   即使前端 bypass，后端 403。
 */
export default async function AdminLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const user = await requireUser();
  if (!isAdmin(user.role)) {
    redirect("/dashboard?error=forbidden");
  }
  return <>{children}</>;
}
