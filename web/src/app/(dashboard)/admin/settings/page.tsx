import { CurrencyConfigAdmin } from "@/components/admin/currency-config";
import { PaymentChannelsAdmin } from "@/components/admin/payment-channels";
import { PlanManager } from "@/components/admin/plan-manager";
import { requireUser } from "@/lib/auth/session";

export const dynamic = "force-dynamic";

export default async function AdminSettingsPage() {
  const user = await requireUser();
  if (user.role !== "superadmin" && user.role !== "admin") {
    return (
      <div className="text-sm text-muted-foreground">
        需要管理员权限
      </div>
    );
  }
  return (
    <div className="space-y-8">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">系统设置</h1>
        <p className="text-sm text-muted-foreground">
          套餐、支付渠道、货币与系统参数配置
        </p>
      </div>

      <PlanManager />

      <div>
        <h3 className="mb-4 text-lg font-semibold">货币与汇率</h3>
        <CurrencyConfigAdmin />
      </div>

      <div>
        <h3 className="mb-4 text-lg font-semibold">支付渠道</h3>
        <PaymentChannelsAdmin />
      </div>
    </div>
  );
}
