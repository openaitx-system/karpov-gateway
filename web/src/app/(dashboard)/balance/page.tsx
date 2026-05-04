import { BalancePanel } from "@/components/billing/balance-panel";
import { requireUser } from "@/lib/auth/session";

export const dynamic = "force-dynamic";

export default async function BalancePage() {
  await requireUser();
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">账户余额</h1>
        <p className="text-sm text-muted-foreground">
          管理钱包余额、充值，并查看交易流水。余额用于"超额使用"按量计费。
        </p>
      </div>
      <BalancePanel />
    </div>
  );
}
