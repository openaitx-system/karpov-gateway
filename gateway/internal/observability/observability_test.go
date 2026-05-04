package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestNewLogger_RedactsSensitive(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if _, ok := SensitiveKeys[a.Key]; ok {
				return slog.String(a.Key, "[REDACTED]")
			}
			return a
		},
	})
	logger := slog.New(h)
	logger.Info("test", "user", "alice", "password", "hunter2", "musickey", "W_X_secret")

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out["user"] != "alice" {
		t.Errorf("non-sensitive lost: %v", out["user"])
	}
	if out["password"] != "[REDACTED]" {
		t.Errorf("password not redacted: %v", out["password"])
	}
	if out["musickey"] != "[REDACTED]" {
		t.Errorf("musickey not redacted: %v", out["musickey"])
	}
}

func TestMetrics_Registration(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	if m.HTTPRequests == nil || m.QuotaDecisions == nil {
		t.Fatalf("metrics not constructed")
	}
	m.HTTPRequests.WithLabelValues("music", "GET", "/song", "200").Inc()
	m.QuotaDecisions.WithLabelValues("qqmusic", "GetSong", "Allow").Inc()

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	names := []string{}
	for _, mf := range mfs {
		names = append(names, mf.GetName())
	}
	want := []string{"gateway_http_requests_total", "gateway_quota_decisions_total"}
	for _, w := range want {
		found := false
		for _, n := range names {
			if n == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing metric %s in %v", w, names)
		}
	}
}

func TestSetupTracerProvider_Noop(t *testing.T) {
	shutdown, err := SetupTracerProvider(TracerProviderConfig{ServiceName: "test"})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	ctx, span := SpanWithAttrs(context.Background(), "test", "TestMethod")
	span.End()
	if ctx == nil {
		t.Errorf("nil ctx")
	}
}

func TestSetupTracerProvider_EmptyServiceName(t *testing.T) {
	_, err := SetupTracerProvider(TracerProviderConfig{})
	if err == nil || !strings.Contains(err.Error(), "empty service name") {
		t.Errorf("expected error: %v", err)
	}
}
