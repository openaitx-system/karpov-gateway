import Link from "next/link";
import { Wallet } from "lucide-react";

import { ExtraUsagePanel } from "@/components/billing/extra-usage-panel";
import { Button } from "@/components/ui/button";
import { requireUser } from "@/lib/auth/session";

export const dynamic = "force-dynamic";

export default async function ExtraUsagePage() {
  await requireUser();
  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">超额使用</h1>
          <p className="text-sm text-muted-foreground">
            月度配额耗尽后允许从钱包余额按量扣费继续使用，可设置月度费用上限。
          </p>
        </div>
        <Button asChild variant="outline" size="sm">
          <Link href="/balance">
            <Wallet className="size-4" />
            管理余额 / 充值
          </Link>
        </Button>
      </div>
      <ExtraUsagePanel />
    </div>
  );
}
