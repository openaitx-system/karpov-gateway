import "server-only";

import { z } from "zod";

/**
 * 服务端环境变量。
 *
 * - 引入 `server-only`：任何客户端组件直接/间接 import 都会在编译期报错；
 *   彻底杜绝 BACKEND_URL / 代理超时等敏感配置泄露到 bundle。
 * - 模块加载时执行 zod 校验，缺失/格式错误立即抛出，避免运行期发散。
 */
const schema = z.object({
  BACKEND_URL: z
    .string()
    .url("BACKEND_URL 必须是合法 URL，例如 http://localhost:8080"),
  SESSION_COOKIE_NAME: z.string().min(1).default("sid"),
  CSRF_COOKIE_NAME: z.string().min(1).default("csrf_token"),
  CSRF_HEADER_NAME: z.string().min(1).default("X-CSRF-Token"),
  TRUST_PROXY: z
    .union([z.literal("true"), z.literal("false")])
    .default("false")
    .transform((v) => v === "true"),
  PROXY_TIMEOUT_MS: z
    .string()
    .regex(/^\d+$/)
    .default("15000")
    .transform((v) => Number.parseInt(v, 10)),
  NODE_ENV: z
    .enum(["development", "production", "test"])
    .default("development"),
});

const parsed = schema.safeParse(process.env);
if (!parsed.success) {
  const issues = parsed.error.issues
    .map((i) => `  - ${i.path.join(".")}: ${i.message}`)
    .join("\n");
  throw new Error(`无效的服务端环境变量配置：\n${issues}`);
}

export const serverEnv = parsed.data;
