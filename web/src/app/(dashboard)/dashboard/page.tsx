import { CheckCircle2, ShieldAlert } from "lucide-react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { RealtimeMetricsCards } from "@/components/dashboard/realtime-metrics";
import { UsageCards } from "@/components/dashboard/usage-cards";
import { UsageChart } from "@/components/dashboard/usage-chart";
import { Roles } from "@/lib/auth/rbac";
import { requireUser } from "@/lib/auth/session";
import { formatDateTime } from "@/lib/datetime";

export const dynamic = "force-dynamic";

export default async function DashboardPage() {
  const user = await requireUser();
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">仪表盘</h1>
        <p className="text-sm text-muted-foreground">
          账户、配额与服务状态总览
        </p>
      </div>
      <div className="grid gap-4 md:grid-cols-3">
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">账户</CardTitle>
            <CardDescription>{user.email}</CardDescription>
          </CardHeader>
          <CardContent className="flex items-center gap-2 text-sm">
            <Badge
              variant={
                user.role === Roles.SuperAdmin
                  ? "destructive"
                  : user.role === Roles.Admin
                    ? "default"
                    : "secondary"
              }
            >
              {user.role}
            </Badge>
            <span className="text-muted-foreground">{user.status}</span>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">两步验证</CardTitle>
            <CardDescription>账户安全</CardDescription>
          </CardHeader>
          <CardContent className="flex items-center gap-2 text-sm">
            {user.totpEnabled ? (
              <>
                <CheckCircle2 className="size-4 text-emerald-500" />
                已启用
              </>
            ) : (
              <>
                <ShieldAlert className="size-4 text-amber-500" />
                建议在「设置」中启用
              </>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">最近登录</CardTitle>
            <CardDescription>来自后端审计</CardDescription>
          </CardHeader>
          <CardContent className="text-sm text-muted-foreground">
            {formatDateTime(user.lastLoginAt, { fallback: "—" })}
          </CardContent>
        </Card>
      </div>

      <div>
        <h2 className="mb-3 text-lg font-semibold tracking-tight">实时吞吐量</h2>
        <RealtimeMetricsCards />
      </div>

      <div>
        <h2 className="mb-3 text-lg font-semibold tracking-tight">用量与配额</h2>
        <UsageCards />
      </div>

      <div>
        <UsageChart />
      </div>
    </div>
  );
}
