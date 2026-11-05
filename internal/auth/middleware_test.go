package auth

import (
	"net"
	"net/http/httptest"
	"testing"
)

// proxyNets 是测试用的可信代理网段：10.0.0.0/8 + 127.0.0.0/8。
var proxyNets = mustNets("10.0.0.0/8,127.0.0.0/8")

func mustNets(raw string) []*net.IPNet {
	var nets []*net.IPNet
	for _, part := range splitComma(raw) {
		_, n, err := net.ParseCIDR(part)
		if err != nil {
			panic(err)
		}
		nets = append(nets, n)
	}
	return nets
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name   string
		xff    string
		remote string
		want   string
	}{
		// 无可信代理：完全忽略 XFF，防伪造绕过限速。
		{"no trusted proxy ignores xff", "1.2.3.4", "192.0.2.10:5555", "192.0.2.10"},
		{"no trusted proxy empty xff", "", "192.0.2.10:5555", "192.0.2.10"},
		{"no trusted proxy remote without port", "", "192.0.2.10", "192.0.2.10"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/v1/config", nil)
			r.RemoteAddr = tc.remote
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := ClientIP(r, nil); got != tc.want {
				t.Fatalf("ClientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClientIPWithTrustedProxy(t *testing.T) {
	cases := []struct {
		name   string
		xff    string
		remote string
		want   string
	}{
		// 连接来自可信代理：取从右往左第一个非可信地址。
		{"proxy appends real client", "203.0.113.9", "10.0.0.1:5000", "203.0.113.9"},
		{"chain skips trusted hops", "203.0.113.9, 10.0.0.7", "10.0.0.1:5000", "203.0.113.9"},
		// 客户端伪造的最左段不再生效——最右端才是可信代理追加的。
		{"forged left hop ignored", "6.6.6.6, 203.0.113.9", "10.0.0.1:5000", "203.0.113.9"},
		{"all hops trusted falls back", "10.0.0.9, 10.0.0.8", "10.0.0.1:5000", "10.0.0.1"},
		{"invalid xff falls back", "not-an-ip", "10.0.0.1:5555", "10.0.0.1"},
		{"empty xff", "", "10.0.0.1:5555", "10.0.0.1"},
		// 连接不来自可信代理：XFF 不可信。
		{"untrusted peer ignores xff", "1.2.3.4", "192.0.2.10:5555", "192.0.2.10"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/v1/config", nil)
			r.RemoteAddr = tc.remote
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := ClientIP(r, proxyNets); got != tc.want {
				t.Fatalf("ClientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeIP(t *testing.T) {
	cases := map[string]string{
		" 203.0.113.9 ":    "203.0.113.9",
		"203.0.113.9:8080": "203.0.113.9",
		"[2001:db8::1]:80": "2001:db8::1",
		"garbage":          "",
		"":                 "",
	}
	for in, want := range cases {
		if got := normalizeIP(in); got != want {
			t.Errorf("normalizeIP(%q) = %q, want %q", in, got, want)
		}
	}
}
