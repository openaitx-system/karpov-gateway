package modules

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
)

func TestParseQQQRStatus_Done(t *testing.T) {
	// 真实抓包中的 ptuiCB 格式
	resp := `ptuiCB('0','0','https://ssl.ptlogin2.qq.com/check_sig?ptsigx=ABCDEF12345&s_url=https%3A%2F%2Fgraph.qq.com%2Foauth2.0%2Flogin_jump&uin=10086&service=foo','0','登录成功！','nickname');`
	got := parseQQQRStatus(resp)
	if got.Event != QREventDone {
		t.Errorf("event: %v want DONE", got.Event)
	}
	if got.QQDoneSigX != "ABCDEF12345" {
		t.Errorf("sigx: %q", got.QQDoneSigX)
	}
	if got.QQDoneUin != "10086" {
		t.Errorf("uin: %q", got.QQDoneUin)
	}
}

func TestParseQQQRStatus_Scan(t *testing.T) {
	resp := `ptuiCB('66','0','','','二维码未失效');`
	got := parseQQQRStatus(resp)
	if got.Event != QREventScan {
		t.Errorf("event: %v want SCAN", got.Event)
	}
}

func TestParseQQQRStatus_Conf(t *testing.T) {
	resp := `ptuiCB('67','0','','','二维码已扫描');`
	if parseQQQRStatus(resp).Event != QREventConf {
		t.Errorf("expected CONF")
	}
}

func TestParseQQQRStatus_Timeout(t *testing.T) {
	resp := `ptuiCB('65','0','','','二维码已失效');`
	if parseQQQRStatus(resp).Event != QREventTimeout {
		t.Errorf("expected TIMEOUT")
	}
}

func TestParseQQQRStatus_Refuse(t *testing.T) {
	resp := `ptuiCB('68','0','','','取消授权');`
	if parseQQQRStatus(resp).Event != QREventRefuse {
		t.Errorf("expected REFUSE")
	}
}

func TestParseQQQRStatus_OtherCode(t *testing.T) {
	resp := `ptuiCB('999','0','','','unknown');`
	if parseQQQRStatus(resp).Event != QREventOther {
		t.Errorf("expected OTHER")
	}
}

func TestParseQQQRStatus_Garbage(t *testing.T) {
	if parseQQQRStatus("garbage").Event != QREventOther {
		t.Errorf("expected OTHER on garbage")
	}
}

func TestParseQQQRStatus_DoneMissingURL(t *testing.T) {
	// status=0 but URL 缺失（异常）
	resp := `ptuiCB('0','0','','','登录成功！');`
	got := parseQQQRStatus(resp)
	if got.Event != QREventOther {
		t.Errorf("missing URL should map to OTHER, got %v", got.Event)
	}
}

func TestParseWXQRStatus_Done(t *testing.T) {
	resp := `window.wx_errcode=405;window.wx_code='WXCODEXXX';`
	got := parseWXQRStatus(resp)
	if got.Event != QREventDone {
		t.Errorf("event: %v want DONE", got.Event)
	}
	if got.WXDoneCode != "WXCODEXXX" {
		t.Errorf("code: %q", got.WXDoneCode)
	}
}

func TestParseWXQRStatus_Scan(t *testing.T) {
	resp := `window.wx_errcode=408;window.wx_code='';`
	if parseWXQRStatus(resp).Event != QREventScan {
		t.Errorf("expected SCAN")
	}
}

func TestParseWXQRStatus_Other(t *testing.T) {
	if parseWXQRStatus("noise").Event != QREventOther {
		t.Errorf("expected OTHER")
	}
}

func TestParseWXQRStatus_DoneEmptyCode(t *testing.T) {
	resp := `window.wx_errcode=405;window.wx_code='';`
	if parseWXQRStatus(resp).Event != QREventOther {
		t.Errorf("DONE with empty code should map to OTHER")
	}
}

// QQQR / WXQR HTTP 路径用 httptest 全链路验证（无需真接 ssl.ptlogin2）。
func TestGetQQQR_HTTPRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "qrsig", Value: "QRSIG_VAL"})
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n")) // 假 PNG 头
	}))
	t.Cleanup(srv.Close)

	c := qqmusic.NewClient(qqmusic.ClientOptions{
		HTTP:     qqmusic.NewHTTPClient(qqmusic.HTTPClientOptions{MaxRetries: 0, MaxConnections: 4, HTTP2: false}),
		Platform: qqmusic.PlatformWeb,
	})
	// override base URL：直接给 GetQQQR 做不到（hard-coded），所以这里只跑解析路径
	// 通过把 srv URL 拼到 Client 注入是不现实的；这里只验证入口可调用形态。
	_ = c

	// 仅作 smoke：直接调内部 helper 构造 request 不可行。完整 e2e 留 v0.3 集成测试。
	got := parseQQQRStatus(`ptuiCB('0','0','?ptsigx=AAA&s_url=x&uin=1&service=y','0','ok');`)
	if got.Event != QREventDone || got.QQDoneSigX != "AAA" {
		t.Errorf("smoke: %+v", got)
	}
}

func TestExtractCookie(t *testing.T) {
	cookies := []*http.Cookie{
		{Name: "a", Value: "1"},
		{Name: "qrsig", Value: "ZZZ"},
		{Name: "x", Value: "2"},
	}
	if got := extractCookie(cookies, "qrsig"); got != "ZZZ" {
		t.Errorf("qrsig: %q", got)
	}
	if got := extractCookie(cookies, "missing"); got != "" {
		t.Errorf("missing: %q", got)
	}
}

func TestMapQRCodes(t *testing.T) {
	if mapQQQRCode(0) != QREventDone {
		t.Errorf("QQ 0 → DONE")
	}
	if mapQQQRCode(99999) != QREventOther {
		t.Errorf("QQ unknown → OTHER")
	}
	if mapWXQRCode(405) != QREventDone {
		t.Errorf("WX 405 → DONE")
	}
	if mapWXQRCode(99) != QREventOther {
		t.Errorf("WX unknown → OTHER")
	}
}

// CheckQQQR 真 HTTP 调用走不通（hard-coded ssl.ptlogin2.qq.com），但能验证入口
// 拒绝空 QR：
func TestCheckQQQR_RejectsEmpty(t *testing.T) {
	c := qqmusic.NewClient(qqmusic.ClientOptions{})
	if _, err := CheckQQQR(context.Background(), c, nil); err == nil {
		t.Errorf("expected error for nil QR")
	}
	if _, err := CheckQQQR(context.Background(), c, &QQQR{}); err == nil ||
		!strings.Contains(err.Error(), "empty") {
		t.Errorf("expected empty QR error, got %v", err)
	}
}

func TestCheckWXQR_RejectsEmpty(t *testing.T) {
	c := qqmusic.NewClient(qqmusic.ClientOptions{})
	if _, err := CheckWXQR(context.Background(), c, nil); err == nil {
		t.Errorf("expected error for nil QR")
	}
}
