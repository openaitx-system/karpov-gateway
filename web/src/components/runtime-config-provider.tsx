"use client";

import * as React from "react";

import { runtimeApi, type RuntimeConfig } from "@/lib/api/runtime";
import { setRuntimeTimeZone } from "@/lib/datetime";

interface RuntimeConfigContextValue {
  config: RuntimeConfig | null;
  /** 拉取失败时为 true；UI 应当 fallback 到浏览器默认值（已自动处理）。 */
  failed: boolean;
}

const RuntimeConfigContext = React.createContext<RuntimeConfigContextValue>({
  config: null,
  failed: false,
});

/**
 * 应用启动时拉一次 /v1/config/runtime，把后端配置的全局时区注入到
 * @/lib/datetime 模块；后续所有 formatDateTime 自动用这个 zone。
 *
 * 容错：
 *   - 端点 404 / 500 / 超时 → setRuntimeTimeZone 不被调，formatDateTime 走浏览器 TZ。
 *   - 后端返回 timezone="Local" → 也走浏览器 TZ（datetime 模块自己识别）。
 *
 * 不阻塞渲染：useEffect 内拉取，子树立刻挂出。
 */
export function RuntimeConfigProvider({ children }: { children: React.ReactNode }) {
  const [config, setConfig] = React.useState<RuntimeConfig | null>(null);
  const [failed, setFailed] = React.useState(false);

  React.useEffect(() => {
    let cancelled = false;
    runtimeApi
      .get()
      .then((cfg) => {
        if (cancelled) return;
        setRuntimeTimeZone(cfg.timezone);
        setConfig(cfg);
      })
      .catch(() => {
        if (cancelled) return;
        // 静默失败：日期显示自动 fallback 到浏览器 TZ
        setFailed(true);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const value = React.useMemo(() => ({ config, failed }), [config, failed]);

  return (
    <RuntimeConfigContext.Provider value={value}>
      {children}
    </RuntimeConfigContext.Provider>
  );
}

/** 拿到当前 runtime 配置（拉取完成后才有；之前为 null）。 */
export function useRuntimeConfig(): RuntimeConfigContextValue {
  return React.useContext(RuntimeConfigContext);
}
