import { ApiKeysPanel } from "@/components/api-keys/panel";
import { requireUser } from "@/lib/auth/session";

export const dynamic = "force-dynamic";

export default async function ApiKeysPage() {
  await requireUser();
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">API Keys</h1>
        <p className="text-sm text-muted-foreground">
          创建、查看与吊销服务调用密钥；明文仅在创建时显示一次。
        </p>
      </div>
      <ApiKeysPanel />
    </div>
  );
}
