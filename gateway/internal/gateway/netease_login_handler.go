package gateway

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/MiChongs/QQMusicApi/gateway/internal/provider/netease"
)

// NeteaseLoginHandler 网易云音乐登录相关 REST 端点。
type NeteaseLoginHandler struct {
	client *netease.Client
}

func NewNeteaseLoginHandler() *NeteaseLoginHandler {
	return &NeteaseLoginHandler{
		client: netease.NewClient(netease.ClientOptions{}),
	}
}

func (h *NeteaseLoginHandler) Mount(e *gin.Engine) {
	g := e.Group("/v1/netease/auth")
	g.POST("/login/cellphone", h.loginCellphone)
	g.POST("/login/email", h.loginEmail)
	g.POST("/login/qr/key", h.qrKey)
	g.POST("/login/qr/create", h.qrCreate)
	g.POST("/login/qr/check", h.qrCheck)
	g.POST("/login/refresh", h.loginRefresh)
	g.GET("/login/status", h.loginStatus)
	g.POST("/register/anonymous", h.registerAnonymous)
	g.POST("/captcha/sent", h.captchaSent)
	g.POST("/captcha/verify", h.captchaVerify)
}

type cellphoneLoginBody struct {
	Phone       string `json:"phone" binding:"required"`
	Password    string `json:"password"`
	Md5Password string `json:"md5Password"`
	Captcha     string `json:"captcha"`
	CountryCode string `json:"countryCode"`
}

func (h *NeteaseLoginHandler) loginCellphone(c *gin.Context) {
	var body cellphoneLoginBody
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "phone is required")
		return
	}
	if body.Password == "" && body.Md5Password == "" && body.Captcha == "" {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "password, md5Password or captcha required")
		return
	}
	client := h.clientWithCookie(c)
	resp, err := client.LoginCellphone(c.Request.Context(),
		body.Phone, body.Password, body.Md5Password, body.Captcha, body.CountryCode)
	if err != nil {
		Fail(c, http.StatusBadGateway, 50200, err.Error())
		return
	}
	OK(c, resp)
}

type emailLoginBody struct {
	Email       string `json:"email" binding:"required"`
	Password    string `json:"password"`
	Md5Password string `json:"md5Password"`
}

func (h *NeteaseLoginHandler) loginEmail(c *gin.Context) {
	var body emailLoginBody
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "email is required")
		return
	}
	if body.Password == "" && body.Md5Password == "" {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "password or md5Password required")
		return
	}
	client := h.clientWithCookie(c)
	resp, err := client.LoginEmail(c.Request.Context(), body.Email, body.Password, body.Md5Password)
	if err != nil {
		Fail(c, http.StatusBadGateway, 50200, err.Error())
		return
	}
	OK(c, resp)
}

func (h *NeteaseLoginHandler) qrKey(c *gin.Context) {
	client := h.clientWithCookie(c)
	resp, err := client.LoginQRKey(c.Request.Context())
	if err != nil {
		slog.Error("[netease] qrKey failed", "err", err)
		Fail(c, http.StatusBadGateway, 50200, err.Error())
		return
	}
	slog.Info("[netease] qrKey response", "resp", resp)
	// 兼容 {"unikey":"xx"} 和 {"data":{"unikey":"xx"}}
	unikey, _ := resp["unikey"].(string)
	if unikey == "" {
		if data, ok := resp["data"].(map[string]any); ok {
			unikey, _ = data["unikey"].(string)
		}
	}
	if unikey == "" {
		slog.Error("[netease] qrKey: unikey not found in response", "resp", resp)
		Fail(c, http.StatusBadGateway, 50200, fmt.Sprintf("未获取到 unikey: %v", resp))
		return
	}
	OK(c, map[string]any{
		"key":   unikey,
		"qrurl": fmt.Sprintf("https://music.163.com/login?codekey=%s", unikey),
	})
}

type qrCreateBody struct {
	Key   string `json:"key" binding:"required"`
	Qrimg bool   `json:"qrimg"`
}

func (h *NeteaseLoginHandler) qrCreate(c *gin.Context) {
	var body qrCreateBody
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "key is required")
		return
	}
	qrURL := fmt.Sprintf("https://music.163.com/login?codekey=%s", body.Key)
	data := map[string]any{
		"qrurl": qrURL,
	}
	// 如果需要 base64 图片，由前端自行生成 QR（避免后端引入 QR 库依赖）
	OK(c, data)
}

type qrCheckBody struct {
	Key string `json:"key" binding:"required"`
}

func (h *NeteaseLoginHandler) qrCheck(c *gin.Context) {
	var body qrCheckBody
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "key is required")
		return
	}
	slog.Info("[netease] qrCheck", "key", body.Key)
	client := h.clientWithCookie(c)
	resp, err := client.LoginQRCheck(c.Request.Context(), body.Key)
	if err != nil {
		slog.Error("[netease] qrCheck error", "key", body.Key, "err", err)
		OK(c, map[string]any{
			"code":    800,
			"message": err.Error(),
		})
		return
	}
	slog.Info("[netease] qrCheck response", "key", body.Key, "code", resp["code"], "hasMessage", resp["message"] != nil, "hasCookie", resp["cookie"] != nil)
	OK(c, resp)
}

func (h *NeteaseLoginHandler) loginRefresh(c *gin.Context) {
	client := h.clientWithCookie(c)
	resp, err := client.LoginRefresh(c.Request.Context())
	if err != nil {
		Fail(c, http.StatusBadGateway, 50200, err.Error())
		return
	}
	OK(c, resp)
}

func (h *NeteaseLoginHandler) loginStatus(c *gin.Context) {
	client := h.clientWithCookie(c)
	resp, err := client.LoginStatus(c.Request.Context())
	if err != nil {
		Fail(c, http.StatusBadGateway, 50200, err.Error())
		return
	}
	OK(c, resp)
}

func (h *NeteaseLoginHandler) registerAnonymous(c *gin.Context) {
	client := h.clientWithCookie(c)
	resp, err := client.RegisterAnonymous(c.Request.Context())
	if err != nil {
		Fail(c, http.StatusBadGateway, 50200, err.Error())
		return
	}
	OK(c, resp)
}

type captchaSentBody struct {
	Phone  string `json:"phone" binding:"required"`
	Ctcode string `json:"ctcode"`
}

func (h *NeteaseLoginHandler) captchaSent(c *gin.Context) {
	var body captchaSentBody
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "phone is required")
		return
	}
	client := h.clientWithCookie(c)
	resp, err := client.CaptchaSent(c.Request.Context(), body.Phone, body.Ctcode)
	if err != nil {
		Fail(c, http.StatusBadGateway, 50200, err.Error())
		return
	}
	OK(c, resp)
}

type captchaVerifyBody struct {
	Phone   string `json:"phone" binding:"required"`
	Captcha string `json:"captcha" binding:"required"`
	Ctcode  string `json:"ctcode"`
}

func (h *NeteaseLoginHandler) captchaVerify(c *gin.Context) {
	var body captchaVerifyBody
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "phone and captcha required")
		return
	}
	client := h.clientWithCookie(c)
	resp, err := client.CaptchaVerify(c.Request.Context(), body.Phone, body.Captcha, body.Ctcode)
	if err != nil {
		Fail(c, http.StatusBadGateway, 50200, err.Error())
		return
	}
	OK(c, resp)
}

// clientWithCookie 如果请求中带了 netease cookie 则注入。
func (h *NeteaseLoginHandler) clientWithCookie(c *gin.Context) *netease.Client {
	cookie := c.GetHeader("X-Netease-Cookie")
	if cookie == "" {
		cookie = c.Query("cookie")
	}
	if cookie != "" {
		return h.client.WithCookie(cookie)
	}
	return h.client
}
