// Package migrations 用 //go:embed 把所有 schema 的 SQL 迁移文件打包进二进制，
// 供 internal/store 的 migrate 工具按 schema 分别加载。
package migrations

import "embed"

// FS 包含 auth/, quota/, billing/, pool/ 四个子目录下的 *.sql 文件。
//
//go:embed all:auth all:quota all:billing all:pool
var FS embed.FS
