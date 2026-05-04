# syntax=docker/dockerfile:1.7
# Music Gateway Web — Next.js 16 multi-stage build
#
# 三阶段:
#   deps    -> 解析锁文件, 装依赖
#   builder -> next build (产物在 .next/)
#   runner  -> 仅保留运行时所需 (.next + node_modules + public + 配置)
#
# 注意: 不使用 next standalone 输出 (避免改 next.config.ts), 代价是镜像多 200MB。

ARG NODE_VERSION=20-alpine

# ---- 1. 安装依赖 ----
FROM node:${NODE_VERSION} AS deps
RUN corepack enable && corepack prepare pnpm@10 --activate
WORKDIR /app
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile --prefer-offline

# ---- 2. 构建 ----
FROM node:${NODE_VERSION} AS builder
RUN corepack enable && corepack prepare pnpm@10 --activate
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
ENV NEXT_TELEMETRY_DISABLED=1 \
    NODE_ENV=production
# NEXT_PUBLIC_* 变量必须在 build time 注入 (会被 inline 进 bundle)
ARG NEXT_PUBLIC_APP_URL
ARG NEXT_PUBLIC_APP_NAME
ENV NEXT_PUBLIC_APP_URL=${NEXT_PUBLIC_APP_URL} \
    NEXT_PUBLIC_APP_NAME=${NEXT_PUBLIC_APP_NAME}
RUN pnpm build

# ---- 3. 运行时 ----
FROM node:${NODE_VERSION} AS runner
RUN apk add --no-cache tzdata && \
    corepack enable && corepack prepare pnpm@10 --activate && \
    addgroup -S nextjs && adduser -S -G nextjs -h /app nextjs
WORKDIR /app

ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    PORT=3000 \
    TZ=Asia/Shanghai

# 仅 COPY 运行时所需; 配置文件 + 已编译 .next + 全量 node_modules
COPY --from=builder --chown=nextjs:nextjs /app/package.json /app/pnpm-lock.yaml ./
COPY --from=builder --chown=nextjs:nextjs /app/next.config.ts ./next.config.ts
COPY --from=builder --chown=nextjs:nextjs /app/tsconfig.json ./tsconfig.json
COPY --from=builder --chown=nextjs:nextjs /app/.next ./.next
COPY --from=builder --chown=nextjs:nextjs /app/public ./public
COPY --from=builder --chown=nextjs:nextjs /app/node_modules ./node_modules

USER nextjs
EXPOSE 3000
CMD ["pnpm", "start"]
