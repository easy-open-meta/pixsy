package main

import "testing"

func TestBrowseURL(t *testing.T) {
	cases := map[string]string{
		":8090":           "http://localhost:8090",
		"localhost:8090":  "http://localhost:8090",
		"0.0.0.0:8080":    "http://localhost:8080",
		"[::]:8080":       "http://localhost:8080",
		"127.0.0.1:9000":  "http://127.0.0.1:9000",
		"192.168.1.10:80": "http://192.168.1.10:80",
		"bad":             "",
	}
	for in, want := range cases {
		if got := browseURL(in); got != want {
			t.Errorf("browseURL(%q) = %q, want %q", in, got, want)
		}
	}
}
