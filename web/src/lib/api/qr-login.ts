"use client";

import { apiFetch } from "@/lib/api/client";

/**
 * QR 登录 admin REST 客户端。
 *
 * 后端契约（gateway/internal/gateway/login_qr.go）：
 *   POST /v1/admin/login/qr/start   { platform: "qq"|"wx" }  → { session_id, image, expires_in_sec }
 *   GET  /v1/admin/login/qr/{id}                            → { event, credential?, error? }
 */

export type QRPlatform = "qq" | "wx";

export type QREvent =
  | "init"
  | "scan"
  | "conf"
  | "done"
  | "timeout"
  | "refuse"
  | "other";

export interface QRStartResponse {
  session_id: string;
  /** data:image/png;base64,... */
  image: string;
  expires_in_sec: number;
  platform: QRPlatform;
}

export interface QRPollResponse {
  event: QREvent;
  /** DONE 时存在；字段名遵循 qqmusic.Credential JSON */
  credential?: Record<string, unknown>;
  error?: string;
}

export const qrLoginApi = {
  start(platform: QRPlatform) {
    return apiFetch<QRStartResponse>("/admin/login/qr/start", {
      method: "POST",
      body: { platform },
    });
  },
  poll(sessionId: string) {
    return apiFetch<QRPollResponse>(
      `/admin/login/qr/${encodeURIComponent(sessionId)}`,
    );
  },
};

/**
 * 把 QR 事件翻译成中文友好提示。
 */
export function qrEventLabel(event: QREvent): { text: string; tone: "ok" | "info" | "warn" | "bad" } {
  switch (event) {
    case "init":
      return { text: "二维码已生成，等待扫码…", tone: "info" };
    case "scan":
      return { text: "已扫码，请在手机上点击「确认登录」", tone: "info" };
    case "conf":
      return { text: "已扫码，请在手机上点击「确认登录」", tone: "info" };
    case "done":
      return { text: "登录成功，凭证已派发", tone: "ok" };
    case "timeout":
      return { text: "二维码已过期，请重新生成", tone: "warn" };
    case "refuse":
      return { text: "用户拒绝了登录请求", tone: "warn" };
    case "other":
      return { text: "登录状态异常", tone: "bad" };
  }
}
