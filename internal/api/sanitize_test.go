package api

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"keeps plain name", "photo.png", "photo.png"},
		{"strips unix dirs", "../../etc/passwd", "passwd"},
		{"strips windows dirs", `C:\Users\me\a.png`, "a.png"},
		{"drops control and angle chars", "a\x00b\x1f\"<>c.jpg", "abc.jpg"},
		{"empty becomes placeholder", "   ", "image"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeFilename(tc.in); got != tc.want {
				t.Fatalf("sanitizeFilename(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// 超长多字节文件名必须截在 rune 边界上：按字节硬切会留下半个汉字，
// 写库后 JSON 编码会把它变成 U+FFFD。
func TestSanitizeFilenameTruncatesOnRuneBoundary(t *testing.T) {
	got := sanitizeFilename(strings.Repeat("图", 100))
	if len(got) > 200 {
		t.Fatalf("len = %d, want <= 200", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncated name is not valid UTF-8: %q", got)
	}
	if got == "" {
		t.Fatal("truncated name is empty")
	}
}
