"use client";

import * as React from "react";
import { CreditCard, FlaskConical, Smartphone, Wallet } from "lucide-react";

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { billingApi, type PaymentChannelPublic } from "@/lib/api/billing";

const ICON_MAP: Record<string, React.ComponentType<{ className?: string }>> = {
  alipay: Wallet,
  wechat: Smartphone,
  credit: CreditCard,
  test: FlaskConical,
  generic: CreditCard,
};

export interface PaymentMethodSelectProps {
  value: string;
  onChange: (provider: string) => void;
  disabled?: boolean;
  /** true 时，会在加载完成后跳过 mock 选第一个真实渠道。默认 true。 */
  preferRealProvider?: boolean;
}

/**
 * 用户端的支付方式选择组件。
 *
 * 行为：
 *  - 挂载时调用 `GET /v1/billing/payment-channels`，把后端返回的"已启用渠道"塞进 select。
 *  - 加载中显示 disabled 占位；加载失败/空列表显示提示卡片，引导联系管理员。
 *  - 当后端列表中不包含当前 value 时，自动选一个合理默认值（preferRealProvider=true 时优先非 mock）。
 */
export function PaymentMethodSelect({
  value,
  onChange,
  disabled,
  preferRealProvider = true,
}: PaymentMethodSelectProps) {
  const [channels, setChannels] = React.useState<PaymentChannelPublic[] | null>(
    null,
  );
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);

  React.useEffect(() => {
    let canceled = false;
    billingApi
      .listPaymentChannels()
      .then((res) => {
        if (canceled) return;
        const list = res.channels ?? [];
        setChannels(list);
        if (list.length === 0) {
          // 没有可用渠道：把上层的 value 清空，让 dialog 的"提交"按钮可以根据空值禁用。
          if (value !== "") onChange("");
          return;
        }
        if (!list.some((c) => c.provider === value)) {
          const preferred = preferRealProvider
            ? (list.find((c) => c.provider !== "mock") ?? list[0])
            : list[0];
          onChange(preferred.provider);
        }
      })
      .catch((e) => {
        if (canceled) return;
        setError(e?.message ?? "加载失败");
        setChannels([]);
        if (value !== "") onChange("");
      })
      .finally(() => {
        if (!canceled) setLoading(false);
      });
    return () => {
      canceled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (loading) {
    return (
      <Select disabled value="">
        <SelectTrigger>
          <SelectValue placeholder="正在加载支付方式…" />
        </SelectTrigger>
        <SelectContent />
      </Select>
    );
  }

  if (!channels || channels.length === 0) {
    return (
      <div className="rounded-md border border-amber-300 bg-amber-50 p-3 text-xs leading-5 text-amber-900 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-100">
        {error
          ? `加载支付方式失败：${error}`
          : "暂无可用支付方式。请联系管理员前往「系统设置 → 支付渠道」启用至少一个渠道。"}
      </div>
    );
  }

  return (
    <Select value={value} onValueChange={onChange} disabled={disabled}>
      <SelectTrigger>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {channels.map((ch) => {
          const Icon = ICON_MAP[ch.icon ?? "generic"] ?? CreditCard;
          return (
            <SelectItem key={ch.provider} value={ch.provider}>
              <span className="flex items-center gap-2">
                <Icon className="size-4 text-muted-foreground" />
                <span className="flex flex-col text-left">
                  <span>{ch.displayName}</span>
                  {ch.description && (
                    <span className="text-[10px] text-muted-foreground">
                      {ch.description}
                    </span>
                  )}
                </span>
              </span>
            </SelectItem>
          );
        })}
      </SelectContent>
    </Select>
  );
}
