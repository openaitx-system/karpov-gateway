import { z } from "zod";

/**
 * 客户端可见环境变量（NEXT_PUBLIC_*）。
 *
 * - 仅暴露非敏感展示字段；
 * - 服务端组件也可以 import 本文件（不依赖 server-only API）；
 * - 任何 secret（KEK / Admin token / DB DSN）一律不准出现在这里。
 */
const schema = z.object({
  NEXT_PUBLIC_APP_NAME: z.string().min(1).default("Music Gateway Console"),
  NEXT_PUBLIC_APP_URL: z.string().url().default("http://localhost:3000"),
});

// process.env 在客户端只能读 NEXT_PUBLIC_*，编译期会被替换为字面量。
const parsed = schema.safeParse({
  NEXT_PUBLIC_APP_NAME: process.env.NEXT_PUBLIC_APP_NAME,
  NEXT_PUBLIC_APP_URL: process.env.NEXT_PUBLIC_APP_URL,
});
if (!parsed.success) {
  const issues = parsed.error.issues
    .map((i) => `  - ${i.path.join(".")}: ${i.message}`)
    .join("\n");
  throw new Error(`无效的客户端环境变量配置：\n${issues}`);
}

export const clientEnv = parsed.data;
