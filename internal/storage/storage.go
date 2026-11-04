// Package storage 负责上传文件的校验与内容寻址落盘（§3.4）。
package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/disintegration/imaging"

	// 解码器注册：DecodeConfig 只读文件头，WebP 也只需解析容器头。
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

// ThumbMaxSide 是缩略图最长边（§3.4 step 5）。
const ThumbMaxSide = 350

// thumbQuality 是缩略图 JPEG 质量。
const thumbQuality = 82

var (
	// ErrTooLarge 表示读取字节数超过单文件上限。
	ErrTooLarge = errors.New("file exceeds the upload limit")
	// ErrUnsupported 表示内容不在白名单格式内（魔数判定，不看扩展名）。
	ErrUnsupported = errors.New("unsupported image format")
)

// format 是魔数识别结果；扩展名只用于展示与 URL，不参与安全判定。
type format struct {
	ext  string
	mime string
}

// knownFormats 既是白名单也是 ext→MIME 映射。
var knownFormats = map[string]string{
	"jpg":  "image/jpeg",
	"png":  "image/png",
	"gif":  "image/gif",
	"webp": "image/webp",
}

// MimeForExt 返回已知扩展名对应的 MIME。
func MimeForExt(ext string) (string, bool) {
	m, ok := knownFormats[ext]
	return m, ok
}

// Result 描述一次落盘的结果。
type Result struct {
	Hash    string
	Ext     string
	Mime    string
	Size    int64
	Width   int
	Height  int
	Deduped bool // 磁盘上已有同 hash 文件，本次写入被丢弃（秒传）
}

// Store 管理 files/ 与 thumbs/ 两个目录。
type Store struct {
	filesDir  string
	thumbsDir string
}

// Open 准备数据目录下的原图与缩略图目录。
func Open(dataDir string) (*Store, error) {
	s := &Store{
		filesDir:  filepath.Join(dataDir, "files"),
		thumbsDir: filepath.Join(dataDir, "thumbs"),
	}
	for _, dir := range []string{s.filesDir, s.thumbsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create storage dir: %w", err)
		}
	}
	return s, nil
}

// Path 返回原图路径：files/ab/cd/<hash>.<ext>（§3.4 step 4）。
func (s *Store) Path(hash, ext string) string {
	return filepath.Join(s.filesDir, hash[:2], hash[2:4], hash+"."+ext)
}

// ThumbPath 返回缩略图路径：thumbs/<hash>.jpg。
func (s *Store) ThumbPath(hash string) string {
	return filepath.Join(s.thumbsDir, hash+".jpg")
}

// Save 校验并落盘一个上传流，返回元信息。maxBytes 是单文件硬上限。
func (s *Store) Save(ctx context.Context, r io.Reader, maxBytes int64) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maxBytes <= 0 {
		return nil, ErrTooLarge
	}

	tmp, err := os.CreateTemp(s.filesDir, "upload-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath) // 幂等：成功 rename 后这里只是空操作
	}()

	// 先读前 32 字节判定真实格式（§3.4 step 3）。
	head := make([]byte, 32)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read upload head: %w", err)
	}
	head = head[:n]
	f, ok := sniff(head)
	if !ok {
		return nil, ErrUnsupported
	}

	// LimitReader 取上限+1：超限说明流比 Content-Length 更长（§3.4 step 2）。
	h := sha256.New()
	body := io.LimitReader(io.MultiReader(bytes.NewReader(head), r), maxBytes+1)
	written, err := io.Copy(io.MultiWriter(tmp, h), body)
	if err != nil {
		return nil, fmt.Errorf("write upload: %w", err)
	}
	if written > maxBytes {
		return nil, ErrTooLarge
	}

	// 只解码头取宽高：GIF 不整帧解码，避免大动图内存放大（§3.4）。
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind temp file: %w", err)
	}
	cfg, _, err := image.DecodeConfig(tmp)
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, ErrUnsupported
	}

	hash := hex.EncodeToString(h.Sum(nil))
	res := &Result{
		Hash:   hash,
		Ext:    f.ext,
		Mime:   f.mime,
		Size:   written,
		Width:  cfg.Width,
		Height: cfg.Height,
	}

	dst := s.Path(hash, f.ext)
	if _, statErr := os.Stat(dst); statErr == nil {
		res.Deduped = true
	} else {
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, fmt.Errorf("create hash dir: %w", err)
		}
		if err := tmp.Close(); err != nil {
			return nil, fmt.Errorf("close temp file: %w", err)
		}
		if err := os.Rename(tmpPath, dst); err != nil {
			return nil, fmt.Errorf("commit upload file: %w", err)
		}
	}

	// 缩略图尽力而为：原图此时已可用，解码失败（如动画 WebP）只跳过缩略图，
	// 不能让已落盘的上传整体失败；去重路径顺带补建历史缺失的缩略图。
	if err := s.ensureThumb(dst, hash); err != nil {
		slog.Warn("thumbnail skipped", "hash", hash, "err", err)
	}
	return res, nil
}

// OpenFile 打开原图（服务端直出用）。
func (s *Store) OpenFile(hash, ext string) (*os.File, os.FileInfo, error) {
	return openWithInfo(s.Path(hash, ext))
}

// OpenThumb 打开缩略图。
func (s *Store) OpenThumb(hash string) (*os.File, os.FileInfo, error) {
	return openWithInfo(s.ThumbPath(hash))
}

func openWithInfo(name string) (*os.File, os.FileInfo, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if info.IsDir() {
		_ = f.Close()
		return nil, nil, os.ErrNotExist
	}
	return f, info, nil
}

// Remove 删除原图与缩略图；文件不存在不算错误。
func (s *Store) Remove(hash, ext string) error {
	var firstErr error
	for _, name := range []string{s.Path(hash, ext), s.ThumbPath(hash)} {
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ensureThumb 生成缩略图（最长边 350px、JPEG 质量 82）；已存在则跳过。
func (s *Store) ensureThumb(src, hash string) error {
	dst := s.ThumbPath(hash)
	if _, err := os.Stat(dst); err == nil {
		return nil
	}

	img, err := imaging.Open(src)
	if err != nil {
		return fmt.Errorf("decode image for thumb: %w", err)
	}
	thumb := imaging.Fit(img, ThumbMaxSide, ThumbMaxSide, imaging.Lanczos)

	tmp, err := os.CreateTemp(s.thumbsDir, "thumb-*.tmp")
	if err != nil {
		return fmt.Errorf("create thumb temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := imaging.Encode(tmp, thumb, imaging.JPEG, imaging.JPEGQuality(thumbQuality)); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("encode thumb: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close thumb: %w", err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("commit thumb: %w", err)
	}
	return nil
}

// sniff 按魔数判定白名单格式；改后缀的文本文件在这里被拒绝（§3.4 step 3）。
func sniff(head []byte) (format, bool) {
	switch {
	case len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF:
		return format{ext: "jpg", mime: "image/jpeg"}, true
	case len(head) >= 8 && bytes.HasPrefix(head, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return format{ext: "png", mime: "image/png"}, true
	case len(head) >= 6 && (string(head[:6]) == "GIF87a" || string(head[:6]) == "GIF89a"):
		return format{ext: "gif", mime: "image/gif"}, true
	case len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		return format{ext: "webp", mime: "image/webp"}, true
	}
	return format{}, false
}
