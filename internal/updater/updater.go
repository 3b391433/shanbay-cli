// Package updater implements the `sb update` command and the daily
// update-check notice printed at startup.
//
// 释放规则(来自 .github/workflows/release.yml):
//   - 资产命名 sb-<GOOS>-<GOARCH>.tar.gz
//   - tar 内单一文件名同资产 basename(如 sb-linux-amd64)
//
// 每日检查通过 ~/.config/shanbay-cli/update-check.json 缓存最近一次拉取到的
// 最新 tag,同步读取即时返回,若今天还没刷新过则后台 goroutine 拉一次(不阻塞
// 主命令)。发现缓存的 latest 比当前版本新才打印一行提醒。
package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	repo       = "3b391433/shanbay-cli"
	apiLatest  = "https://api.github.com/repos/" + repo + "/releases/latest"
	httpTO     = 8 * time.Second
	installTO  = 60 * time.Second
	cacheName  = "update-check.json"
	dateLayout = "2006-01-02"
)

type asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Release 只保留我们关心的字段。
type Release struct {
	TagName string  `json:"tag_name"`
	HTMLURL string  `json:"html_url"`
	Assets  []asset `json:"assets"`
}

// AssetForCurrentPlatform 返回本机 GOOS/GOARCH 对应的 tar.gz 资产。
func (r *Release) AssetForCurrentPlatform() (name, url string, ok bool) {
	want := fmt.Sprintf("sb-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	for _, a := range r.Assets {
		if a.Name == want {
			return a.Name, a.URL, true
		}
	}
	return "", "", false
}

// Latest fetches the latest release metadata from GitHub.
func Latest(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiLatest, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "shanbay-cli-updater")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github api %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var r Release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	if r.TagName == "" {
		return nil, errors.New("release without tag_name")
	}
	return &r, nil
}

// Install downloads the latest release and replaces the current binary in place.
// It prints progress to stdout.
func Install(currentVersion string) error {
	ctx, cancel := context.WithTimeout(context.Background(), installTO)
	defer cancel()

	fmt.Println("拉取最新 release …")
	rel, err := Latest(ctx)
	if err != nil {
		return fmt.Errorf("获取 latest release 失败: %w", err)
	}
	fmt.Printf("最新版本: %s(当前 %s)\n", rel.TagName, currentVersion)
	if cmp(rel.TagName, currentVersion) <= 0 {
		fmt.Println("已是最新版本,无需更新。")
		writeCache(rel.TagName) // 顺手刷新缓存,避免下次再提醒
		return nil
	}

	name, url, ok := rel.AssetForCurrentPlatform()
	if !ok {
		return fmt.Errorf("release %s 未提供 %s/%s 的资产", rel.TagName, runtime.GOOS, runtime.GOARCH)
	}
	fmt.Printf("下载 %s …\n", name)

	binPath, err := currentBinaryPath()
	if err != nil {
		return err
	}
	binDir := filepath.Dir(binPath)

	// 新二进制写到目标目录下的临时文件,保证与目标同 filesystem,rename 可原子。
	tmp, err := os.CreateTemp(binDir, "sb-new-*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpName)
		}
	}()

	if err := downloadAndExtract(ctx, url, name, tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	// Linux/macOS 允许 rename 一个正在执行的二进制:老 inode 保留给当前进程,新
	// 文件出现在原路径。用这一步做原子替换。
	if err := os.Rename(tmpName, binPath); err != nil {
		return fmt.Errorf("替换 %s 失败: %w", binPath, err)
	}
	cleanupTmp = false

	writeCache(rel.TagName)
	fmt.Printf("✓ 已升级到 %s(%s)\n", rel.TagName, binPath)
	return nil
}

func currentBinaryPath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("定位当前二进制路径失败: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		// symlink 解析失败也让 rename 用原路径试一下,而不是直接放弃。
		return p, nil
	}
	return resolved, nil
}

func downloadAndExtract(ctx context.Context, url, assetName string, out io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "shanbay-cli-updater")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("下载 %s 失败: %w", assetName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载 %s: %s", assetName, resp.Status)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("gunzip 失败: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("tar 中未找到可执行文件")
		}
		if err != nil {
			return fmt.Errorf("tar 解析失败: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if _, err := io.Copy(out, tr); err != nil {
			return fmt.Errorf("写出二进制失败: %w", err)
		}
		return nil
	}
}

// -- daily notice ---------------------------------------------------------

type cache struct {
	LastCheck string `json:"last_check"` // YYYY-MM-DD
	Latest    string `json:"latest"`     // 最近一次拉到的 latest tag
}

func cachePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "shanbay-cli", cacheName)
}

func readCache() cache {
	var c cache
	b, err := os.ReadFile(cachePath())
	if err != nil {
		return c
	}
	_ = json.Unmarshal(b, &c)
	return c
}

func writeCache(latest string) {
	p := cachePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	b, err := json.Marshal(cache{LastCheck: time.Now().Format(dateLayout), Latest: latest})
	if err != nil {
		return
	}
	_ = os.WriteFile(p, b, 0o644)
}

// refreshSubcmd 是主命令识别的隐藏子命令名。主进程发现缓存过期时 fork 一个
// 自身副本执行它:该副本 setsid 脱离进程组,主进程不 Wait,立即返回。
const refreshSubcmd = "__refresh-updates"

// NoticeAndBackgroundCheck 返回可能的一行升级提示;若今日尚未刷新过缓存,会
// spawn 一个脱离主进程的子进程去刷新缓存(不阻塞主命令)。
//
// 首次运行(缓存不存在)不提示;后台子进程刷新后下次运行才会看到提示——这是
// 「不阻塞」的代价,避免拖慢一次命令数百 ms 的网络往返。
func NoticeAndBackgroundCheck(currentVersion string) string {
	c := readCache()
	today := time.Now().Format(dateLayout)
	if c.LastCheck != today {
		spawnRefresher()
	}
	if c.Latest == "" {
		return ""
	}
	if cmp(c.Latest, currentVersion) > 0 {
		return fmt.Sprintf("提示:有新版本 %s 可用(当前 %s),运行 `sb update` 升级。", c.Latest, currentVersion)
	}
	return ""
}

// IsRefreshSubcmd 判断 arg 是否是隐藏的后台刷新子命令。
func IsRefreshSubcmd(arg string) bool { return arg == refreshSubcmd }

// RefreshCacheCLI 是隐藏子命令的入口:拉一次 latest release,写缓存,退出。
// 静默处理错误——本命令不该产生任何输出。
func RefreshCacheCLI() {
	ctx, cancel := context.WithTimeout(context.Background(), httpTO)
	defer cancel()
	rel, err := Latest(ctx)
	if err != nil || rel == nil {
		return
	}
	writeCache(rel.TagName)
}

func spawnRefresher() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, refreshSubcmd)
	// setsid 让子进程脱离父进程会话/进程组,父进程退出不会带走它。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// 屏蔽 stdio,避免子进程持有父进程的 tty 或输出干扰。
	devNull, _ := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if devNull != nil {
		cmd.Stdin = devNull
		cmd.Stdout = devNull
		cmd.Stderr = devNull
	}
	if err := cmd.Start(); err != nil {
		if devNull != nil {
			devNull.Close()
		}
		return
	}
	// 释放 Process 句柄,避免父进程等待/僵尸;stdio fd 由子进程持有,父进程这边可关。
	_ = cmd.Process.Release()
	if devNull != nil {
		devNull.Close()
	}
}

// cmp 比较两个 vMAJOR.MINOR.PATCH,>0 = a 更新。非数字段视作 0;开发版 "dev"
// 视作永远最旧,确保本地未打 tag 的构建也能收到提醒。
func cmp(a, b string) int {
	if a == b {
		return 0
	}
	if b == "dev" && a != "dev" {
		return 1
	}
	if a == "dev" && b != "dev" {
		return -1
	}
	as := splitVer(a)
	bs := splitVer(b)
	for i := 0; i < len(as) || i < len(bs); i++ {
		var ai, bi int
		if i < len(as) {
			ai = as[i]
		}
		if i < len(bs) {
			bi = bs[i]
		}
		if ai != bi {
			if ai > bi {
				return 1
			}
			return -1
		}
	}
	return 0
}

func splitVer(v string) []int {
	v = strings.TrimPrefix(v, "v")
	// 丢弃 pre-release/build 元数据(-rc1、+meta),仅按数字段比较
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out
}
