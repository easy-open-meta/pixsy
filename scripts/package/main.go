// 发行包打包器：把 bin/ 下的交叉编译产物打成可直接分发的安装包到 deploy/。
//
// 用法（通常由 `make package` 调用）：
//
//	go run ./scripts/package               # 版本号取 git describe
//	go run ./scripts/package -version v1.2.3
//	go run ./scripts/package -out dist      # 自定义输出目录
//
// 产物：
//
//	deploy/pixsy_<版本>_linux_amd64.tar.gz
//	deploy/pixsy_<版本>_linux_arm64.tar.gz
//	deploy/pixsy_<版本>_windows_amd64.zip
//
// 用 Go 标准库打包：zip 条目统一正斜杠路径（Windows 上的 Compress-Archive
// 会写反斜杠，Linux 解压会出错），并保留可执行权限位。
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// 每个包的公共附带文件
var extras = []string{"README.md", "LICENSE", "pixsy.json.example"}

func main() {
	version := flag.String("version", "", "版本号（默认取 git describe --tags --always）")
	outDir := flag.String("out", "deploy", "输出目录")
	binDir := flag.String("bin", "bin", "交叉编译产物目录")
	flag.Parse()

	ver := strings.TrimSpace(*version)
	if ver == "" {
		ver = gitVersion()
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fail("创建输出目录失败: %v", err)
	}

	linuxAmd64, err := buildLinux(*binDir, *outDir, ver, "amd64")
	if err != nil {
		fail("%v", err)
	}
	linuxArm64, err := buildLinux(*binDir, *outDir, ver, "arm64")
	if err != nil {
		fail("%v", err)
	}
	win, err := buildWindows(*binDir, *outDir, ver)
	if err != nil {
		fail("%v", err)
	}

	for _, f := range []string{linuxAmd64, linuxArm64, win} {
		fmt.Printf("打包完成: %s\n", f)
	}
}

// buildLinux 生成 tar.gz：二进制（保留 0755）、systemd unit、说明文件。
func buildLinux(binDir, outDir, ver, arch string) (string, error) {
	src := filepath.Join(binDir, "pixsy-linux-"+arch)
	if _, err := os.Stat(src); err != nil {
		return "", fmt.Errorf("缺少产物 %s（先执行 make linux）", src)
	}
	name := fmt.Sprintf("pixsy_%s_linux_%s.tar.gz", ver, arch)
	dst := filepath.Join(outDir, name)

	f, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	// 目录
	if err := tw.WriteHeader(&tar.Header{
		Name: "pixsy/", Mode: 0o755, Typeflag: tar.TypeDir, ModTime: time.Now(),
	}); err != nil {
		return "", err
	}
	// 二进制
	if err := writeTarFile(tw, "pixsy/pixsy", src, 0o755); err != nil {
		return "", err
	}
	// systemd unit
	if err := writeTarFile(tw, "pixsy/pixsy.service", filepath.Join("deploy", "pixsy.service"), 0o644); err != nil {
		return "", err
	}
	// 公共文件
	for _, e := range extras {
		if err := writeTarFile(tw, "pixsy/"+e, e, 0o644); err != nil {
			return "", err
		}
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	return dst, nil
}

// buildWindows 生成 zip：exe + 说明文件，条目统一正斜杠。
func buildWindows(binDir, outDir, ver string) (string, error) {
	src := filepath.Join(binDir, "pixsy-windows-amd64.exe")
	if _, err := os.Stat(src); err != nil {
		return "", fmt.Errorf("缺少产物 %s（先执行 make windows）", src)
	}
	name := fmt.Sprintf("pixsy_%s_windows_amd64.zip", ver)
	dst := filepath.Join(outDir, name)

	f, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	if err := writeZipFile(zw, "pixsy/pixsy.exe", src); err != nil {
		return "", err
	}
	for _, e := range extras {
		if err := writeZipFile(zw, "pixsy/"+e, e); err != nil {
			return "", err
		}
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return dst, nil
}

func writeTarFile(tw *tar.Writer, name, src string, mode int64) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("读取 %s 失败: %w", src, err)
	}
	h := &tar.Header{
		Name: path.Clean(name), Size: info.Size(), Mode: mode,
		ModTime: time.Now(), Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	_, err = io.Copy(tw, in)
	return err
}

func writeZipFile(zw *zip.Writer, name, src string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("读取 %s 失败: %w", src, err)
	}
	h, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	h.Name = path.Clean(name) // 统一正斜杠，跨平台解压安全
	h.Method = zip.Deflate
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	_, err = io.Copy(w, in)
	return err
}

// gitVersion 取版本号，取不到时用 dev。
func gitVersion() string {
	out, err := exec.Command("git", "describe", "--tags", "--always").Output()
	if err != nil {
		return "dev"
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		return "dev"
	}
	return v
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "错误: "+format+"\n", args...)
	os.Exit(1)
}
