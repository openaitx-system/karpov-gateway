"use client";

import { apiFetch } from "@/lib/api/client";

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

export interface NeteaseLoginResult {
  code?: number;
  cookie?: string;
  profile?: {
    userId: number;
    nickname: string;
    avatarUrl: string;
  };
  account?: {
    id: number;
    userName: string;
  };
  [key: string]: unknown;
}

export interface QRKeyResult {
  key: string;
  qrurl: string;
}

export interface QRCheckResult {
  code: number;
  message?: string;
  cookie?: string;
  [key: string]: unknown;
}

export interface CaptchaResult {
  code?: number;
  data?: boolean;
  [key: string]: unknown;
}

export const neteaseAuthApi = {
  loginCellphone(input: {
    phone: string;
    password?: string;
    md5Password?: string;
    captcha?: string;
    countryCode?: string;
  }) {
    return unwrap(
      apiFetch<Wrapped<NeteaseLoginResult>>("/netease/auth/login/cellphone", {
        method: "POST",
        body: input,
      }),
    );
  },

  loginEmail(input: { email: string; password?: string; md5Password?: string }) {
    return unwrap(
      apiFetch<Wrapped<NeteaseLoginResult>>("/netease/auth/login/email", {
        method: "POST",
        body: input,
      }),
    );
  },

  qrKey() {
    return unwrap(
      apiFetch<Wrapped<QRKeyResult>>("/netease/auth/login/qr/key", {
        method: "POST",
        body: {},
      }),
    );
  },

  qrCreate(key: string) {
    return unwrap(
      apiFetch<Wrapped<{ qrurl: string }>>("/netease/auth/login/qr/create", {
        method: "POST",
        body: { key },
      }),
    );
  },

  qrCheck(key: string) {
    return unwrap(
      apiFetch<Wrapped<QRCheckResult>>("/netease/auth/login/qr/check", {
        method: "POST",
        body: { key },
      }),
    );
  },

  loginRefresh() {
    return unwrap(
      apiFetch<Wrapped<NeteaseLoginResult>>("/netease/auth/login/refresh", {
        method: "POST",
        body: {},
      }),
    );
  },

  loginStatus() {
    return unwrap(apiFetch<Wrapped<any>>("/netease/auth/login/status"));
  },

  registerAnonymous() {
    return unwrap(
      apiFetch<Wrapped<NeteaseLoginResult>>("/netease/auth/register/anonymous", {
        method: "POST",
        body: {},
      }),
    );
  },

  captchaSent(phone: string, ctcode?: string) {
    return unwrap(
      apiFetch<Wrapped<CaptchaResult>>("/netease/auth/captcha/sent", {
        method: "POST",
        body: { phone, ctcode },
      }),
    );
  },

  captchaVerify(phone: string, captcha: string, ctcode?: string) {
    return unwrap(
      apiFetch<Wrapped<CaptchaResult>>("/netease/auth/captcha/verify", {
        method: "POST",
        body: { phone, captcha, ctcode },
      }),
    );
  },
};
