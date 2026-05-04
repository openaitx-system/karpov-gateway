"use client";

import { apiFetch } from "@/lib/api/client";
import type {
  APIKey,
  APIKeyList,
  LoginResponse,
  RegisterResponse,
  TOTPResult,
  TOTPSecret,
  User,
} from "@/types/api";

export const authApi = {
  register(input: {
    email: string;
    password: string;
    /** 后端启用"邮箱验证码注册"时必填；否则忽略即可。 */
    verificationCode?: string;
  }) {
    return apiFetch<RegisterResponse>("/auth/register", {
      method: "POST",
      body: input,
    });
  },
  /**
   * 发送邮箱验证码。后端有 cooldown / per-email / per-IP 三层频控；
   * 命中频控时抛 ApiError(status=429)，前端按返回的 cooldown 倒计时。
   */
  sendEmailCode(input: { email: string; purpose?: "register" | "reset" }) {
    return apiFetch<{ cooldownSeconds: number; expiresInSeconds: number }>(
      "/auth/email/send-code",
      { method: "POST", body: { email: input.email, purpose: input.purpose ?? "register" } },
    );
  },
  /**
   * 用激活 token 激活账号。
   * 错误码语义：
   *   - 404 invalid token
   *   - 410 expired (DeadlineExceeded → grpc-gateway 504，但本系统映射为 410；具体见 mapAuthError)
   *   - 409 already-used / already-active
   */
  verifyEmail(token: string) {
    return apiFetch<{ userId: string; email: string }>("/auth/email/verify", {
      method: "POST",
      body: { token },
    });
  },
  /**
   * 重发激活邮件。
   * 后端故意"静默成功"——邮箱不存在 / 已激活时仍返回 200，避免暴露用户存在性；
   * 前端文案应当是"如该邮箱有未激活账号，激活邮件已重发"而不是"已发送"。
   */
  resendActivation(email: string) {
    return apiFetch<{ cooldownSeconds: number }>("/auth/email/resend", {
      method: "POST",
      body: { email },
    });
  },
  login(input: { email: string; password: string; totpCode?: string }) {
    return apiFetch<LoginResponse>("/auth/login", {
      method: "POST",
      body: input,
    });
  },
  logout() {
    return apiFetch<void>("/auth/logout", { method: "POST", body: {} });
  },
  me() {
    return apiFetch<User>("/auth/me");
  },
  changePassword(input: { oldPassword: string; newPassword: string }) {
    return apiFetch<void>("/auth/password/change", {
      method: "POST",
      body: input,
    });
  },

  // ---- 2FA / TOTP ----
  // 启用第一步：服务端生成 secret + otpauth URL，缓存为待确认（10 分钟内必须 confirm）
  enableTOTP() {
    return apiFetch<TOTPSecret>("/auth/totp/enable", {
      method: "POST",
      body: {},
    });
  },
  // 启用第二步：用 Authenticator 当前 6 位码确认。成功返回更新后的 User
  confirmEnableTOTP(code: string) {
    return apiFetch<User>("/auth/totp/enable/confirm", {
      method: "POST",
      body: { code },
    });
  },
  // 登录第二步：拿 challengeId + 6 位码换 sid
  verifyTOTP(challengeId: string, code: string) {
    return apiFetch<TOTPResult>("/auth/totp/verify", {
      method: "POST",
      body: { challengeId, code },
    });
  },
  // 关闭：要求当前 6 位码二次确认
  disableTOTP(code: string) {
    return apiFetch<void>("/auth/totp/disable", {
      method: "POST",
      body: { code },
    });
  },
};

export const apiKeysApi = {
  list() {
    return apiFetch<APIKeyList>("/auth/api-keys");
  },
  get(id: string) {
    return apiFetch<APIKey>(`/auth/api-keys/${encodeURIComponent(id)}`);
  },
  create(input: {
    name: string;
    description?: string;
    scopes?: string[];
    ipAllowlist?: string[];
    rateLimitRpm?: number;
    rateLimitDaily?: number;
    expiresAt?: string;
  }) {
    return apiFetch<APIKey>("/auth/api-keys", { method: "POST", body: input });
  },
  update(
    id: string,
    input: {
      name?: string;
      description?: string;
      scopes?: string[];
      ipAllowlist?: string[];
      rateLimitRpm?: number;
      rateLimitDaily?: number;
      expiresAt?: string;
    },
  ) {
    return apiFetch<APIKey>(`/auth/api-keys/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: input,
    });
  },
  setEnabled(id: string, enabled: boolean) {
    return apiFetch<void>(
      `/auth/api-keys/${encodeURIComponent(id)}/enabled`,
      { method: "POST", body: { enabled } },
    );
  },
  revoke(id: string) {
    return apiFetch<void>(`/auth/api-keys/${encodeURIComponent(id)}`, {
      method: "DELETE",
    });
  },
};
