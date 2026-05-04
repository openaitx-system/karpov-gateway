"use client";

import * as React from "react";
import { RefreshCcw, Search } from "lucide-react";
import { toast } from "sonner";

import { AddCredentialDialog } from "@/components/pool/add-credential-dialog";
import { CredentialsTable } from "@/components/pool/credentials-table";
import { HealthSummaryCard } from "@/components/pool/health-summary-card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { ApiError } from "@/lib/api/client";
import { poolApi } from "@/lib/api/pool";
import { formatDateTime } from "@/lib/datetime";
import { PROVIDER_CAPABILITIES } from "@/lib/pool-capabilities";
import type {
  PoolCredential,
  PoolHealth,
  ProviderPool,
} from "@/types/api";

interface PoolAdminClientProps {
  initialProvider?: string;
}

const PAGE_SIZE_OPTIONS = [10, 20, 50, 100] as const;
const KNOWN_PROVIDERS = Object.keys(PROVIDER_CAPABILITIES);

const STATUS_OPTIONS: ReadonlyArray<{ value: string; label: string }> = [
  { value: "all", label: "全部" },
  { value: "active", label: "仅可用" },
  { value: "disabled", label: "仅禁用" },
  { value: "banned", label: "仅封禁" },
];

/**
 * 号池管理控制台：筛选 + 健康概览 + 凭证表格。
 *
 * 数据流：provider/状态/翻页/每页变化 → 并发 fetch list+health。
 * 客户端搜索：按标签 / id / 能力前缀过滤当前页。
 * R 键刷新：输入框聚焦时不拦截。
 */
export function PoolAdminClient({
  initialProvider = "__all__",
}: PoolAdminClientProps) {
  const [provider, setProvider] = React.useState(initialProvider);
  const [statusFilter, setStatusFilter] = React.useState("all");
  const [page, setPage] = React.useState(0);
  const [pageSize, setPageSize] = React.useState(20);
  const [search, setSearch] = React.useState("");

  const [items, setItems] = React.useState<PoolCredential[]>([]);
  const [total, setTotal] = React.useState(0);
  const [health, setHealth] = React.useState<ProviderPool | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [lastRefresh, setLastRefresh] = React.useState<Date | null>(null);

  const refresh = React.useCallback(async () => {
    setLoading(true);
    try {
      const apiStatus = statusFilter === "all" ? undefined : statusFilter;
      const actualProvider = provider === "__all__" ? undefined : provider;
      const listPromise = poolApi.list({
        provider: actualProvider,
        statusFilter: apiStatus,
        limit: pageSize,
        offset: page * pageSize,
      });
      const healthPromise = actualProvider
        ? poolApi.health(actualProvider).catch((err) => {
            if (err instanceof ApiError && err.status >= 400 && err.status < 500) return null;
            throw err;
          })
        : Promise.resolve(null);
      const [list, h] = await Promise.all([listPromise, healthPromise]);
      setItems(list.items ?? []);
      setTotal(list.total ?? 0);
      const summary = (h as PoolHealth | null)?.pools?.[0] ?? null;
      setHealth(summary);
      setLastRefresh(new Date());
    } catch (err) {
      toast.error(
        err instanceof ApiError ? `加载失败：${err.message}` : "网络异常",
      );
    } finally {
      setLoading(false);
    }
  }, [provider, statusFilter, page, pageSize]);

  React.useEffect(() => {
    void refresh();
  }, [refresh]);

  // R 快捷键刷新（输入框 / 下拉聚焦时不拦截）
  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!e.key || e.key.toLowerCase() !== "r") return;
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      const el = e.target as HTMLElement | null;
      const tag = el?.tagName ?? "";
      if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
      if (el?.getAttribute("role") === "combobox") return;
      e.preventDefault();
      void refresh();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [refresh]);

  const filtered = React.useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return items;
    return items.filter(
      (c) =>
        (c.label ?? "").toLowerCase().includes(q) ||
        (c.id ?? "").toLowerCase().includes(q) ||
        (c.capabilities ?? []).some((cap) => (cap ?? "").toLowerCase().includes(q)),
    );
  }, [items, search]);

  const providerOptions = React.useMemo(() => {
    const set = new Set(["__all__", ...KNOWN_PROVIDERS]);
    if (provider && provider !== "__all__" && !set.has(provider)) set.add(provider);
    return Array.from(set);
  }, [provider]);

  const updatedLabel = lastRefresh
    ? `更新于 ${lastRefresh.toLocaleTimeString("zh-CN", { hour12: false })}`
    : "未加载";

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <div className="flex flex-1 flex-wrap items-end gap-3">
          <FilterField label="平台" htmlFor="filter-provider">
            <Select
              value={provider}
              onValueChange={(v) => {
                setPage(0);
                setProvider(v);
              }}
            >
              <SelectTrigger id="filter-provider" className="w-44">
                <SelectValue placeholder="全部平台" />
              </SelectTrigger>
              <SelectContent>
                {providerOptions.map((p) => (
                  <SelectItem key={p} value={p}>
                    {p === "__all__" ? "全部平台" : p === "qqmusic" ? "QQ 音乐" : p === "netease" ? "网易云音乐" : p}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </FilterField>

          <FilterField label="状态" htmlFor="filter-status">
            <Select
              value={statusFilter}
              onValueChange={(v) => {
                setPage(0);
                setStatusFilter(v);
              }}
            >
              <SelectTrigger id="filter-status" className="w-32">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {STATUS_OPTIONS.map((opt) => (
                  <SelectItem key={opt.value} value={opt.value}>
                    {opt.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </FilterField>

          <FilterField label="每页" htmlFor="filter-page-size">
            <Select
              value={String(pageSize)}
              onValueChange={(v) => {
                setPage(0);
                setPageSize(Number(v));
              }}
            >
              <SelectTrigger id="filter-page-size" className="w-28">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {PAGE_SIZE_OPTIONS.map((n) => (
                  <SelectItem key={n} value={String(n)}>
                    {n} 条
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </FilterField>

          <FilterField
            label="搜索"
            htmlFor="filter-search"
            className="min-w-[200px] flex-1 sm:max-w-sm"
          >
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                id="filter-search"
                value={search}
                placeholder="标签 / ID / 能力"
                onChange={(e) => setSearch(e.target.value)}
                className="pl-8"
                autoComplete="off"
              />
            </div>
          </FilterField>
        </div>

        <div className="flex items-center gap-3 self-end">
          <span
            className="text-xs tabular-nums text-muted-foreground"
            title={formatDateTime(lastRefresh, { fallback: "" })}
          >
            {updatedLabel}
          </span>
          <Button
            variant="outline"
            onClick={() => void refresh()}
            disabled={loading}
            title="刷新（按 R）"
          >
            <RefreshCcw className={loading ? "animate-spin" : ""} />
            刷新
          </Button>
          <AddCredentialDialog
            defaultProvider={provider}
            onAdded={() => void refresh()}
          />
        </div>
      </div>

      <HealthSummaryCard
        provider={provider}
        data={health}
        loading={loading && health === null}
      />

      <CredentialsTable
        items={filtered}
        loading={loading}
        total={search ? filtered.length : total}
        page={page}
        pageSize={pageSize}
        onPageChange={(next) => setPage(Math.max(0, next))}
        onChanged={() => void refresh()}
      />
    </div>
  );
}

function FilterField({
  label,
  htmlFor,
  className,
  children,
}: {
  label: string;
  htmlFor: string;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div className={["space-y-1.5", className].filter(Boolean).join(" ")}>
      <Label htmlFor={htmlFor} className="text-xs text-muted-foreground">
        {label}
      </Label>
      {children}
    </div>
  );
}
