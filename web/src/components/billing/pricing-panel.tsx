"use client";

import * as React from "react";
import { useRouter, useSearchParams } from "next/navigation";
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

const PENDING_ORDER_KEY = "karpov:pendingOrderId";
// 支付完成后轮询订单状态的窗口期 —— LDC 异步通知通常 1~3 秒到达，30 秒兜底.
const POLL_INTERVAL_MS = 1500;
const POLL_TIMEOUT_MS = 30000;

// pollUntilPaid 反复 GET /v1/billing/orders/{id} 直到 status 进入终态。
// 返回最终 status；超时返回 "timeout"。
async function pollUntilPaid(orderId: string): Promise<string> {
  const deadline = Date.now() + POLL_TIMEOUT_MS;
  while (Date.now() < deadline) {
    try {
      const order = (await billingApi.getOrder(orderId)) as { status?: string };
      const s = (order?.status ?? "").toString().toLowerCase();
      if (s === "paid" || s === "completed") return s;
      if (s === "failed" || s === "expired" || s === "canceled") return s;
    } catch {
      // 网络抖动 / 401 都吞掉, 继续轮询直到 deadline.
    }
    await new Promise((r) => setTimeout(r, POLL_INTERVAL_MS));
  }
  return "timeout";
}

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
  const router = useRouter();
  const searchParams = useSearchParams();

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

  // 处理 LDC / 易支付 return_url 跳回 /billing?payment=success 时的激活流程：
  //   1. 从 localStorage 取下单时存的 orderId
  //   2. 轮询订单状态直到 paid/completed (说明 async notify 已到达并履约成功)
  //   3. toast 成功 + router.refresh() 让上层 server component 重新拉 myPlan
  //   4. 清 query string 让用户不会反复触发
  React.useEffect(() => {
    const status = searchParams.get("payment");
    if (status !== "success") return;

    const orderId = window.localStorage.getItem(PENDING_ORDER_KEY);
    // 不论是否拿到 orderId, 都先把 query 清掉, 避免刷新或回退反复触发.
    const url = new URL(window.location.href);
    url.searchParams.delete("payment");
    window.history.replaceState({}, "", url.toString());

    if (!orderId) {
      // 用户从其它入口直接带着 ?payment=success 来 (例如收藏 URL), 没有
      // pending order, 静默 refresh 一下就好.
      router.refresh();
      return;
    }

    const tid = toast.loading("支付完成，正在激活套餐…");
    pollUntilPaid(orderId).then((finalStatus) => {
      window.localStorage.removeItem(PENDING_ORDER_KEY);
      toast.dismiss(tid);
      if (finalStatus === "paid" || finalStatus === "completed") {
        toast.success("套餐已激活");
        router.refresh();
      } else if (finalStatus === "timeout") {
        toast.warning(
          "未在 30 秒内收到支付通道的回调，请稍后刷新页面查看；如长时间未到账请联系客服",
        );
      } else {
        toast.error(`订单未成功 (${finalStatus})，如已扣款请联系客服`);
      }
    });
  }, [searchParams, router]);

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
        // 把 orderId 存下来, return_url 跳回 /billing?payment=success 时取出轮询.
        // 用 same-tab 跳转 (不是 _blank) 让用户付完款直接回到我们域内, PricingPanel
        // 的 ?payment=success effect 自动接手激活流程.
        if (res.orderId) {
          window.localStorage.setItem(PENDING_ORDER_KEY, res.orderId);
        }
        toast.success("订单已创建，正在跳转支付页面…");
        onClose();
        window.location.href = res.payUrl;
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
