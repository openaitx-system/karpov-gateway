# Licensing Notes

整个 `gateway/` 目录默认沿用根仓库 LICENSE（GNU GPL v3.0 or later）。

## 单文件许可证例外

| 路径 | 许可证 | 来源 |
|---|---|---|
| `internal/provider/qqmusic/algorithms/tripledes/*.go` | GPL-3.0-only（**not** "or later"） | 算法移植自 `qqmusic_api/algorithms/tripledes.py`，该文件来源于 [LDDC 项目](https://github.com/chenmozhijin/LDDC)，文件头明示 `SPDX-License-Identifier: GPL-3.0-only` |

**含义**：使用了 `tripledes` 子包的 binary 必须以 GPL-3.0-only（或更严格的兼容许可）发布，**不能**升级为 GPL-3.0-or-later 或其他不兼容许可。

## 第三方依赖许可证摘要

主要依赖均为宽松许可（MIT / BSD / Apache-2.0），不影响整体 GPL 合规：

- gin-gonic/gin (MIT), grpc-go (Apache-2.0), grpc-gateway (BSD-3)
- pgx (MIT), sqlc (MIT), golang-migrate (MIT)
- go-redis (BSD-2), redis_rate (BSD-2), asynq (MIT)
- TarsCloud/TarsGo (BSD-3), eclipse/paho.golang (EPL-2.0 + EDL)
- looplab/fsm (MIT), shopspring/decimal (MIT), google/uuid (BSD-3)

完整清单运行 `go mod why -m all` 与 `go-licenses report ./...` 获取。

## SBOM

发布时生成 SPDX SBOM（`make sbom`），随 GitHub Release 一起公开。
