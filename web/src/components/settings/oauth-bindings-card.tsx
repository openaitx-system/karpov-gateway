"use client";

import { useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

interface IdentityItem {
  provider: string;
  providerSub: string;
  providerLogin: string;
  providerEmail: string;
  providerName: string;
  providerAvatar: string;
  trustLevel: number;
  createdAt: string;
  lastLoginAt: string | null;
}

interface ProviderInfo {
  name: string;
  displayName: string;
}

interface ApiResp<T> {
  code?: number;
  message?: string;
  data?: T;
}

const PROVIDER_DISPLAY_FALLBACK: Record<string, string> = {
  linuxdo: "Linux.do",
};

export function OAuthBindingsCard() {
  const search = useSearchParams();
  const [identities, setIdentities] = useState<IdentityItem[] | null>(null);
  const [providers, setProviders] = useState<ProviderInfo[]>([]);
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  // 读取 bind=ok / oauth_error= callback hint
  useEffect(() => {
    if (search.get("bind") === "ok") toast.success("第三方账号已绑定");
  }, [search]);

  // 拉 identities + providers 并行
  useEffect(() => {
    let cancelled = false;
    setErr(null);
    Promise.all([
      fetch("/v1/auth/oauth/identities", { credentials: "same-origin" }).then((r) => r.json()),
      fetch("/v1/auth/oauth/providers", { credentials: "same-origin" }).then((r) => r.json()),
    ])
      .then(([idResp, provResp]: [ApiResp<{ identities?: IdentityItem[] }>, ApiResp<{ providers?: ProviderInfo[] }>]) => {
        if (cancelled) return;
        setIdentities(idResp?.data?.identities ?? []);
        setProviders(provResp?.data?.providers ?? []);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        setErr(e instanceof Error ? e.message : "加载失败");
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // 刚解绑 / 刚拉数据时, providers 不会变, identities 会
  const refresh = async () => {
    const r = await fetch("/v1/auth/oauth/identities", { credentials: "same-origin" });
    const j = (await r.json()) as ApiResp<{ identities?: IdentityItem[] }>;
    setIdentities(j?.data?.identities ?? []);
  };

  const unbind = async (provider: string) => {
    if (!confirm(`确定解绑 ${displayName(provider, providers)} 吗?`)) return;
    setBusy(provider);
    try {
      const csrfToken = readCookie("csrf_token") || "";
      const res = await fetch(`/v1/auth/oauth/identities/${encodeURIComponent(provider)}`, {
        method: "DELETE",
        credentials: "same-origin",
        headers: { "X-CSRF-Token": csrfToken },
      });
      const body = (await res.json().catch(() => ({}))) as ApiResp<unknown>;
      if (!res.ok) {
        throw new Error(body?.message || `解绑失败 (HTTP ${res.status})`);
      }
      toast.success("已解绑");
      await refresh();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "解绑失败");
    } finally {
      setBusy(null);
    }
  };

  // 待绑定 = providers 中尚未在 identities 出现的
  const boundSet = new Set((identities ?? []).map((i) => i.provider));
  const unboundProviders = providers.filter((p) => !boundSet.has(p.name));

  if (providers.length === 0 && (identities?.length ?? 0) === 0) {
    return null; // OAuth 子系统未启用; 不显示
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">第三方账号</CardTitle>
        <CardDescription>用第三方账号登录, 或解绑已有绑定</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {err && <p className="text-xs text-destructive">{err}</p>}
        {identities === null && <p className="text-xs text-muted-foreground">加载中...</p>}
        {identities !== null && identities.length === 0 && unboundProviders.length === 0 && (
          <p className="text-xs text-muted-foreground">没有可用的第三方登录</p>
        )}

        {identities && identities.length > 0 && (
          <ul className="space-y-2">
            {identities.map((id) => (
              <li
                key={`${id.provider}:${id.providerSub}`}
                className="flex items-center justify-between rounded-md border bg-muted/40 px-3 py-2"
              >
                <div className="flex items-center gap-3 min-w-0">
                  {id.providerAvatar ? (
                    // eslint-disable-next-line @next/next/no-img-element
                    <img
                      src={id.providerAvatar}
                      alt=""
                      className="h-8 w-8 rounded-full object-cover"
                      loading="lazy"
                    />
                  ) : (
                    <div className="h-8 w-8 rounded-full bg-muted" />
                  )}
                  <div className="min-w-0">
                    <div className="text-sm font-medium truncate">
                      {displayName(id.provider, providers)}
                      {id.providerLogin && (
                        <span className="ml-2 text-xs text-muted-foreground">@{id.providerLogin}</span>
                      )}
                    </div>
                    <div className="text-xs text-muted-foreground truncate">
                      {id.providerEmail || id.providerName || `sub: ${id.providerSub}`}
                      {id.provider === "linuxdo" && id.trustLevel >= 0 && (
                        <span className="ml-2">· 等级 {id.trustLevel}</span>
                      )}
                    </div>
                  </div>
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  disabled={busy === id.provider}
                  onClick={() => unbind(id.provider)}
                >
                  {busy === id.provider ? "解绑中..." : "解绑"}
                </Button>
              </li>
            ))}
          </ul>
        )}

        {unboundProviders.length > 0 && (
          <div className="space-y-2 border-t pt-3">
            <p className="text-xs text-muted-foreground">尚未绑定</p>
            {unboundProviders.map((p) => (
              <Button key={p.name} type="button" variant="outline" className="w-full" asChild>
                <a href={`/v1/auth/oauth/${p.name}/start?intent=bind&next=/settings`}>
                  绑定 {p.displayName}
                </a>
              </Button>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function displayName(name: string, providers: ProviderInfo[]): string {
  const p = providers.find((x) => x.name === name);
  return p?.displayName || PROVIDER_DISPLAY_FALLBACK[name] || name;
}

function readCookie(name: string): string {
  if (typeof document === "undefined") return "";
  const match = document.cookie.match(new RegExp("(^| )" + name + "=([^;]+)"));
  return match ? decodeURIComponent(match[2]) : "";
}
