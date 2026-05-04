import Link from "next/link";
import { Zap } from "lucide-react";

import { PricingPanel } from "@/components/billing/pricing-panel";
import { Button } from "@/components/ui/button";
import { requireUser } from "@/lib/auth/session";

export const dynamic = "force-dynamic";

export default async function BillingPage() {
  await requireUser();
  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">套餐与计费</h1>
          <p className="text-sm text-muted-foreground">
            选择适合的套餐，或按量付费使用 API
          </p>
        </div>
        <Button asChild variant="outline" size="sm">
          <Link href="/extra-usage">
            <Zap className="size-4" />
            超额使用设置
          </Link>
        </Button>
      </div>
      <PricingPanel />
    </div>
  );
}
