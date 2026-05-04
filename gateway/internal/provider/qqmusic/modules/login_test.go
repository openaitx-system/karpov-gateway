package modules

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
)

// loginServer 构造一个返回固定 JSON 的 musicu.fcg mock。
func loginServer(t *testing.T, respJSON string) (*qqmusic.Client, *string) {
	t.Helper()
	captured := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*captured = string(body)
		_, _ = w.Write([]byte(respJSON))
	}))
	t.Cleanup(srv.Close)
	httpc := qqmusic.NewHTTPClient(qqmusic.HTTPClientOptions{
		MaxRetries: 0, MaxConnections: 4, HTTP2: false,
	})
	c := qqmusic.NewClient(qqmusic.ClientOptions{
		HTTP:       httpc,
		Platform:   qqmusic.PlatformAndroid,
		MusicuURL:  srv.URL,
		Credential: &qqmusic.Credential{},
	})
	return c, captured
}

func TestSendPhoneAuthCode_Send(t *testing.T) {
	c, body := loginServer(t, `{"req_0":{"code":0,"data":{}}}`)
	res, err := SendPhoneAuthCode(context.Background(), c, SendPhoneAuthCodeOptions{
		PhoneNo: 13800000000, CountryCode: 86,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Event != PhoneLoginEventSend {
		t.Errorf("event=%v want SEND", res.Event)
	}
	// 校验请求参数
	var req map[string]any
	_ = json.Unmarshal([]byte(*body), &req)
	r0 := req["req_0"].(map[string]any)
	if r0["module"] != "music.login.LoginServer" || r0["method"] != "SendPhoneAuthCode" {
		t.Errorf("wrong module/method: %v", r0)
	}
	param := r0["param"].(map[string]any)
	if param["phoneNo"] != "13800000000" || param["areaCode"] != "86" {
		t.Errorf("phoneNo/areaCode: %+v", param)
	}
	comm := req["comm"].(map[string]any)
	if comm["tmeLoginMethod"] != float64(3) {
		t.Errorf("tmeLoginMethod: %v", comm["tmeLoginMethod"])
	}
}

func TestSendPhoneAuthCode_Captcha(t *testing.T) {
	c, _ := loginServer(t, `{"req_0":{"code":20276,"data":{"securityURL":"https://example.com/cap"}}}`)
	res, err := SendPhoneAuthCode(context.Background(), c, SendPhoneAuthCodeOptions{
		PhoneNo: 13800000000,
	})
	if err != nil {
		t.Fatalf("captcha should not error: %v", err)
	}
	if res.Event != PhoneLoginEventCaptcha {
		t.Errorf("event: %v", res.Event)
	}
	if res.Info != "https://example.com/cap" {
		t.Errorf("info: %q", res.Info)
	}
}

func TestSendPhoneAuthCode_Frequency(t *testing.T) {
	c, _ := loginServer(t, `{"req_0":{"code":100001,"data":{}}}`)
	res, err := SendPhoneAuthCode(context.Background(), c, SendPhoneAuthCodeOptions{PhoneNo: 1})
	if !errors.Is(err, ErrLoginRateLimited) {
		t.Errorf("expected ErrLoginRateLimited, got %v", err)
	}
	if res.Event != PhoneLoginEventFrequency {
		t.Errorf("event: %v", res.Event)
	}
}

func TestSendPhoneAuthCode_RequiresInput(t *testing.T) {
	c, _ := loginServer(t, `{"req_0":{"code":0}}`)
	if _, err := SendPhoneAuthCode(context.Background(), c, SendPhoneAuthCodeOptions{}); err == nil {
		t.Errorf("expected error for missing phone")
	}
}

func TestPhoneAuthorize_Success(t *testing.T) {
	c, body := loginServer(t, `{"req_0":{"code":0,"data":{"openid":"O1","musicid":1234,"musickey":"W_X_KEY","unionid":"U1"}}}`)
	cred, err := PhoneAuthorize(context.Background(), c, PhoneAuthorizeOptions{
		PhoneNo: 13900000000, AuthCode: 999888,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cred.OpenID != "O1" || cred.MusicID != 1234 {
		t.Errorf("cred fields: %+v", cred)
	}
	// W_X 前缀 → LoginType=1
	if cred.LoginType != 1 {
		t.Errorf("login_type: %d want 1", cred.LoginType)
	}
	// 校验请求 comm
	var req map[string]any
	_ = json.Unmarshal([]byte(*body), &req)
	comm := req["comm"].(map[string]any)
	if comm["tmeLoginMethod"] != float64(3) || comm["tmeLoginType"] != float64(0) {
		t.Errorf("comm: %+v", comm)
	}
	r0 := req["req_0"].(map[string]any)
	param := r0["param"].(map[string]any)
	if param["code"] != "999888" || param["loginMode"] != float64(1) {
		t.Errorf("param: %+v", param)
	}
}

func TestPhoneAuthorize_BadAuthCode(t *testing.T) {
	c, _ := loginServer(t, `{"req_0":{"code":20271,"data":{}}}`)
	_, err := PhoneAuthorize(context.Background(), c, PhoneAuthorizeOptions{PhoneNo: 1, AuthCode: 1})
	if !errors.Is(err, ErrLoginAuthCode) {
		t.Errorf("expected ErrLoginAuthCode, got %v", err)
	}
}

func TestPhoneAuthorize_DeviceLimit(t *testing.T) {
	c, _ := loginServer(t, `{"req_0":{"code":20279,"data":{}}}`)
	_, err := PhoneAuthorize(context.Background(), c, PhoneAuthorizeOptions{PhoneNo: 1, AuthCode: 1})
	if !errors.Is(err, ErrLoginDeviceLimit) {
		t.Errorf("expected ErrLoginDeviceLimit, got %v", err)
	}
}

func TestRefreshCredential_QQ(t *testing.T) {
	c, body := loginServer(t, `{"req_0":{"code":0,"data":{"openid":"O","musicid":7,"musickey":"W_X_K","unionid":"u"}}}`)
	in := &qqmusic.Credential{
		LoginType: 1, OpenID: "Oin", MusicKey: "W_X_OLD",
		RefreshToken: "RT", RefreshKey: "RK", StrMusicID: "7", UnionID: "u",
	}
	cred, err := RefreshCredential(context.Background(), c, in)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cred.MusicID != 7 {
		t.Errorf("musicid: %d", cred.MusicID)
	}
	// 校验 QQ refresh param
	var req map[string]any
	_ = json.Unmarshal([]byte(*body), &req)
	param := req["req_0"].(map[string]any)["param"].(map[string]any)
	if param["openid"] != "Oin" || param["str_musicid"] != "7" {
		t.Errorf("qq param: %+v", param)
	}
	if _, has := param["access_token"]; has {
		t.Errorf("qq path should not include access_token: %+v", param)
	}
	// comm.tmeLoginType = login_type
	if req["comm"].(map[string]any)["tmeLoginType"] != float64(1) {
		t.Errorf("tmeLoginType: %v", req["comm"])
	}
}

func TestRefreshCredential_WX(t *testing.T) {
	c, body := loginServer(t, `{"req_0":{"code":0,"data":{"musicid":9,"musickey":"WX_K"}}}`)
	in := &qqmusic.Credential{
		LoginType: 2, AccessToken: "AT", RefreshToken: "RT", ExpiredAt: 12345, MusicID: 9, MusicKey: "OLD",
	}
	if _, err := RefreshCredential(context.Background(), c, in); err != nil {
		t.Fatalf("err: %v", err)
	}
	var req map[string]any
	_ = json.Unmarshal([]byte(*body), &req)
	param := req["req_0"].(map[string]any)["param"].(map[string]any)
	if param["access_token"] != "AT" || param["expired_in"] != float64(12345) {
		t.Errorf("wx param: %+v", param)
	}
	if _, has := param["unionid"]; has {
		t.Errorf("wx path should not include unionid: %+v", param)
	}
}

func TestRefreshCredential_Fallback(t *testing.T) {
	c, body := loginServer(t, `{"req_0":{"code":0,"data":{"musicid":9,"musickey":"K"}}}`)
	in := &qqmusic.Credential{
		LoginType: 0, AccessToken: "AT", RefreshToken: "RT", MusicID: 9, MusicKey: "OLD",
	}
	if _, err := RefreshCredential(context.Background(), c, in); err != nil {
		t.Fatalf("err: %v", err)
	}
	var req map[string]any
	_ = json.Unmarshal([]byte(*body), &req)
	param := req["req_0"].(map[string]any)["param"].(map[string]any)
	// 默认分支同时含 access_token + str_musicid + unionid
	if _, ok := param["access_token"]; !ok {
		t.Errorf("fallback: missing access_token: %+v", param)
	}
	if _, ok := param["str_musicid"]; !ok {
		t.Errorf("fallback: missing str_musicid: %+v", param)
	}
}

func TestRefreshCredential_RejectsEmpty(t *testing.T) {
	c, _ := loginServer(t, `{"req_0":{"code":0}}`)
	if _, err := RefreshCredential(context.Background(), c, &qqmusic.Credential{}); err == nil {
		t.Errorf("expected error on empty credential")
	}
}

func TestCheckExpired_NotExpired(t *testing.T) {
	c, _ := loginServer(t, `{"req_0":{"code":0,"data":{"foo":1}}}`)
	expired, err := CheckExpired(context.Background(), c, &qqmusic.Credential{MusicKey: "K"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if expired {
		t.Errorf("expected not expired")
	}
}

func TestCheckExpired_Expired(t *testing.T) {
	c, _ := loginServer(t, `{"req_0":{"code":104401,"data":{}}}`)
	expired, err := CheckExpired(context.Background(), c, &qqmusic.Credential{MusicKey: "K"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !expired {
		t.Errorf("expected expired")
	}
}

func TestCheckExpired_OtherError(t *testing.T) {
	c, _ := loginServer(t, `{"req_0":{"code":99999,"data":{}}}`)
	_, err := CheckExpired(context.Background(), c, &qqmusic.Credential{MusicKey: "K"})
	if err == nil {
		t.Errorf("expected error")
	}
	if errors.Is(err, ErrLoginCredentialExpired) {
		t.Errorf("should not classify as expired: %v", err)
	}
}

func TestGetMobileQR_Success(t *testing.T) {
	// 构造一张 1×1 PNG 作为 base64 payload
	pngB := makeTinyPNG(t)
	b64 := base64.StdEncoding.EncodeToString(pngB)
	dataURL := "data:image/png;base64," + b64
	respBody, _ := json.Marshal(map[string]any{
		"req_0": map[string]any{
			"code": 0,
			"data": map[string]any{
				"qrcode":   dataURL,
				"qrcodeID": "QID-123",
			},
		},
	})
	c, body := loginServer(t, string(respBody))

	qr, err := GetMobileQR(context.Background(), c)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if qr.Identifier != "QID-123" || qr.MIME != "image/png" {
		t.Errorf("qr: %+v", qr)
	}
	if !strings.HasPrefix(string(qr.Image[:8]), "\x89PNG") {
		t.Errorf("decoded image not PNG: %x", qr.Image[:8])
	}
	// 校验请求里 ct/cv 来自 queryCommon
	var req map[string]any
	_ = json.Unmarshal([]byte(*body), &req)
	param := req["req_0"].(map[string]any)["param"].(map[string]any)
	if param["tmeAppID"] != "qqmusic" {
		t.Errorf("tmeAppID: %v", param["tmeAppID"])
	}
	if _, ok := param["ct"]; !ok {
		t.Errorf("missing ct in param: %+v", param)
	}
}

func TestCheckMobileQRMQTT_NotImplemented(t *testing.T) {
	c, _ := loginServer(t, `{}`)
	if err := CheckMobileQRMQTT(context.Background(), c, &MobileQR{}); !errors.Is(err, ErrMobileQRStreamNotImplemented) {
		t.Errorf("expected ErrMobileQRStreamNotImplemented, got %v", err)
	}
}

// makeTinyPNG 生成一张 1×1 的 PNG 字节流（仅用于 base64 解码端到端测试）。
func makeTinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}
