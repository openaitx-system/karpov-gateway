"use client";

import * as React from "react";
import { Pencil, Plus, Trash2, Loader2, Zap, Clock, Database } from "lucide-react";
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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { apiFetch } from "@/lib/api/client";
import { ApiError } from "@/lib/api/client";

interface Plan {
  id: string;
  name: string;
  description: string;
  priceCents: number;
  currency: string;
  period: string;
  qps: number;
  dailyLimit: number;
  monthlyLimit: number;
  softLimitPct: number;
  payAsYouGo: boolean;
  overagePricePer_1k: number;
  sortOrder: number;
  isActive: boolean;
}

interface Wrapped<T> { code: number; message: string; data: T }

async function unwrap<T>(p: Promise<Wrapped<T> | T>): Promise<T> {
  const r = await p;
  if (r && typeof r === "object" && "data" in r && "code" in r) return (r as Wrapped<T>).data;
  return r as T;
}

const planApi = {
  async list(): Promise<Plan[]> {
    const res = await unwrap(apiFetch<Wrapped<{ plans: Plan[] }>>("/billing/plans"));
    return (res as any).plans ?? [];
  },
  async upsert(plan: Plan): Promise<Plan> {
    return unwrap(apiFetch<Wrapped<Plan>>(`/billing/plans/${plan.id}`, { method: "PUT", body: plan }));
  },
  async remove(id: string): Promise<void> {
    await apiFetch(`/billing/plans/${id}`, { method: "DELETE" });
  },
};

export function PlanManager() {
  const [plans, setPlans] = React.useState<Plan[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [editPlan, setEditPlan] = React.useState<Plan | null>(null);
  const [isNew, setIsNew] = React.useState(false);

  const refresh = () => {
    setLoading(true);
    planApi.list().then(setPlans).catch(() => toast.error("加载套餐失败")).finally(() => setLoading(false));
  };

  React.useEffect(refresh, []);

  const onEdit = (plan: Plan) => { setEditPlan({ ...plan }); setIsNew(false); };
  const onNew = () => {
    setEditPlan({
      id: "", name: "", description: "", priceCents: 0, currency: "CNY",
      period: "monthly", qps: 5, dailyLimit: 100, monthlyLimit: 1000,
      softLimitPct: 80, payAsYouGo: false, overagePricePer_1k: 0,
      sortOrder: plans.length, isActive: true,
    });
    setIsNew(true);
  };
  const onDelete = async (id: string) => {
    if (!confirm(`确定下架套餐「${id}」？已订阅用户不受影响。`)) return;
    try {
      await planApi.remove(id);
      toast.success("已下架");
      refresh();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "操作失败");
    }
  };

  if (loading) {
    return <div className="text-sm text-muted-foreground animate-pulse py-8 text-center">加载中...</div>;
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-lg font-semibold">套餐管理</h3>
        <Button size="sm" onClick={onNew}><Plus className="mr-1 size-4" />新增套餐</Button>
      </div>

      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        {plans.map((plan) => (
          <Card key={plan.id} className="relative">
            {!plan.isActive && (
              <Badge variant="secondary" className="absolute top-2 right-2">已下架</Badge>
            )}
            <CardHeader className="pb-2">
              <CardTitle className="text-base">{plan.name}</CardTitle>
              <CardDescription>{plan.description || plan.id}</CardDescription>
            </CardHeader>
            <CardContent className="space-y-2 text-sm">
              <div className="flex justify-between">
                <span className="text-muted-foreground">价格</span>
                <span className="font-medium">¥{(plan.priceCents / 100).toFixed(2)}/{plan.period === "monthly" ? "月" : "年"}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground flex items-center gap-1"><Zap className="size-3" />QPS</span>
                <span>{plan.qps}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground flex items-center gap-1"><Clock className="size-3" />日限额</span>
                <span>{plan.dailyLimit.toLocaleString()}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground flex items-center gap-1"><Database className="size-3" />月限额</span>
                <span>{plan.monthlyLimit.toLocaleString()}</span>
              </div>
              {plan.payAsYouGo && (
                <div className="flex justify-between">
                  <span className="text-muted-foreground">超额计费</span>
                  <span>¥{(plan.overagePricePer_1k / 100).toFixed(2)}/千次</span>
                </div>
              )}
            </CardContent>
            <CardFooter className="gap-2">
              <Button size="sm" variant="outline" onClick={() => onEdit(plan)}>
                <Pencil className="mr-1 size-3" />编辑
              </Button>
              {plan.id !== "free" && (
                <Button size="sm" variant="ghost" className="text-destructive" onClick={() => onDelete(plan.id)}>
                  <Trash2 className="mr-1 size-3" />下架
                </Button>
              )}
            </CardFooter>
          </Card>
        ))}
      </div>

      <PlanEditDialog
        plan={editPlan}
        isNew={isNew}
        onClose={() => setEditPlan(null)}
        onSaved={() => { setEditPlan(null); refresh(); }}
      />
    </div>
  );
}

function PlanEditDialog({ plan, isNew, onClose, onSaved }: {
  plan: Plan | null; isNew: boolean; onClose: () => void; onSaved: () => void;
}) {
  const [form, setForm] = React.useState<Plan | null>(null);
  const [saving, setSaving] = React.useState(false);

  React.useEffect(() => { setForm(plan ? { ...plan } : null); }, [plan]);

  if (!form) return null;

  const update = (patch: Partial<Plan>) => setForm({ ...form, ...patch });

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.id.trim()) { toast.error("ID 不能为空"); return; }
    if (!form.name.trim()) { toast.error("名称不能为空"); return; }
    setSaving(true);
    try {
      await planApi.upsert(form);
      toast.success(isNew ? "套餐已创建" : "套餐已更新");
      onSaved();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "保存失败");
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={!!plan} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{isNew ? "新增套餐" : `编辑套餐 · ${form.name}`}</DialogTitle>
          <DialogDescription>修改后立即生效，新订阅将使用更新后的配置。</DialogDescription>
        </DialogHeader>
        <form onSubmit={onSubmit} className="space-y-4">
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label>套餐 ID</Label>
              <Input value={form.id} onChange={(e) => update({ id: e.target.value })} disabled={!isNew} placeholder="pro" />
            </div>
            <div className="space-y-1.5">
              <Label>名称</Label>
              <Input value={form.name} onChange={(e) => update({ name: e.target.value })} placeholder="专业版" />
            </div>
          </div>
          <div className="space-y-1.5">
            <Label>描述</Label>
            <Input value={form.description} onChange={(e) => update({ description: e.target.value })} placeholder="适合团队与商业项目" />
          </div>
          <div className="grid gap-3 sm:grid-cols-3">
            <div className="space-y-1.5">
              <Label>价格（分）</Label>
              <Input type="number" value={form.priceCents} onChange={(e) => update({ priceCents: parseInt(e.target.value) || 0 })} />
              <p className="text-[10px] text-muted-foreground">= ¥{(form.priceCents / 100).toFixed(2)}</p>
            </div>
            <div className="space-y-1.5">
              <Label>周期</Label>
              <Select value={form.period} onValueChange={(v) => update({ period: v })}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="monthly">月付</SelectItem>
                  <SelectItem value="yearly">年付</SelectItem>
                  <SelectItem value="lifetime">终身</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label>排序</Label>
              <Input type="number" value={form.sortOrder} onChange={(e) => update({ sortOrder: parseInt(e.target.value) || 0 })} />
            </div>
          </div>
          <div className="grid gap-3 sm:grid-cols-3">
            <div className="space-y-1.5">
              <Label>QPS 限制</Label>
              <Input type="number" value={form.qps} onChange={(e) => update({ qps: parseInt(e.target.value) || 0 })} />
            </div>
            <div className="space-y-1.5">
              <Label>日限额</Label>
              <Input type="number" value={form.dailyLimit} onChange={(e) => update({ dailyLimit: parseInt(e.target.value) || 0 })} />
            </div>
            <div className="space-y-1.5">
              <Label>月限额</Label>
              <Input type="number" value={form.monthlyLimit} onChange={(e) => update({ monthlyLimit: parseInt(e.target.value) || 0 })} />
            </div>
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label>软限比例（%）</Label>
              <Input type="number" value={form.softLimitPct} onChange={(e) => update({ softLimitPct: parseInt(e.target.value) || 0 })} />
            </div>
            <div className="space-y-1.5">
              <Label>超额千次价格（分）</Label>
              <Input type="number" value={form.overagePricePer_1k} onChange={(e) => update({ overagePricePer_1k: parseInt(e.target.value) || 0 })} />
            </div>
          </div>
          <div className="flex items-center gap-3">
            <Switch checked={form.payAsYouGo} onCheckedChange={(v) => update({ payAsYouGo: v })} />
            <Label>启用按量计费（超额后按次收费）</Label>
          </div>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>取消</Button>
            <Button type="submit" disabled={saving}>
              {saving && <Loader2 className="mr-2 size-4 animate-spin" />}
              {saving ? "保存中..." : "保存"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
