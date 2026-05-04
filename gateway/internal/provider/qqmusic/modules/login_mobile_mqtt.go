package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/paho"
	gws "github.com/gorilla/websocket"
	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
)

type MobileQREvent string

const (
	MobileEventScanned     MobileQREvent = "scanned"
	MobileEventCanceled    MobileQREvent = "canceled"
	MobileEventTimeout     MobileQREvent = "timeout"
	MobileEventLoginFailed MobileQREvent = "loginFailed"
	MobileEventCookies     MobileQREvent = "cookies"
)

type MobileQRMessage struct {
	Event      MobileQREvent
	Credential *qqmusic.Credential
}

// CheckMobileQRMQTT 通过 MQTT 5.0 over WebSocket 订阅手机扫码状态。
func CheckMobileQRMQTT(ctx context.Context, c *qqmusic.Client, qr *MobileQR) (<-chan MobileQRMessage, error) {
	if qr == nil || qr.Identifier == "" {
		return nil, errors.New("qqmusic: nil/empty mobile QR")
	}

	clientID := fmt.Sprintf("%d%04d", time.Now().UnixMilli(), rand.IntN(10000))
	topic := "management.qrcode_login/" + qr.Identifier

	conn, err := dialMQTTWebSocket(ctx)
	if err != nil {
		return nil, fmt.Errorf("qqmusic: mqtt ws dial: %w", err)
	}
	slog.Info("[mqtt] websocket connected")

	ch := make(chan MobileQRMessage, 8)
	closed := make(chan struct{})

	mqttClient := paho.NewClient(paho.ClientConfig{
		Conn:     conn,
		ClientID: clientID,
		OnPublishReceived: []func(paho.PublishReceived) (bool, error){
			func(pr paho.PublishReceived) (bool, error) {
				msg := handleMobileMessage(ctx, c, qr.Identifier, pr.Packet)
				select {
				case ch <- msg:
				default:
				}
				switch msg.Event {
				case MobileEventCookies, MobileEventCanceled, MobileEventTimeout, MobileEventLoginFailed:
					select {
					case <-closed:
					default:
						close(closed)
					}
				}
				return true, nil
			},
		},
	})

	connProps := &paho.ConnectProperties{
		AuthMethod: "pass",
		User: paho.UserProperties{
			{Key: "tmeAppID", Value: "qqmusic"},
			{Key: "business", Value: "management"},
			{Key: "hashTag", Value: qr.Identifier},
			{Key: "clientTag", Value: "management.user"},
			{Key: "userID", Value: qr.Identifier},
		},
	}

	connectCtx, connectCancel := context.WithTimeout(ctx, 15*time.Second)
	defer connectCancel()

	ca, err := mqttClient.Connect(connectCtx, &paho.Connect{
		KeepAlive:  45,
		CleanStart: true,
		ClientID:   clientID,
		Properties: connProps,
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("qqmusic: mqtt connect: %w", err)
	}
	if ca.ReasonCode != 0 {
		// 处理重定向
		if (ca.ReasonCode == 0x9C || ca.ReasonCode == 0x9D) && ca.Properties != nil && ca.Properties.ServerReference != "" {
			slog.Info("[mqtt] server redirect", "ref", ca.Properties.ServerReference, "code", ca.ReasonCode)
			conn.Close()
			// 重连到重定向地址
			return retryWithRedirect(ctx, c, qr, ca.Properties.ServerReference, clientID, topic, ch, closed)
		}
		conn.Close()
		return nil, fmt.Errorf("qqmusic: mqtt connect rejected: code=%d", ca.ReasonCode)
	}
	slog.Info("[mqtt] connected ok")

	subCtx, subCancel := context.WithTimeout(ctx, 10*time.Second)
	defer subCancel()

	sa, err := mqttClient.Subscribe(subCtx, &paho.Subscribe{
		Subscriptions: []paho.SubscribeOptions{{Topic: topic, QoS: 0}},
		Properties: &paho.SubscribeProperties{
			User: paho.UserProperties{
				{Key: "authorization", Value: "tmelogin"},
				{Key: "pubsub", Value: "unicast"},
			},
		},
	})
	if err != nil {
		mqttClient.Disconnect(&paho.Disconnect{})
		conn.Close()
		return nil, fmt.Errorf("qqmusic: mqtt subscribe: %w", err)
	}
	if len(sa.Reasons) > 0 && sa.Reasons[0] >= 0x80 {
		mqttClient.Disconnect(&paho.Disconnect{})
		conn.Close()
		return nil, fmt.Errorf("qqmusic: mqtt subscribe rejected: code=%d", sa.Reasons[0])
	}
	slog.Info("[mqtt] subscribed", "topic", topic)

	go func() {
		select {
		case <-ctx.Done():
		case <-closed:
		}
		mqttClient.Disconnect(&paho.Disconnect{})
		conn.Close()
		select {
		case <-closed:
		default:
			close(closed)
		}
		close(ch)
	}()

	return ch, nil
}

func retryWithRedirect(ctx context.Context, c *qqmusic.Client, qr *MobileQR, serverRef, clientID, topic string, ch chan MobileQRMessage, closed chan struct{}) (<-chan MobileQRMessage, error) {
	// 构建新路径
	newPath := "/ws/handshake/" + serverRef
	slog.Info("[mqtt] redirecting", "path", newPath)

	conn, err := dialMQTTWebSocketPath(ctx, newPath)
	if err != nil {
		return nil, fmt.Errorf("qqmusic: mqtt redirect dial: %w", err)
	}

	mqttClient := paho.NewClient(paho.ClientConfig{
		Conn:     conn,
		ClientID: clientID,
		OnPublishReceived: []func(paho.PublishReceived) (bool, error){
			func(pr paho.PublishReceived) (bool, error) {
				msg := handleMobileMessage(ctx, c, qr.Identifier, pr.Packet)
				select {
				case ch <- msg:
				default:
				}
				switch msg.Event {
				case MobileEventCookies, MobileEventCanceled, MobileEventTimeout, MobileEventLoginFailed:
					select {
					case <-closed:
					default:
						close(closed)
					}
				}
				return true, nil
			},
		},
	})

	connCtx, connCancel := context.WithTimeout(ctx, 15*time.Second)
	defer connCancel()

	ca, err := mqttClient.Connect(connCtx, &paho.Connect{
		KeepAlive:  45,
		CleanStart: true,
		ClientID:   clientID,
		Properties: &paho.ConnectProperties{
			AuthMethod: "pass",
			User: paho.UserProperties{
				{Key: "tmeAppID", Value: "qqmusic"},
				{Key: "business", Value: "management"},
				{Key: "hashTag", Value: qr.Identifier},
				{Key: "clientTag", Value: "management.user"},
				{Key: "userID", Value: qr.Identifier},
			},
		},
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("qqmusic: mqtt redirect connect: %w", err)
	}
	if ca.ReasonCode != 0 {
		conn.Close()
		return nil, fmt.Errorf("qqmusic: mqtt redirect connect rejected: code=%d", ca.ReasonCode)
	}

	subCtx, subCancel := context.WithTimeout(ctx, 10*time.Second)
	defer subCancel()

	_, err = mqttClient.Subscribe(subCtx, &paho.Subscribe{
		Subscriptions: []paho.SubscribeOptions{{Topic: topic, QoS: 0}},
		Properties: &paho.SubscribeProperties{
			User: paho.UserProperties{
				{Key: "authorization", Value: "tmelogin"},
				{Key: "pubsub", Value: "unicast"},
			},
		},
	})
	if err != nil {
		mqttClient.Disconnect(&paho.Disconnect{})
		conn.Close()
		return nil, fmt.Errorf("qqmusic: mqtt redirect subscribe: %w", err)
	}

	go func() {
		select {
		case <-ctx.Done():
		case <-closed:
		}
		mqttClient.Disconnect(&paho.Disconnect{})
		conn.Close()
		select {
		case <-closed:
		default:
			close(closed)
		}
		close(ch)
	}()

	return ch, nil
}

func handleMobileMessage(ctx context.Context, c *qqmusic.Client, qrcodeID string, pub *paho.Publish) MobileQRMessage {
	eventType := ""
	if pub.Properties != nil {
		for _, p := range pub.Properties.User {
			if p.Key == "type" {
				eventType = p.Value
				break
			}
		}
	}
	slog.Info("[mqtt] message", "type", eventType)

	switch MobileQREvent(eventType) {
	case MobileEventScanned:
		return MobileQRMessage{Event: MobileEventScanned}
	case MobileEventCanceled:
		return MobileQRMessage{Event: MobileEventCanceled}
	case MobileEventTimeout:
		return MobileQRMessage{Event: MobileEventTimeout}
	case MobileEventLoginFailed:
		return MobileQRMessage{Event: MobileEventLoginFailed}
	case MobileEventCookies:
		cred, err := extractMobileCredential(ctx, c, qrcodeID, pub.Payload)
		if err != nil {
			slog.Error("[mqtt] extract credential failed", "err", err)
			return MobileQRMessage{Event: MobileEventLoginFailed}
		}
		return MobileQRMessage{Event: MobileEventCookies, Credential: cred}
	default:
		return MobileQRMessage{Event: MobileEventLoginFailed}
	}
}

func extractMobileCredential(ctx context.Context, c *qqmusic.Client, qrcodeID string, payload []byte) (*qqmusic.Credential, error) {
	var msg struct {
		Cookies map[string]json.RawMessage `json:"cookies"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return nil, err
	}

	getCookieVal := func(name string) string {
		raw, ok := msg.Cookies[name]
		if !ok {
			return ""
		}
		var obj struct {
			Value string `json:"value"`
		}
		if json.Unmarshal(raw, &obj) == nil && obj.Value != "" {
			return obj.Value
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}

	uin := getCookieVal("qqmusic_uin")
	key := getCookieVal("qqmusic_key")
	musicID, _ := strconv.ParseInt(uin, 10, 64)

	data, err := callJSONLogin(ctx, c, "music.login.LoginServer", "Login",
		map[string]any{
			"musicid":  musicID,
			"qrCodeID": qrcodeID,
			"token":    key,
		},
		qqmusic.MusicuOptions{
			Comm: map[string]any{"tmeLoginType": 6},
		})
	if err != nil {
		return nil, err
	}
	return decodeCredential(data)
}

// --- WebSocket → net.Conn adapter ---

func dialMQTTWebSocket(ctx context.Context) (net.Conn, error) {
	return dialMQTTWebSocketPath(ctx, "/ws/handshake")
}

func dialMQTTWebSocketPath(ctx context.Context, path string) (net.Conn, error) {
	dialer := gws.Dialer{
		Subprotocols:    []string{"mqtt"},
		HandshakeTimeout: 10 * time.Second,
	}
	header := http.Header{
		"Origin":     {"https://y.qq.com"},
		"Referer":    {"https://y.qq.com/"},
		"User-Agent": {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"},
	}
	url := "wss://mu.y.qq.com:443" + path
	slog.Info("[mqtt] dialing websocket", "url", url)

	wsConn, resp, err := dialer.DialContext(ctx, url, header)
	if err != nil {
		if resp != nil {
			slog.Error("[mqtt] ws dial failed", "status", resp.StatusCode, "err", err)
		}
		return nil, fmt.Errorf("ws dial %s: %w", url, err)
	}
	slog.Info("[mqtt] ws dial ok", "url", url)
	return newWSNetConn(wsConn), nil
}

// wsNetConn 把 gorilla/websocket.Conn 适配为 net.Conn。
// paho.golang 需要 net.Conn 做 MQTT 帧读写。
type wsNetConn struct {
	ws     *gws.Conn
	mu     sync.Mutex
	reader io.Reader
}

func newWSNetConn(ws *gws.Conn) *wsNetConn {
	return &wsNetConn{ws: ws}
}

func (c *wsNetConn) Read(p []byte) (int, error) {
	for {
		if c.reader != nil {
			n, err := c.reader.Read(p)
			if err == io.EOF {
				c.reader = nil
				if n > 0 {
					return n, nil
				}
				continue
			}
			return n, err
		}
		_, msg, err := c.ws.ReadMessage()
		if err != nil {
			return 0, err
		}
		c.reader = bytes.NewReader(msg)
	}
}

func (c *wsNetConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.ws.WriteMessage(gws.BinaryMessage, p)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsNetConn) Close() error                       { return c.ws.Close() }
func (c *wsNetConn) LocalAddr() net.Addr                 { return c.ws.LocalAddr() }
func (c *wsNetConn) RemoteAddr() net.Addr                { return c.ws.RemoteAddr() }
func (c *wsNetConn) SetDeadline(t time.Time) error       { return nil }
func (c *wsNetConn) SetReadDeadline(t time.Time) error   { return nil }
func (c *wsNetConn) SetWriteDeadline(t time.Time) error  { return nil }
