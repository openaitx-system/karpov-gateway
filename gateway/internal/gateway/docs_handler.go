package gateway

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed docs/openapi.yaml
var openapiYAML []byte

// DocsHandler 暴露 OpenAPI 文档（YAML）。
//
// 设计：
//   - 只 embed YAML 一份；Scalar / Redoc / Swagger UI 等渲染器都支持直接消费 YAML。
//   - 不在 Go 端转 JSON，避免引入额外 yaml 解析依赖。
//   - 路径：
//       GET /v1/docs/openapi.yaml  → 原始 YAML（application/yaml）
//       GET /v1/docs/openapi.json  → 同一份 YAML，仅 Content-Type 切换为 application/yaml；
//                                    现代 OpenAPI 渲染器对 .json 后缀也接受 YAML 内容。
//                                    保留 .json 路径是为了与一些只看后缀的客户端兼容。
type DocsHandler struct{}

// NewDocsHandler 构造 handler。
func NewDocsHandler() *DocsHandler { return &DocsHandler{} }

// Mount 挂载到 gin engine。
func (h *DocsHandler) Mount(r *gin.Engine) {
	g := r.Group("/v1/docs")
	g.GET("/openapi.yaml", h.yaml)
}

func (h *DocsHandler) yaml(c *gin.Context) {
	c.Header("Cache-Control", "public, max-age=60")
	c.Data(http.StatusOK, "application/yaml; charset=utf-8", openapiYAML)
}
