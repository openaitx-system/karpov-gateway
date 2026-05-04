"use client";

import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { Button } from "@/components/ui/button";

// 后端 oauth.CallbackError.Code 全集; 中文文案 + 是否给"返回登录"按钮.
const CODE_TEXTS: Record<string, { title: string; hint: string; action?: { label: string; href: string } }> = {
  state_missing: {
    title: "登录会话已过期",
    hint: "OAuth 跳转流程超时 (15 分钟). 请回到登录页重新点击.",
    action: { label: "返回登录", href: "/login" },
  },
  state_invalid: {
    title: "登录会话无效",
    hint: "请回到登录页重新点击, 不要在多个标签页同时操作.",
    action: { label: "返回登录", href: "/login" },
  },
  state_mismatch: {
    title: "登录会话校验失败",
    hint: "可能是浏览器拒绝了 cookie, 或者跨标签页操作冲突. 重新点一次登录即可.",
    action: { label: "返回登录", href: "/login" },
  },
  state_provider_mismatch: {
    title: "登录会话错位",
    hint: "请重新点击登录, 不要混合多个第三方账号同时操作.",
    action: { label: "返回登录", href: "/login" },
  },
  exchange_failed: {
    title: "授权码换取 token 失败",
    hint: "请重试; 持续失败请联系管理员检查 OAuth 配置.",
    action: { label: "返回登录", href: "/login" },
  },
  exchange_empty_token: {
    title: "第三方未返回 token",
    hint: "请重试; 持续失败请联系管理员.",
    action: { label: "返回登录", href: "/login" },
  },
  userinfo_failed: {
    title: "无法读取第三方用户信息",
    hint: "请重试; 持续失败请检查授权范围.",
    action: { label: "返回登录", href: "/login" },
  },
  validate_failed: {
    title: "账号不满足登录要求",
    hint: "请确认账号已激活 / 等级达标后重试.",
    action: { label: "返回登录", href: "/login" },
  },
  email_exists: {
    title: "邮箱已被本地账号占用",
    hint: "请先用邮箱密码登录, 然后到「设置」页面绑定第三方账号.",
    action: { label: "用邮箱登录", href: "/login" },
  },
  already_bound: {
    title: "此第三方账号已被其他本地账号绑定",
    hint: "解除原账号绑定后才能绑到当前账号. 也可以登录原账号继续使用.",
    action: { label: "返回登录", href: "/login" },
  },
  account_locked: {
    title: "账号已锁定 / 禁用",
    hint: "请联系管理员解封后再登录.",
    action: { label: "返回登录", href: "/login" },
  },
  session_required: {
    title: "需要先登录",
    hint: "绑定第三方账号前请先用邮箱密码登录.",
    action: { label: "去登录", href: "/login" },
  },
  session_mismatch: {
    title: "会话不匹配",
    hint: "请保持浏览器同一标签页完成绑定.",
    action: { label: "返回设置", href: "/settings" },
  },
  unknown_provider: {
    title: "未知的第三方提供方",
    hint: "服务端没有启用该 provider, 请联系管理员.",
    action: { label: "返回登录", href: "/login" },
  },
  access_denied: {
    title: "你拒绝了授权",
    hint: "如需登录, 请回到登录页重新点击并完成授权.",
    action: { label: "返回登录", href: "/login" },
  },
};

const FALLBACK = {
  title: "登录失败",
  hint: "请重试; 持续失败请联系管理员.",
  action: { label: "返回登录", href: "/login" },
};

export function OAuthErrorClient() {
  const search = useSearchParams();
  const code = search.get("code") || "";
  const message = search.get("message") || "";
  const info = CODE_TEXTS[code] || FALLBACK;

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">{info.title}</h1>
        <p className="mt-2 text-sm text-muted-foreground">{info.hint}</p>
      </div>
      {message && (
        <div className="rounded-md border bg-muted/40 px-3 py-2">
          <p className="text-xs text-muted-foreground">详细信息</p>
          <p className="break-all text-xs font-mono">{message}</p>
        </div>
      )}
      {info.action && (
        <Button asChild className="w-full">
          <Link href={info.action.href}>{info.action.label}</Link>
        </Button>
      )}
    </div>
  );
}
