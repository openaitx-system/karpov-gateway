"use client";

import { apiFetch } from "@/lib/api/client";

/**
 * 后端 GET /v1/config/runtime 返回的运行时配置。
 *
 * 这是**公开**端点，未登录页（登录页、注册页）也能拉。
 * 不放任何密钥或用户私有信息。
 */
export interface RuntimeConfig {
  /**
   * 后端配置的全局时区。
   * - "Local" 表示后端跟随系统 TZ（前端再 fallback 到浏览器 TZ）
   * - "UTC" / "Asia/Shanghai" 等 IANA 名 → 前端格式化时显式使用
   */
  timezone: string;

  /**
   * 邮箱验证子系统状态。
   * - enabled=false ⇒ 注册表单隐藏"验证码"行；
   * - enabled=true & required=false ⇒ 用户可选填验证码（M19 邮箱激活流复用）；
   * - enabled=true & required=true ⇒ 注册必须先发码再提交。
   */
  email_verification: EmailVerificationRuntime;

  /**
   * 账号激活子系统状态（注册后点邮件链接激活）。
   * - enabled=false ⇒ 注册成功直接跳登录页；
   * - enabled=true & required=true ⇒ 注册成功后切到"已发送激活邮件"提示页；
   *   登录页拦截 412 时显示"未激活，重发激活邮件"按钮。
   */
  account_activation: AccountActivationRuntime;
}

export interface EmailVerificationRuntime {
  enabled: boolean;
  required: boolean;
  cooldown_seconds: number;
  code_ttl_seconds: number;
  hourly_limit_per_email: number;
  /** 精确域名（"qq.com"）或后缀通配（"*.edu.cn"）；空 = 不限。 */
  allowed_domains: string[];
  /** 黑名单（黑名单优先级高于白名单）。 */
  blocked_domains: string[];
  /** 顶部品牌名，前端可直接展示在表单提示里。 */
  app_name?: string;
}

export interface AccountActivationRuntime {
  enabled: boolean;
  required: boolean;
  ttl_seconds: number;
}

interface Wrapped<T> {
  code: number;
  message: string;
  data: T;
}

async function unwrap<T>(promise: Promise<Wrapped<T> | T>): Promise<T> {
  const res = await promise;
  if (res && typeof res === "object" && "data" in res && "code" in res) {
    return (res as Wrapped<T>).data;
  }
  return res as T;
}

export const runtimeApi = {
  get() {
    return unwrap(apiFetch<Wrapped<RuntimeConfig> | RuntimeConfig>("/config/runtime"));
  },
};
