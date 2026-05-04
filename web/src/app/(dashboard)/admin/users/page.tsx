import { UsersAdminClient } from "@/components/admin/users-admin-client";

export const dynamic = "force-dynamic";

export default function UsersAdminPage() {
  return (
    <div className="space-y-6">
      <div className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">用户管理</h1>
        <p className="text-sm text-muted-foreground">
          列举用户、修改角色与状态、调整余额、切换 API Key 套餐
        </p>
      </div>
      <UsersAdminClient />
    </div>
  );
}
