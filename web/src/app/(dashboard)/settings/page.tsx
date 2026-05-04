import { ChangePasswordCard } from "@/components/settings/change-password-card";
import { TOTPCard } from "@/components/settings/totp-card";
import { OAuthBindingsCard } from "@/components/settings/oauth-bindings-card";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { requireUser } from "@/lib/auth/session";

export const dynamic = "force-dynamic";

export default async function SettingsPage() {
  const user = await requireUser();
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">设置</h1>
        <p className="text-sm text-muted-foreground">账户安全与密码管理</p>
      </div>
      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">账户信息</CardTitle>
            <CardDescription>只读字段</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            <div>
              <span className="text-muted-foreground">邮箱：</span>
              {user.email}
            </div>
            <div>
              <span className="text-muted-foreground">角色：</span>
              {user.role}
            </div>
            <div>
              <span className="text-muted-foreground">状态：</span>
              {user.status}
            </div>
          </CardContent>
        </Card>
        <ChangePasswordCard />
        <TOTPCard enabled={!!user.totpEnabled} />
        <OAuthBindingsCard />
      </div>
    </div>
  );
}
