import { PoolAdminClient } from "@/components/pool/pool-admin-client";

export const dynamic = "force-dynamic";

export default function PoolAdminPage() {
  return (
    <div className="space-y-6">
      <div className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">号池管理</h1>
        <p className="text-sm text-muted-foreground">
          管理各 provider 的凭证池与健康状态
        </p>
      </div>
      <PoolAdminClient />
    </div>
  );
}
