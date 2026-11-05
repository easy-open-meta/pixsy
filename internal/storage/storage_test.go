package storage_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/pixsy/pixsy/internal/storage"
)

// staticWebp 是一张 1×1 无损静态 WebP（VP8L）。
var staticWebp = mustBase64Decode("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")

func mustBase64Decode(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func openStore(t *testing.T) *storage.Store {
	t.Helper()
	s, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return s
}

// chunkPayload 从 RIFF/WEBP 容器里取出指定 chunk 的载荷。
func chunkPayload(t *testing.T, raw []byte, fourcc string) []byte {
	t.Helper()
	if string(raw[:4]) != "RIFF" || string(raw[8:12]) != "WEBP" {
		t.Fatalf("not a webp riff container")
	}
	rest := raw[12:]
	for len(rest) >= 8 {
		id := string(rest[:4])
		size := binary.LittleEndian.Uint32(rest[4:8])
		if id == fourcc {
			return rest[8 : 8+size]
		}
		rest = rest[8+size+size%2:] // chunk 按 2 字节对齐
	}
	t.Fatalf("chunk %s not found", fourcc)
	return nil
}

// riffChunk 组装单个 RIFF chunk（按 2 字节对齐补零）。
func riffChunk(fourcc string, payload []byte) []byte {
	out := make([]byte, 0, 8+len(payload)+1)
	out = append(out, fourcc...)
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], uint32(len(payload)))
	out = append(out, size[:]...)
	out = append(out, payload...)
	if len(out)%2 != 0 {
		out = append(out, 0)
	}
	return out
}

// riffWebp 把若干 chunk 组装成 RIFF/WEBP 容器。
func riffWebp(chunks ...[]byte) []byte {
	var body bytes.Buffer
	body.WriteString("WEBP")
	for _, c := range chunks {
		body.Write(c)
	}
	var out bytes.Buffer
	out.WriteString("RIFF")
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], uint32(body.Len()))
	out.Write(size[:])
	out.Write(body.Bytes())
	return out.Bytes()
}

// animatedWebp 手工拼装最小动画 WebP：VP8X(动画位) + ANIM + ANMF(内嵌 1×1 VP8L 帧)。
// x/image/webp 的 DecodeConfig 只读 VP8X 画布头即成功，完整解码不支持动画帧会失败。
func animatedWebp(t *testing.T) []byte {
	t.Helper()
	frame := chunkPayload(t, staticWebp, "VP8L")
	// VP8X 载荷 10 字节：flags(动画位 0x02) + 3 字节保留 + 画布宽高各 3 字节（值-1，1×1 全 0）
	vp8x := append([]byte{0x02, 0, 0, 0}, make([]byte, 6)...)
	anmf := make([]byte, 0, 16+len(frame))
	anmf = append(anmf, 0, 0, 0) // frame x
	anmf = append(anmf, 0, 0, 0) // frame y
	anmf = append(anmf, 0, 0, 0) // width-1
	anmf = append(anmf, 0, 0, 0) // height-1
	anmf = append(anmf, 0, 0, 0) // duration
	anmf = append(anmf, 0)       // flags
	anmf = append(anmf, riffChunk("VP8L", frame)...)
	return riffWebp(
		riffChunk("VP8X", vp8x),
		riffChunk("ANIM", make([]byte, 6)), // 背景色 4B + 循环次数 2B
		riffChunk("ANMF", anmf),
	)
}

// 动画 WebP 原图合法可直出，但缩略图无法解码：上传必须成功落盘，只跳过缩略图。
func TestSaveAnimatedWebpKeepsUpload(t *testing.T) {
	s := openStore(t)
	data := animatedWebp(t)

	res, err := s.Save(context.Background(), bytes.NewReader(data), 1<<20)
	if err != nil {
		t.Fatalf("Save animated webp: %v", err)
	}
	if res.Ext != "webp" {
		t.Fatalf("ext = %q, want webp", res.Ext)
	}
	if _, err := os.Stat(s.Path(res.Hash, res.Ext)); err != nil {
		t.Fatalf("original not committed: %v", err)
	}
	if _, err := os.Stat(s.ThumbPath(res.Hash)); !os.IsNotExist(err) {
		t.Fatalf("thumb should be absent, got err = %v", err)
	}

	// 重复上传走去重路径，同样必须成功。
	again, err := s.Save(context.Background(), bytes.NewReader(data), 1<<20)
	if err != nil {
		t.Fatalf("Save dedup animated webp: %v", err)
	}
	if !again.Deduped {
		t.Fatal("second Save should be deduped")
	}
	if again.Hash != res.Hash {
		t.Fatalf("dedup hash = %s, want %s", again.Hash, res.Hash)
	}
}

// icoBytes 组装一个 ICO：ICONDIR + 单帧条目 + 载荷。
// Save 只读目录头取宽高，不解码帧，所以载荷用现成的静态 WebP 填充即可。
func icoBytes(width, height byte, payload []byte) []byte {
	dir := []byte{0, 0, 1, 0, 1, 0} // reserved=0, type=1(icon), count=1
	entry := make([]byte, 16)
	entry[0], entry[1] = width, height                             // 0 表示 256
	binary.LittleEndian.PutUint16(entry[4:], 1)                    // planes
	binary.LittleEndian.PutUint16(entry[6:], 32)                   // bit count
	binary.LittleEndian.PutUint32(entry[8:], uint32(len(payload))) // bytes in resource
	binary.LittleEndian.PutUint32(entry[12:], 22)                  // 图像数据偏移
	return append(append(dir, entry...), payload...)
}

func TestSaveICO(t *testing.T) {
	s := openStore(t)
	res, err := s.Save(context.Background(), bytes.NewReader(icoBytes(32, 32, staticWebp)), 1<<20)
	if err != nil {
		t.Fatalf("Save ico: %v", err)
	}
	if res.Ext != "ico" || res.Mime != "image/x-icon" {
		t.Fatalf("ext/mime = %q/%q, want ico/image/x-icon", res.Ext, res.Mime)
	}
	if res.Width != 32 || res.Height != 32 {
		t.Fatalf("dims = %d×%d, want 32×32", res.Width, res.Height)
	}
	if _, err := os.Stat(s.Path(res.Hash, res.Ext)); err != nil {
		t.Fatalf("original not committed: %v", err)
	}
	// ICO 无法解码，不生成缩略图，前端会回退到原图。
	if _, err := os.Stat(s.ThumbPath(res.Hash)); !os.IsNotExist(err) {
		t.Fatalf("thumb should be absent, got err = %v", err)
	}
}

func TestSaveICODimsZeroMeans256(t *testing.T) {
	s := openStore(t)
	res, err := s.Save(context.Background(), bytes.NewReader(icoBytes(0, 0, staticWebp)), 1<<20)
	if err != nil {
		t.Fatalf("Save ico: %v", err)
	}
	if res.Width != 256 || res.Height != 256 {
		t.Fatalf("dims = %d×%d, want 256×256", res.Width, res.Height)
	}
}

func TestSaveCursorRejected(t *testing.T) {
	s := openStore(t)
	raw := icoBytes(32, 32, staticWebp)
	raw[2] = 2 // type=2 是光标（.cur），不在白名单内
	_, err := s.Save(context.Background(), bytes.NewReader(raw), 1<<20)
	if !errors.Is(err, storage.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

// Path 对异常短的 hash 不能切片越界 panic（防御：正常 hash 恒为 64 位 hex）。
func TestPathShortHashDoesNotPanic(t *testing.T) {
	s := openStore(t)
	for _, hash := range []string{"", "ab", "abc", strings.Repeat("a", 64)} {
		p := s.Path(hash, "png")
		if !strings.Contains(p, hash+".png") {
			t.Fatalf("Path(%q) = %q, want it to end with %q", hash, p, hash+".png")
		}
	}
}
