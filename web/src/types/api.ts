/**
 * 后端 REST 响应类型映射（与 gateway/api/proto/v1/*.proto 对齐）。
 *
 * 注意：grpc-gateway 默认输出 lowerCamelCase，下面字段名严格按生成结果。
 */

export type Role = "user" | "admin" | "superadmin";

export interface User {
  id: string;
  email: string;
  status: string; // active / locked / pending_email
  totpEnabled: boolean;
  createdAt?: string;
  lastLoginAt?: string;
  role: Role;
}

export interface LoginResponse {
  sid: string;
  expiresAt?: string;
  totpRequired?: boolean;
  challengeId?: string;
}

export interface TOTPSecret {
  secretBase32: string;
  otpauthUrl: string;
}

export interface TOTPResult {
  sid: string;
  expiresAt?: string;
}

export interface RegisterResponse {
  userId: string;
  email?: string;
  emailVerificationRequired: boolean;
  /** 后端发出激活邮件后返回 true；前端据此切到"激活待办"卡片。 */
  activationEmailSent?: boolean;
}

export type APIKeyStatus = "active" | "disabled" | "expired" | "revoked";

export interface APIKey {
  id: string;
  prefix: string;
  plaintext?: string; // 仅创建时返回
  name: string;
  description?: string;
  scopes?: string[];
  ipAllowlist?: string[];
  rateLimitRpm?: number;   // 每分钟请求数上限（0 = 不限）
  rateLimitDaily?: number; // 每日请求数上限（0 = 不限）
  enabled?: boolean;
  status?: APIKeyStatus;
  totalRequests?: number;
  expiresAt?: string;
  createdAt?: string;
  lastUsedAt?: string;
}

export interface APIKeyList {
  items: APIKey[];
}

export interface ProblemDetail {
  code?: number;
  message?: string;
  details?: unknown[];
}

/**
 * Pool 凭证（与 musicgw.pool.v1.Credential 对齐，grpc-gateway 输出 lowerCamelCase）。
 */
export interface PoolCredential {
  id: string;
  provider: string;
  label: string;
  status: string; // active / disabled / banned / refreshing
  healthScore: number; // [0, 1]
  failCount?: number;
  lastUsedAt?: string;
  lastFailedAt?: string;
  cooldownUntil?: string;
  capabilities?: string[];
  createdAt?: string;
  expiresAt?: string;
}

export interface PoolCredentialList {
  items: PoolCredential[];
  total: number;
}

export interface ProviderPool {
  provider: string;
  active?: number;
  disabled?: number;
  banned?: number;
  inCooldown?: number;
  avgHealthScore?: number;
}

export interface PoolHealth {
  pools: ProviderPool[];
}

export interface AddPoolCredentialInput {
  provider: string;
  label: string;
  /** 已 base64 编码的明文 payload（grpc-gateway 把 bytes 字段做 base64 in/out） */
  payload: string;
  capabilities?: string[];
}
