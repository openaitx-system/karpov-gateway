import { z } from "zod";

/**
 * 客户端表单 zod schema —— 仅用于减少无效请求；
 * 服务端 gateway/internal/auth 已有 zxcvbn / 邮箱格式 / argon2 等强校验，
 * 前端校验绝不可作为最终安全边界。
 */
export const emailSchema = z
  .string()
  .trim()
  .min(1, "请输入邮箱")
  .max(254, "邮箱过长")
  .email("邮箱格式不正确");

/**
 * 密码：最低 8 位，至少含一个字母 + 数字。
 * 强度评分由后端 zxcvbn 决定（拒弱口令），前端仅做基础门槛。
 */
export const passwordSchema = z
  .string()
  .min(8, "密码至少 8 位")
  .max(128, "密码过长")
  .regex(/[a-zA-Z]/, "密码必须包含字母")
  .regex(/\d/, "密码必须包含数字");

export const loginSchema = z.object({
  email: emailSchema,
  password: z.string().min(1, "请输入密码"),
  totpCode: z
    .string()
    .regex(/^\d{6}$/, "动态验证码为 6 位数字")
    .optional()
    .or(z.literal("")),
});

export type LoginInput = z.infer<typeof loginSchema>;

export const registerSchema = z
  .object({
    email: emailSchema,
    password: passwordSchema,
    confirm: z.string().min(1, "请再次输入密码"),
    /**
     * 邮箱验证码：后端启用"必填"时前端会再做长度/数字校验。
     * 这里保持基础门槛——空 / 6 位数字两种合法输入；表单层会按 runtime config 加强。
     */
    verificationCode: z
      .string()
      .regex(/^\d{6}$/, "验证码为 6 位数字")
      .optional()
      .or(z.literal("")),
  })
  .refine((d) => d.password === d.confirm, {
    path: ["confirm"],
    message: "两次输入的密码不一致",
  });

export type RegisterInput = z.infer<typeof registerSchema>;

export const changePasswordSchema = z
  .object({
    oldPassword: z.string().min(1, "请输入当前密码"),
    newPassword: passwordSchema,
    confirm: z.string().min(1, "请再次输入新密码"),
  })
  .refine((d) => d.newPassword === d.confirm, {
    path: ["confirm"],
    message: "两次输入的密码不一致",
  })
  .refine((d) => d.oldPassword !== d.newPassword, {
    path: ["newPassword"],
    message: "新密码不能与当前密码相同",
  });

export type ChangePasswordInput = z.infer<typeof changePasswordSchema>;

export const createApiKeySchema = z.object({
  name: z
    .string()
    .trim()
    .min(1, "请输入名称")
    .max(64, "名称最多 64 字符"),
  description: z
    .string()
    .max(256, "描述最多 256 字符")
    .optional()
    .or(z.literal("")),
  scopes: z
    .array(
      z
        .string()
        .regex(
          /^(\*|[a-z]+:(\*|[a-z]+))$/,
          'scope 必须是 "*" / "module:*" / "module:action"',
        ),
    )
    .max(20, "scope 数量最多 20 个")
    .optional(),
  ipAllowlist: z
    .array(
      z
        .string()
        .regex(
          /^(\d{1,3}\.){3}\d{1,3}(\/\d{1,2})?$/,
          "IP 必须是 IPv4 或 CIDR",
        ),
    )
    .max(20, "IP 白名单最多 20 条")
    .optional(),
  rateLimitRpm: z
    .number()
    .int()
    .min(0, "RPM 不能为负数")
    .max(100000, "RPM 上限 100000")
    .optional(),
  rateLimitDaily: z
    .number()
    .int()
    .min(0, "日限额不能为负数")
    .max(10000000, "日限额上限 10000000")
    .optional(),
  expiresAt: z
    .string()
    .datetime({ offset: true, message: "过期时间必须是 ISO 8601 时间" })
    .optional()
    .or(z.literal("")),
});

export type CreateApiKeyInput = z.infer<typeof createApiKeySchema>;

export const updateApiKeySchema = z.object({
  name: z
    .string()
    .trim()
    .min(1, "请输入名称")
    .max(64, "名称最多 64 字符"),
  description: z
    .string()
    .max(256, "描述最多 256 字符")
    .optional()
    .or(z.literal("")),
  scopes: z
    .array(
      z
        .string()
        .regex(
          /^(\*|[a-z]+:(\*|[a-z]+))$/,
          'scope 必须是 "*" / "module:*" / "module:action"',
        ),
    )
    .max(20, "scope 数量最多 20 个")
    .optional(),
  ipAllowlist: z
    .array(
      z
        .string()
        .regex(
          /^(\d{1,3}\.){3}\d{1,3}(\/\d{1,2})?$/,
          "IP 必须是 IPv4 或 CIDR",
        ),
    )
    .max(20, "IP 白名单最多 20 条")
    .optional(),
  rateLimitRpm: z
    .number()
    .int()
    .min(0, "RPM 不能为负数")
    .max(100000, "RPM 上限 100000")
    .optional(),
  rateLimitDaily: z
    .number()
    .int()
    .min(0, "日限额不能为负数")
    .max(10000000, "日限额上限 10000000")
    .optional(),
  expiresAt: z
    .string()
    .datetime({ offset: true, message: "过期时间必须是 ISO 8601 时间" })
    .optional()
    .or(z.literal("")),
});

export type UpdateApiKeyInput = z.infer<typeof updateApiKeySchema>;

/**
 * 号池凭证：label 1-64，provider 必填，payload 任意非空文本（前端再 base64），
 * capabilities 可选标签数组（用于 capability 路由）。
 */
export const addPoolCredentialSchema = z.object({
  provider: z
    .string()
    .trim()
    .min(1, "请输入 provider，例如 qqmusic")
    .max(32, "provider 过长"),
  label: z
    .string()
    .trim()
    .min(1, "请输入凭证标签")
    .max(64, "标签最多 64 字符"),
  payload: z
    .string()
    .min(1, "请粘贴凭证内容（cookie / token / JSON）")
    .max(16384, "凭证内容过大"),
  capabilities: z
    .array(z.string().regex(/^Cap[A-Z][A-Za-z0-9]*$/, "capability 形如 CapGetSong"))
    .max(64, "能力数量过多")
    .optional(),
});

export type AddPoolCredentialInput = z.infer<typeof addPoolCredentialSchema>;
