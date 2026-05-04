"use client";

import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";

interface ProviderInfo {
  name: string;
  displayName: string;
}

interface ListProvidersResp {
  code?: number;
  data?: { providers?: ProviderInfo[] };
}

interface SsoButtonsProps {
  /**
   * 当前页面的 intent: "login" 用于登录页、"bind" 用于已登录后绑定流程.
   * Bind 流程后端要求 X-User-Id header (走 session middleware), 前端只需在 query 加 intent=bind.
   */
  intent?: "login" | "bind";
  /** 成功登录/绑定后跳转地址; 后端会做 host 白名单, 不安全的会被静默忽略. */
  next?: string;
  /** 隐藏标题文字 (绑定页可能不需要 "或使用以下方式登录"). */
  hideHeader?: boolean;
}

export function SsoButtons({ intent = "login", next, hideHeader }: SsoButtonsProps) {
  const [providers, setProviders] = useState<ProviderInfo[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    fetch("/v1/auth/oauth/providers", { credentials: "same-origin" })
      .then((r) => r.json())
      .then((j: ListProvidersResp) => {
        if (cancelled) return;
        setProviders(j?.data?.providers ?? []);
      })
      .catch(() => {
        // 后端 OAuth 子系统未启用时静默隐藏 (不报错给用户).
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  if (loading || providers.length === 0) {
    return null;
  }

  return (
    <div className="space-y-3">
      {!hideHeader && (
        <div className="relative">
          <div className="absolute inset-0 flex items-center">
            <span className="w-full border-t" />
          </div>
          <div className="relative flex justify-center text-xs uppercase">
            <span className="bg-background px-2 text-muted-foreground tracking-wider">
              {intent === "bind" ? "可绑定的账号" : "或使用以下方式登录"}
            </span>
          </div>
        </div>
      )}
      <div className="space-y-2">
        {providers.map((p) => (
          <SsoButton key={p.name} provider={p} intent={intent} next={next} />
        ))}
      </div>
    </div>
  );
}

interface SsoButtonProps {
  provider: ProviderInfo;
  intent: "login" | "bind";
  next?: string;
}

function SsoButton({ provider, intent, next }: SsoButtonProps) {
  const params = new URLSearchParams();
  if (intent !== "login") params.set("intent", intent);
  if (next) params.set("next", next);
  const href = `/v1/auth/oauth/${provider.name}/start${
    params.toString() ? `?${params.toString()}` : ""
  }`;

  return (
    <Button
      type="button"
      variant="outline"
      className="w-full"
      asChild
    >
      <a href={href} aria-label={`使用 ${provider.displayName} ${intent === "bind" ? "绑定" : "登录"}`}>
        <ProviderIcon name={provider.name} />
        <span>
          {intent === "bind" ? "绑定 " : "用 "}
          {provider.displayName}
        </span>
      </a>
    </Button>
  );
}

// ProviderIcon 内联渲染各 provider 图标; 不引外部图床, 避免 CSP 问题.
function ProviderIcon({ name }: { name: string }) {
  if (name === "linuxdo") {
    // Linux.do 企鹅 (简化几何; 不做 1:1 抄, 只取识别度).
    return (
      <svg
        viewBox="0 0 24 24"
        width="16"
        height="16"
        aria-hidden
        className="shrink-0"
        fill="currentColor"
      >
        <path d="M12 2C7 2 4 6 4 11c0 3.5 2 6.5 4.5 8L7 22h10l-1.5-3c2.5-1.5 4.5-4.5 4.5-8 0-5-3-9-8-9zm-2 8a1 1 0 110-2 1 1 0 010 2zm4 0a1 1 0 110-2 1 1 0 010 2z" />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden className="shrink-0" fill="currentColor">
      <circle cx="12" cy="12" r="10" />
    </svg>
  );
}
