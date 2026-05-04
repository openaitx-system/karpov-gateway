"use client";

import * as React from "react";
import { Check, Zap } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { PaymentMethodSelect } from "@/components/billing/payment-method-select";
import { billingApi, type Plan } from "@/lib/api/billing";
import { ApiError } from "@/lib/api/client";
import { cn } from "@/lib/utils";

function buildFeatures(plan: Plan): string[] {
  const features: string[] = [];
  if (plan.dailyLimit) features.push(`${plan.dailyLimit.toLocaleString()} 次/日 API 调用`);
  if (plan.monthlyLimit) features.push(`${plan.monthlyLimit.toLocaleString()} 次/月 API 调用`);
  if (plan.qps) features.push(`${plan.qps} QPS 限速`);
  if (plan.payAsYouGo && (plan.overagePricePer_1k ?? 0) > 0) {
    features.push(`超额按量计费 ¥${((plan.overagePricePer_1k ?? 0) / 100).toFixed(2)}/千次`);
  }
  const code = plan.code || plan.id;
  if (code === "free") features.push("基础歌曲搜索与详情", "社区支持");
  if (code === "basic") features.push("标准音质直链", "歌词查询");
  if (code === "pro") features.push("高品质音源链接", "歌词 / MV / 评论", "优先客服支持");
  if (code === "enterprise") features.push("全部 API 能力", "独立凭据池", "SLA 保障 + 专属支持");
  return features;
}

const PAY_AS_YOU_GO = {
  name: "按量付费",
  description: "超出套餐额度后按次计费",
  rates: [
    { endpoint: "歌曲搜索/详情", price: "¥0.001/次" },
    { endpoint: "歌曲链接", price: "¥0.003/次" },
    { endpoint: "歌词/MV", price: "¥0.001/次" },
    { endpoint: "其他 API", price: "¥0.001/次" },
  ],
};

export function PricingPanel() {
  const [plans, setPlans] = React.useState<Plan[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [checkoutPlan, setCheckoutPlan] = React.useState<Plan | null>(null);

  React.useEffect(() => {
    billingApi
      .listPlans()
      .then((res) => {
        const items = (res as any).plans ?? (res as any).items ?? [];
        setPlans(items);
      })
      .catch(() => toast.error("加载套餐失败"))
      .finally(() => setLoading(false));
  }, []);

  if (loading) {
    return (
      <div className="grid gap-6 md:grid-cols-3">
        {[0, 1, 2].map((i) => (
          <Card key={i} className="animate-pulse">
            <CardHeader className="h-32" />
            <CardContent className="h-48" />
          </Card>
        ))}
      </div>
    );
  }

  return (
    <>
      <div className="grid gap-6 md:grid-cols-3">
        {plans.map((plan) => {
          const features = buildFeatures(plan);
          const isPopular = plan.code === "pro";
          return (
            <Card
              key={plan.id || plan.code || String(Math.random())}
              className={cn(
                "relative flex flex-col",
                isPopular && "border-primary shadow-lg",
              )}
            >
              {isPopular && (
                <Badge className="absolute -top-3 left-1/2 -translate-x-1/2">
                  最受欢迎
                </Badge>
              )}
              <CardHeader>
                <CardTitle>{plan.name}</CardTitle>
                <CardDescription>{plan.period === "monthly" ? "月付" : "年付"}</CardDescription>
                <div className="mt-2">
                  <span className="text-3xl font-bold">
                    ¥{(plan.priceCents / 100).toFixed(0)}
                  </span>
                  {plan.priceCents > 0 && (
                    <span className="text-sm text-muted-foreground">/月</span>
                  )}
                  {plan.priceCents === 0 && (
                    <span className="ml-1 text-sm text-muted-foreground">永久免费</span>
                  )}
                </div>
              </CardHeader>
              <CardContent className="flex-1">
                <ul className="space-y-2">
                  {features.map((f) => (
                    <li key={f} className="flex items-start gap-2 text-sm">
                      <Check className="mt-0.5 size-4 shrink-0 text-primary" />
                      {f}
                    </li>
                  ))}
                </ul>
              </CardContent>
              <CardFooter>
                {plan.priceCents === 0 ? (
                  <Button variant="outline" className="w-full" disabled>
                    当前套餐
                  </Button>
                ) : (
                  <Button
                    className="w-full"
                    variant={isPopular ? "default" : "outline"}
                    onClick={() => setCheckoutPlan(plan)}
                  >
                    立即订阅
                  </Button>
                )}
              </CardFooter>
            </Card>
          );
        })}
      </div>

      {/* 按量付费卡片 */}
      <Card className="mt-6">
        <CardHeader>
          <div className="flex items-center gap-2">
            <Zap className="size-5 text-amber-500" />
            <CardTitle className="text-base">{PAY_AS_YOU_GO.name}</CardTitle>
          </div>
          <CardDescription>{PAY_AS_YOU_GO.description}</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="grid gap-3 sm:grid-cols-2 md:grid-cols-4">
            {PAY_AS_YOU_GO.rates.map((r) => (
              <div
                key={r.endpoint}
                className="rounded-lg border p-3 text-center"
              >
                <p className="text-sm text-muted-foreground">{r.endpoint}</p>
                <p className="mt-1 text-lg font-semibold">{r.price}</p>
              </div>
            ))}
          </div>
        </CardContent>
      </Card>

      <CheckoutDialog
        plan={checkoutPlan}
        onClose={() => setCheckoutPlan(null)}
      />
    </>
  );
}

function CheckoutDialog({
  plan,
  onClose,
}: {
  plan: Plan | null;
  onClose: () => void;
}) {
  const [provider, setProvider] = React.useState("mock");
  const [busy, setBusy] = React.useState(false);

  const onSubmit = async () => {
    if (!plan) return;
    if (!provider) {
      toast.error("请选择支付方式");
      return;
    }
    setBusy(true);
    try {
      const res = await billingApi.subscribe(plan.id || plan.code || "", provider);
      if (res.payUrl) {
        window.open(res.payUrl, "_blank");
        toast.success(`订单已创建，正在跳转支付页面...`);
        onClose();
      } else {
        toast.success(`订单已创建：${res.orderId}`);
        onClose();
      }
    } catch (err) {
      toast.error(
        err instanceof ApiError ? `订阅失败：${err.message}` : "网络异常",
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={!!plan} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>确认订阅</DialogTitle>
          <DialogDescription>
            {plan?.name} — ¥{((plan?.priceCents ?? 0) / 100).toFixed(2)}/
            {plan?.period === "monthly" ? "月" : "年"}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1">
            <label className="text-sm font-medium">支付方式</label>
            <PaymentMethodSelect
              value={provider}
              onChange={setProvider}
              disabled={busy}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button onClick={onSubmit} disabled={busy || !provider}>
            {busy ? "处理中..." : "确认支付"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
