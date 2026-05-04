package cmd

import (
	"fmt"
	"net/url"
	"os"
)

// ComposePGDSN 在用户没显式给完整 PG DSN 时（existing 为空），尝试从
// POSTGRES_USER / POSTGRES_PASSWORD / POSTGRES_DB / POSTGRES_HOST / POSTGRES_PORT
// 自动拼装一条 DSN —— 这套变量正是 deploy/compose/.env 的现成命名，因此一份 .env
// 同时供 docker-compose 和所有 gateway 二进制使用，无需在多个地方重复声明 DSN。
//
// 触发条件：existing == "" 且 POSTGRES_PASSWORD 非空（password 是判断"用户真的想跑 PG"的最强信号）。
//   - HOST 默认 127.0.0.1（compose 把 5432 映射到宿主）
//   - USER 默认 mgw、DB 默认 mgw、PORT 默认 5432
//   - 永远追加 sslmode=disable（适配本地 compose；生产请直接给完整 DSN 覆盖）
//
// 用户/密码做 url.QueryEscape，DB 名做 url.PathEscape，避免特殊字符破坏 URI。
func ComposePGDSN(existing string) string {
	if existing != "" {
		return existing
	}
	pwd := os.Getenv("POSTGRES_PASSWORD")
	if pwd == "" {
		return ""
	}
	user := defaultEnv("POSTGRES_USER", "mgw")
	db := defaultEnv("POSTGRES_DB", "mgw")
	host := defaultEnv("POSTGRES_HOST", "127.0.0.1")
	port := defaultEnv("POSTGRES_PORT", "5432")
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		url.QueryEscape(user), url.QueryEscape(pwd), host, port, url.PathEscape(db))
}

// ComposeRedisAddr 在用户用了 flag 默认值（127.0.0.1:6379 / :6379）时，尝试用
// REDIS_HOST + REDIS_PORT 替换。用户显式 -redis 或 MGW_*_REDIS 时不动。
//
// 即使只设了一边（例如只填 REDIS_HOST），另一边也会用合理 fallback 拼成完整 addr。
func ComposeRedisAddr(existing string) string {
	if existing != "" && existing != "127.0.0.1:6379" && existing != ":6379" {
		return existing
	}
	host := os.Getenv("REDIS_HOST")
	port := os.Getenv("REDIS_PORT")
	if host == "" && port == "" {
		return existing
	}
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "6379"
	}
	return host + ":" + port
}

func defaultEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
