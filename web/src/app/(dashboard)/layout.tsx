import { redirect } from "next/navigation";

import { DashboardShell } from "@/components/layout/dashboard-shell";
import { getCurrentUser } from "@/lib/auth/session";

/**
 * 已登录用户的统一外壳：左侧导航 + 顶栏 + 内容区。
 *
 * 服务端守卫：未登录直接 redirect 到 /login，避免任何客户端 JS 执行；
 * middleware 已经在 cookie 缺失时拦截，这里是兜底（cookie 在但已过期）。
 */
export default async function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const user = await getCurrentUser();
  if (!user) redirect("/login");
  return <DashboardShell user={user}>{children}</DashboardShell>;
}
