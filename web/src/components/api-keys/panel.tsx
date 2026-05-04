"use client";

import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Check,
  ChevronDown,
  Copy,
  Globe,
  Key,
  Lock,
  MoreHorizontal,
  Pause,
  Pencil,
  Play,
  Plus,
  Shield,
  Trash2,
  Zap,
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { TokenInput } from "@/components/ui/token-input";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { apiKeysApi } from "@/lib/api/auth";
import { ApiError } from "@/lib/api/client";
import { formatDate, formatDateTime, formatRelative } from "@/lib/datetime";
import type { APIKey, APIKeyStatus } from "@/types/api";

// 已知的 scope 预设；后端 ScopeMiddleware 会在 API Key 调用时强制校验。
// 留空 = 不限；填了 = 仅授予所列权限范围内的接口。
//
// 通配语法：
//   "*"          全局通配，任何 scope 都通过
//   "module:*"   模块通配（如 "music:*" 涵盖 music namespace 下所有 action）
//   "module:action" 精确匹配
//
// 注意：号池（pool 模块）相关 scope 是平台内部资源，不在用户可选列表中。
// admin:* 仅对底层用户角色为 admin / superadmin 的账号有效；普通用户即使
// 给 key 配上 admin scope 也会被 AdminAuthMiddleware 在路径上拦下。
const KNOWN_SCOPES = [
  "*",
  "music:*",
  "music:read",
  "music:write",
  "billing:*",
  "billing:read",
  "billing:write",
  "quota:read",
  "admin:*",
  "admin:read",
  "admin:write",
] as const;

// scope 含义说明，用作下方解释列表 / tooltip。
const SCOPE_DESCRIPTIONS: Record<string, string> = {
  "*": "全部权限（任何模块的任何操作；包括管理面）",
  "music:*": "音乐模块所有操作（读 + 写）",
  "music:read": "音乐读取（搜索、详情、歌词、URL 等所有 GET）",
  "music:write": "音乐写入（预留扩展）",
  "billing:*": "账单模块所有操作（查看 + 创建）",
  "billing:read": "查看订单、余额、发票、订阅套餐",
  "billing:write": "创建订单、订阅、充值、超额加购",
  "quota:read": "查看用量与历史",
  "admin:*": "管理面所有操作（仅当账号本身是 admin/superadmin 时生效）",
  "admin:read": "管理面只读（用户列表 / 套餐 / 设置查看；需账号 admin 角色）",
  "admin:write":
    "管理面写入（号池增删 / 用户管理 / 设置变更；高敏感操作需 superadmin）",
};

// 限速预设档；选"默认"时不下发 rpm/daily（=0=不限），由套餐 quota 中间件统一管控。
const RATE_PRESETS = [
  { id: "default", label: "默认", desc: "由账户套餐控制", rpm: 0, daily: 0 },
  { id: "strict", label: "严格", desc: "60/分钟 · 1万/天", rpm: 60, daily: 10_000 },
  { id: "standard", label: "标准", desc: "600/分钟 · 10万/天", rpm: 600, daily: 100_000 },
  { id: "custom", label: "自定义", desc: "手动填写", rpm: -1, daily: -1 },
] as const;
type RatePresetId = (typeof RATE_PRESETS)[number]["id"];

// 过期时间预设；返回相对天数。null = 永不过期。
const EXPIRES_PRESETS = [
  { id: "never", label: "永不", days: null as number | null },
  { id: "7d", label: "7 天", days: 7 },
  { id: "30d", label: "30 天", days: 30 },
  { id: "90d", label: "90 天", days: 90 },
  { id: "1y", label: "1 年", days: 365 },
  { id: "custom", label: "自定义", days: -1 },
] as const;
type ExpiresPresetId = (typeof EXPIRES_PRESETS)[number]["id"];

// scope 接受三种形式：单星号 / 模块通配 / 精确 module:action（小写）
const SCOPE_PATTERN = /^(\*|[a-z]+:(\*|[a-z]+))$/;

// describeCustomScope 给用户手输的 scope（不在 SCOPE_DESCRIPTIONS 字典里）一个合理的中文解释。
function describeCustomScope(s: string): string {
  if (s === "*") return "全部权限";
  if (s.endsWith(":*")) return `${s.slice(0, -2)} 模块的所有操作`;
  const idx = s.indexOf(":");
  if (idx > 0) {
    return `仅 ${s.slice(0, idx)} 模块的 ${s.slice(idx + 1)} 操作`;
  }
  return "（未知 scope）";
}
// IP 或 CIDR：IPv4 / IPv6（粗校验，精细校验后端 net.ParseCIDR / net.ParseIP 兜底）
const IPV4_OR_CIDR =
  /^((\d{1,3}\.){3}\d{1,3}(\/\d{1,2})?|[0-9a-fA-F:]+(\/\d{1,3})?)$/;

const STATUS_MAP: Record<APIKeyStatus, { label: string; variant: "default" | "secondary" | "destructive" | "outline" }> = {
  active: { label: "活跃", variant: "default" },
  disabled: { label: "已禁用", variant: "secondary" },
  expired: { label: "已过期", variant: "outline" },
  revoked: { label: "已吊销", variant: "destructive" },
};

function resolveStatus(k: APIKey): APIKeyStatus {
  if (k.status) return k.status;
  if (k.enabled === false) return "disabled";
  if (k.expiresAt && new Date(k.expiresAt) < new Date()) return "expired";
  return "active";
}

function toNum(v: unknown): number {
  if (typeof v === "number") return v;
  if (typeof v === "string") return parseInt(v, 10) || 0;
  return 0;
}

function formatNumber(n: unknown): string {
  const num = toNum(n);
  if (num >= 1_000_000) return `${(num / 1_000_000).toFixed(1)}M`;
  if (num >= 10_000) return `${(num / 1_000).toFixed(1)}K`;
  return num.toLocaleString("zh-CN");
}

function formatRateLimit(rpm?: number | string, daily?: number | string): string {
  const parts: string[] = [];
  const r = toNum(rpm), d = toNum(daily);
  if (r > 0) parts.push(`${r}/分钟`);
  if (d > 0) parts.push(`${formatNumber(d)}/天`);
  return parts.length > 0 ? parts.join(" · ") : "不限速";
}

function relativeTime(dateStr?: string): string {
  return formatRelative(dateStr, { fallback: "—" });
}

export function ApiKeysPanel() {
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: ["api-keys"],
    queryFn: () => apiKeysApi.list(),
  });
  const items = list.data?.items ?? [];

  const [openCreate, setOpenCreate] = React.useState(false);
  // 创建后把 plaintext 暂存到这里；同一对话框切换到 reveal 视图，避免双弹窗
  const [created, setCreated] = React.useState<APIKey | null>(null);

  const create = useMutation({
    mutationFn: apiKeysApi.create,
    onSuccess: (data) => {
      setCreated(data);
      qc.invalidateQueries({ queryKey: ["api-keys"] });
    },
    onError: (err) => {
      toast.error(err instanceof ApiError ? err.message : "创建失败");
    },
  });

  // 打开 / 关闭对话框时清掉残留的 created（防下次开启又出现旧密钥）
  React.useEffect(() => {
    if (!openCreate) {
      const t = setTimeout(() => setCreated(null), 200);
      return () => clearTimeout(t);
    }
  }, [openCreate]);

  const toggleEnabled = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      apiKeysApi.setEnabled(id, enabled),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["api-keys"] });
    },
    onError: (err) =>
      toast.error(err instanceof ApiError ? err.message : "操作失败"),
  });

  const revoke = useMutation({
    mutationFn: (id: string) => apiKeysApi.revoke(id),
    onSuccess: () => {
      toast.success("已吊销");
      qc.invalidateQueries({ queryKey: ["api-keys"] });
    },
    onError: (err) =>
      toast.error(err instanceof ApiError ? err.message : "吊销失败"),
  });

  const activeKeys = items.filter((k) => {
    const s = resolveStatus(k);
    return s === "active" || s === "disabled";
  });
  const inactiveKeys = items.filter((k) => {
    const s = resolveStatus(k);
    return s === "expired" || s === "revoked";
  });

  return (
    <TooltipProvider>
      <div className="space-y-6">
        {/* 头部统计 + 创建按钮 */}
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-6">
            <div className="flex items-center gap-2">
              <Key className="size-5 text-muted-foreground" />
              <span className="text-sm text-muted-foreground">
                {activeKeys.length} 个活跃密钥
              </span>
            </div>
            {items.some((k) => toNum(k.totalRequests) > 0) && (
              <div className="flex items-center gap-2">
                <Zap className="size-4 text-muted-foreground" />
                <span className="text-sm text-muted-foreground">
                  总计 {formatNumber(items.reduce((s, k) => s + toNum(k.totalRequests), 0))} 次调用
                </span>
              </div>
            )}
          </div>
          <Dialog open={openCreate} onOpenChange={setOpenCreate}>
            <DialogTrigger asChild>
              <Button>
                <Plus className="mr-1 size-4" />
                新建密钥
              </Button>
            </DialogTrigger>
            <DialogContent className="max-w-lg">
              {created ? (
                <RevealView
                  apiKey={created}
                  onDone={() => setOpenCreate(false)}
                />
              ) : (
                <CreateForm
                  busy={create.isPending}
                  onSubmit={(input) => create.mutateAsync(input)}
                />
              )}
            </DialogContent>
          </Dialog>
        </div>

        {/* 活跃密钥列表 */}
        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>名称</TableHead>
                <TableHead>密钥</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>限速</TableHead>
                <TableHead>调用量</TableHead>
                <TableHead>最近使用</TableHead>
                <TableHead className="w-[60px]" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.isLoading ? (
                <TableRow>
                  <TableCell colSpan={7} className="text-center text-muted-foreground py-8">
                    加载中...
                  </TableCell>
                </TableRow>
              ) : activeKeys.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={7} className="text-center text-muted-foreground py-8">
                    <div className="flex flex-col items-center gap-2">
                      <Shield className="size-8 text-muted-foreground/50" />
                      <p>尚未创建 API 密钥</p>
                      <p className="text-xs">点击「新建密钥」开始使用 API</p>
                    </div>
                  </TableCell>
                </TableRow>
              ) : (
                activeKeys.map((k) => (
                  <TableRow key={k.id} className={resolveStatus(k) === "disabled" ? "opacity-60" : ""}>
                    <TableCell>
                      <div className="space-y-0.5">
                        <div className="flex items-center gap-1.5">
                          <span className="font-medium">{k.name}</span>
                          <KeyAclBadges scopes={k.scopes} ipAllowlist={k.ipAllowlist} />
                        </div>
                        {k.description && (
                          <p className="text-xs text-muted-foreground truncate max-w-[200px]">
                            {k.description}
                          </p>
                        )}
                      </div>
                    </TableCell>
                    <TableCell>
                      <code className="rounded bg-muted px-1.5 py-0.5 text-xs font-mono">
                        {k.prefix}••••••
                      </code>
                    </TableCell>
                    <TableCell>
                      <StatusBadge status={resolveStatus(k)} />
                    </TableCell>
                    <TableCell>
                      <span className="text-sm text-muted-foreground">
                        {formatRateLimit(k.rateLimitRpm, k.rateLimitDaily)}
                      </span>
                    </TableCell>
                    <TableCell>
                      <span className="text-sm font-mono">
                        {formatNumber(k.totalRequests)}
                      </span>
                    </TableCell>
                    <TableCell>
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <span className="text-sm text-muted-foreground cursor-default">
                            {relativeTime(k.lastUsedAt)}
                          </span>
                        </TooltipTrigger>
                        {k.lastUsedAt && (
                          <TooltipContent>
                            {formatDateTime(k.lastUsedAt)}
                          </TooltipContent>
                        )}
                      </Tooltip>
                    </TableCell>
                    <TableCell>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button variant="ghost" size="icon" className="size-8">
                            <MoreHorizontal className="size-4" />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem
                            onClick={() => {
                              toggleEnabled.mutate({
                                id: k.id,
                                enabled: resolveStatus(k) === "disabled",
                              });
                            }}
                          >
                            {resolveStatus(k) === "disabled" ? (
                              <>
                                <Play className="mr-2 size-4" />
                                启用
                              </>
                            ) : (
                              <>
                                <Pause className="mr-2 size-4" />
                                禁用
                              </>
                            )}
                          </DropdownMenuItem>
                          <DropdownMenuItem disabled>
                            <Pencil className="mr-2 size-4" />
                            编辑
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            className="text-destructive focus:text-destructive"
                            onClick={() => {
                              if (window.confirm(`确定永久吊销「${k.name}」？此操作不可撤销。`)) {
                                revoke.mutate(k.id);
                              }
                            }}
                          >
                            <Trash2 className="mr-2 size-4" />
                            吊销
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>

        {/* 已失效密钥 */}
        {inactiveKeys.length > 0 && (
          <details className="group">
            <summary className="cursor-pointer text-sm text-muted-foreground hover:text-foreground transition-colors">
              已失效的密钥（{inactiveKeys.length}）
            </summary>
            <div className="mt-2 rounded-lg border opacity-60">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>名称</TableHead>
                    <TableHead>密钥</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>创建时间</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {inactiveKeys.map((k) => (
                    <TableRow key={k.id}>
                      <TableCell className="font-medium">{k.name}</TableCell>
                      <TableCell>
                        <code className="text-xs font-mono">{k.prefix}••••••</code>
                      </TableCell>
                      <TableCell>
                        <StatusBadge status={resolveStatus(k)} />
                      </TableCell>
                      <TableCell className="text-sm text-muted-foreground">
                        {formatDate(k.createdAt, { fallback: "—" })}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </details>
        )}
      </div>
    </TooltipProvider>
  );
}

function StatusBadge({ status }: { status: APIKeyStatus }) {
  const info = STATUS_MAP[status] ?? STATUS_MAP.active;
  return <Badge variant={info.variant}>{info.label}</Badge>;
}

/**
 * 在密钥名称旁显示 ACL 状态徽章：scope 限定 + IP 限定。
 * 留空字段不渲染对应徽章；hover 显示具体内容。
 */
function KeyAclBadges({
  scopes,
  ipAllowlist,
}: {
  scopes?: string[];
  ipAllowlist?: string[];
}) {
  return (
    <>
      {scopes && scopes.length > 0 && (
        <Tooltip>
          <TooltipTrigger asChild>
            <span className="inline-flex items-center gap-0.5 rounded-sm bg-blue-500/10 px-1 py-0.5 text-[10px] text-blue-700 dark:text-blue-400 cursor-help">
              <Lock className="size-2.5" />
              {scopes.length}
            </span>
          </TooltipTrigger>
          <TooltipContent>
            <div className="space-y-0.5 text-xs">
              <p className="font-medium">仅限以下权限：</p>
              {scopes.map((s) => (
                <code key={s} className="block font-mono">
                  {s}
                </code>
              ))}
            </div>
          </TooltipContent>
        </Tooltip>
      )}
      {ipAllowlist && ipAllowlist.length > 0 && (
        <Tooltip>
          <TooltipTrigger asChild>
            <span className="inline-flex items-center gap-0.5 rounded-sm bg-emerald-500/10 px-1 py-0.5 text-[10px] text-emerald-700 dark:text-emerald-400 cursor-help">
              <Globe className="size-2.5" />
              {ipAllowlist.length}
            </span>
          </TooltipTrigger>
          <TooltipContent>
            <div className="space-y-0.5 text-xs">
              <p className="font-medium">仅限以下来源 IP：</p>
              {ipAllowlist.map((ip) => (
                <code key={ip} className="block font-mono">
                  {ip}
                </code>
              ))}
            </div>
          </TooltipContent>
        </Tooltip>
      )}
    </>
  );
}

interface CreateFormProps {
  busy: boolean;
  onSubmit: (input: {
    name: string;
    description?: string;
    scopes?: string[];
    ipAllowlist?: string[];
    rateLimitRpm?: number;
    rateLimitDaily?: number;
    expiresAt?: string;
  }) => Promise<unknown>;
}

/**
 * 简化版"创建 API 密钥"表单。
 *
 * 默认仅暴露：名称 + [更多选项]。点创建即默认值（永久 / 由套餐限速 / 不绑 IP）。
 *
 * 高级选项展开后：
 *   - 描述
 *   - 过期时间预设按钮组（永不 / 7天 / 30天 / 90天 / 1年 / 自定义日期）
 *   - 限速预设按钮组（默认 / 严格 / 标准 / 自定义）
 *   - scope token 输入（含已知列表 autocomplete）
 *   - IP 白名单 token 输入
 */
function CreateForm({ busy, onSubmit }: CreateFormProps) {
  const [name, setName] = React.useState("");
  const [nameError, setNameError] = React.useState<string | null>(null);
  const [advanced, setAdvanced] = React.useState(false);

  const [description, setDescription] = React.useState("");
  const [scopes, setScopes] = React.useState<string[]>([]);
  const [ipAllow, setIpAllow] = React.useState<string[]>([]);

  const [expPreset, setExpPreset] = React.useState<ExpiresPresetId>("never");
  const [expCustom, setExpCustom] = React.useState(""); // datetime-local

  const [ratePreset, setRatePreset] = React.useState<RatePresetId>("default");
  const [rpmInput, setRpmInput] = React.useState("");
  const [dailyInput, setDailyInput] = React.useState("");

  const expiresISO = React.useMemo<string | undefined>(() => {
    const preset = EXPIRES_PRESETS.find((p) => p.id === expPreset);
    if (!preset) return undefined;
    if (preset.id === "never") return undefined;
    if (preset.id === "custom") {
      if (!expCustom) return undefined;
      const d = new Date(expCustom);
      return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
    }
    if (preset.days != null) {
      const d = new Date(Date.now() + preset.days * 86_400_000);
      return d.toISOString();
    }
    return undefined;
  }, [expPreset, expCustom]);

  const { rpm, daily, rateError } = React.useMemo(() => {
    const preset = RATE_PRESETS.find((p) => p.id === ratePreset)!;
    if (preset.id === "custom") {
      const r = rpmInput === "" ? 0 : Number(rpmInput);
      const d = dailyInput === "" ? 0 : Number(dailyInput);
      if (
        Number.isNaN(r) ||
        Number.isNaN(d) ||
        r < 0 ||
        d < 0 ||
        r > 100_000 ||
        d > 10_000_000
      ) {
        return { rpm: 0, daily: 0, rateError: "限速数值不合法" };
      }
      return { rpm: r, daily: d, rateError: null as string | null };
    }
    return { rpm: preset.rpm, daily: preset.daily, rateError: null as string | null };
  }, [ratePreset, rpmInput, dailyInput]);

  const handleCreate = async () => {
    const trimmed = name.trim();
    if (!trimmed) {
      setNameError("请输入密钥名称");
      return;
    }
    if (trimmed.length > 64) {
      setNameError("名称最多 64 字符");
      return;
    }
    setNameError(null);
    if (rateError) return;
    try {
      await onSubmit({
        name: trimmed,
        description: description.trim() || undefined,
        scopes: scopes.length > 0 ? scopes : undefined,
        ipAllowlist: ipAllow.length > 0 ? ipAllow : undefined,
        rateLimitRpm: rpm > 0 ? rpm : undefined,
        rateLimitDaily: daily > 0 ? daily : undefined,
        expiresAt: expiresISO,
      });
    } catch {
      // 错误由上层 mutation onError toast；这里不重复
    }
  };

  return (
    <>
      <DialogHeader>
        <DialogTitle>创建 API 密钥</DialogTitle>
        <DialogDescription>
          填一个名字即可创建。默认永不过期、由账户套餐控制限速。
        </DialogDescription>
      </DialogHeader>

      <div className="space-y-4">
        <div className="space-y-1.5">
          <Label htmlFor="key-name">密钥名称</Label>
          <Input
            id="key-name"
            placeholder="例如：生产 / 测试脚本"
            maxLength={64}
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              if (nameError) setNameError(null);
            }}
            autoFocus
          />
          {nameError && <p className="text-xs text-destructive">{nameError}</p>}
        </div>

        <button
          type="button"
          onClick={() => setAdvanced((v) => !v)}
          className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ChevronDown
            className={cn(
              "size-3 transition-transform",
              advanced && "rotate-180",
            )}
          />
          {advanced ? "收起更多选项" : "更多选项"}
        </button>

        {advanced && (
          <div className="space-y-4 pt-1">
            <div className="space-y-1.5">
              <Label htmlFor="key-desc">描述（可选）</Label>
              <Input
                id="key-desc"
                placeholder="用途说明，方便日后辨认"
                maxLength={256}
                value={description}
                onChange={(e) => setDescription(e.target.value)}
              />
            </div>

            <div className="space-y-1.5">
              <Label className="flex items-center gap-1.5">
                <Zap className="size-3.5 text-muted-foreground" />
                限速
              </Label>
              <div className="grid grid-cols-2 gap-1.5 sm:grid-cols-4">
                {RATE_PRESETS.map((p) => (
                  <PresetButton
                    key={p.id}
                    active={ratePreset === p.id}
                    onClick={() => setRatePreset(p.id)}
                    label={p.label}
                    desc={p.desc}
                  />
                ))}
              </div>
              {ratePreset === "custom" && (
                <div className="grid grid-cols-2 gap-2 pt-2">
                  <div className="space-y-1">
                    <Label htmlFor="rpm" className="text-xs">每分钟</Label>
                    <Input
                      id="rpm"
                      type="number"
                      placeholder="0"
                      min={0}
                      value={rpmInput}
                      onChange={(e) => setRpmInput(e.target.value)}
                    />
                  </div>
                  <div className="space-y-1">
                    <Label htmlFor="daily" className="text-xs">每天</Label>
                    <Input
                      id="daily"
                      type="number"
                      placeholder="0"
                      min={0}
                      value={dailyInput}
                      onChange={(e) => setDailyInput(e.target.value)}
                    />
                  </div>
                </div>
              )}
              {rateError && (
                <p className="text-xs text-destructive">{rateError}</p>
              )}
            </div>

            <div className="space-y-1.5">
              <Label>过期时间</Label>
              <div className="flex flex-wrap gap-1.5">
                {EXPIRES_PRESETS.map((p) => (
                  <PresetButton
                    key={p.id}
                    active={expPreset === p.id}
                    onClick={() => setExpPreset(p.id)}
                    label={p.label}
                  />
                ))}
              </div>
              {expPreset === "custom" && (
                <Input
                  type="datetime-local"
                  value={expCustom}
                  onChange={(e) => setExpCustom(e.target.value)}
                  className="mt-2"
                />
              )}
            </div>

            <div className="space-y-1.5">
              <Label>权限范围（Scopes）</Label>
              <TokenInput
                value={scopes}
                onChange={setScopes}
                suggestions={KNOWN_SCOPES}
                placeholder='留空 = 全部权限；支持 "*" / "music:*" / "music:read"'
                validate={(t) =>
                  SCOPE_PATTERN.test(t)
                    ? undefined
                    : "格式：* | module:* | module:action（小写）"
                }
                max={20}
              />
              {scopes.length > 0 && (
                <ul className="text-xs text-muted-foreground space-y-0.5 pt-1">
                  {scopes.map((s) => (
                    <li key={s} className="flex gap-1.5">
                      <code className="font-mono text-foreground/80">{s}</code>
                      <span>—</span>
                      <span>
                        {SCOPE_DESCRIPTIONS[s] ?? describeCustomScope(s)}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
              <p className="text-[11px] text-muted-foreground">
                此密钥使用时，后端会校验请求路径是否在已授权 scope 范围内；不在则返回 403。
              </p>
            </div>

            <div className="space-y-1.5">
              <Label>IP 白名单（CIDR）</Label>
              <TokenInput
                value={ipAllow}
                onChange={setIpAllow}
                placeholder="留空 = 不限来源；填了仅这些 IP/CIDR 可调用"
                validate={(t) =>
                  IPV4_OR_CIDR.test(t) ? undefined : "需要 IPv4 或 CIDR"
                }
                max={20}
              />
              <p className="text-[11px] text-muted-foreground">
                来源 IP 不在白名单时返回 403。建议生产环境设置出口 IP，杜绝密钥泄漏后被外部滥用。
              </p>
            </div>
          </div>
        )}
      </div>

      <DialogFooter>
        <Button onClick={handleCreate} disabled={busy} className="w-full sm:w-auto">
          {busy ? "创建中..." : "创建密钥"}
        </Button>
      </DialogFooter>
    </>
  );
}

function PresetButton({
  active,
  onClick,
  label,
  desc,
}: {
  active: boolean;
  onClick: () => void;
  label: string;
  desc?: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex flex-col items-start gap-0.5 rounded-md border px-2.5 py-1.5 text-left text-xs transition-colors",
        active
          ? "border-primary bg-primary/5 text-foreground"
          : "border-input bg-background text-muted-foreground hover:text-foreground hover:border-foreground/30",
      )}
    >
      <span className="font-medium">{label}</span>
      {desc && <span className="text-[10px] opacity-70">{desc}</span>}
    </button>
  );
}

function RevealView({
  apiKey,
  onDone,
}: {
  apiKey: APIKey;
  onDone: () => void;
}) {
  const [copied, setCopied] = React.useState(false);

  const onCopy = async () => {
    if (!apiKey.plaintext) return;
    try {
      await navigator.clipboard.writeText(apiKey.plaintext);
      setCopied(true);
      toast.success("已复制到剪贴板");
      setTimeout(() => setCopied(false), 2000);
    } catch {
      toast.error("复制失败，请手动选中");
    }
  };

  return (
    <>
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2">
          <Check className="size-5 text-emerald-600" />
          密钥已创建
        </DialogTitle>
        <DialogDescription>
          这是「{apiKey.name}」的明文，仅显示一次。关闭后无法再次查看。
        </DialogDescription>
      </DialogHeader>
      <div className="space-y-3">
        <div className="space-y-1.5">
          <Label className="text-xs text-muted-foreground">API 密钥</Label>
          <div className="flex items-center gap-2">
            <Input
              readOnly
              value={apiKey.plaintext ?? ""}
              className="font-mono text-xs bg-muted"
              onFocus={(e) => e.currentTarget.select()}
            />
            <Button
              size="icon"
              variant={copied ? "default" : "outline"}
              onClick={onCopy}
              aria-label="复制"
            >
              {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
            </Button>
          </div>
        </div>
        <div className="rounded-md bg-amber-50 dark:bg-amber-950/20 border border-amber-200 dark:border-amber-800 p-3 text-xs text-amber-800 dark:text-amber-200">
          <p className="font-medium">安全提示</p>
          <ul className="mt-1 list-disc list-inside space-y-0.5">
            <li>切勿提交到代码仓库；推荐用环境变量保存</li>
            <li>定期轮换密钥；可在列表页吊销旧密钥</li>
          </ul>
        </div>
      </div>
      <DialogFooter>
        <Button onClick={onDone} className="w-full sm:w-auto">
          我已保存
        </Button>
      </DialogFooter>
    </>
  );
}
