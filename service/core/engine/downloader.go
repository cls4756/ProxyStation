package engine

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ProxyStation/proxystation/db/configure"
)

// 日志回调函数
var LogCallback func(msg string)

// DownloadProgress 下载进度
type DownloadProgress struct {
	Kernel  string  `json:"kernel"`
	Status  string  `json:"status"` // "downloading" | "extracting" | "done" | "error"
	Message string  `json:"message"`
	Percent float64 `json:"percent"`
}

// applyMirrorURL 将 GitHub URL 替换为镜像加速地址
func applyMirrorURL(originalURL, mirror string) string {
	if mirror == "" {
		return originalURL
	}
	mirror = strings.TrimRight(mirror, "/")
	for _, host := range []string{
		"https://raw.githubusercontent.com/",
		"https://github.com/",
		"https://objects.githubusercontent.com/",
	} {
		if strings.HasPrefix(originalURL, host) {
			return mirror + "/" + originalURL
		}
	}
	return originalURL
}

// newHTTPClient 创建支持代理的 HTTP 客户端
func newHTTPClient(timeout time.Duration) *http.Client {
	setting := configure.GetSettingNotNil()
	transport := &http.Transport{}
	if setting.DownloadProxy != "" {
		if proxyURL, err := url.Parse(setting.DownloadProxy); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

// newDownloadClient 创建用于大文件下载的 HTTP 客户端（只设连接超时，不限制读取时间）
func newDownloadClient() *http.Client {
	setting := configure.GetSettingNotNil()
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: 30 * time.Second,
	}
	if setting.DownloadProxy != "" {
		if proxyURL, err := url.Parse(setting.DownloadProxy); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	// Timeout=0 表示不限制总时间，只靠 transport 层的连接超时
	return &http.Client{Timeout: 0, Transport: transport}
}

// githubRelease GitHub release API 响应
type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func stripMirrorURL(maybeMirroredURL, mirror string) string {
	if mirror == "" {
		return maybeMirroredURL
	}
	mirror = strings.TrimRight(mirror, "/")
	prefix := mirror + "/"
	if strings.HasPrefix(maybeMirroredURL, prefix) {
		raw := strings.TrimPrefix(maybeMirroredURL, prefix)
		if strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://") {
			return raw
		}
	}
	return maybeMirroredURL
}

func appendUniqueURL(urls []string, seen map[string]struct{}, u string) []string {
	u = strings.TrimSpace(u)
	if u == "" {
		return urls
	}
	if _, ok := seen[u]; ok {
		return urls
	}
	seen[u] = struct{}{}
	return append(urls, u)
}

func buildDataDownloadCandidates(dataType, primaryURL string) []string {
	setting := configure.GetSettingNotNil()
	seen := map[string]struct{}{}
	candidates := make([]string, 0, 8)

	// 1) 当前选择的 URL
	candidates = appendUniqueURL(candidates, seen, primaryURL)
	// 2) 镜像 URL 的原始 GitHub URL（用于镜像 403/限流时回退）
	candidates = appendUniqueURL(candidates, seen, stripMirrorURL(primaryURL, setting.GithubMirror))

	// 3) 兜底列表：镜像版 + 原始版
	for _, fb := range dataDownloadFallbacks[dataType] {
		candidates = appendUniqueURL(candidates, seen, applyMirrorURL(fb.URL, setting.GithubMirror))
		candidates = appendUniqueURL(candidates, seen, fb.URL)
	}
	return candidates
}

type selectedAsset struct {
	Name        string
	DownloadURL string
}

type KernelMeta struct {
	Path          string `json:"path"`
	LocalVersion  string `json:"localVersion"`
	LatestVersion string `json:"latestVersion"`
	HasUpdate     bool   `json:"hasUpdate"`
	CheckError    string `json:"checkError,omitempty"`
}

// KernelDownloadInfo 各内核的 GitHub 仓库信息
var kernelRepos = map[string]string{
	"xray":    "XTLS/Xray-core",
	"singbox": "SagerNet/sing-box",
	"v2ray":   "v2fly/v2ray-core",
}

type dataReleaseSource struct {
	Repo       string
	AssetNames []string
}

type dataDownloadFallback struct {
	Version string
	URL     string
}

// 数据文件下载来源：
// 1) 先走 GitHub release API（支持获取真实版本号）
// 2) geosite 增加 latest/download 直链兜底，避免上游资产名或发布方式变化导致无法下载
var dataReleaseSources = map[string][]dataReleaseSource{
	"geoip": {
		{Repo: "v2fly/geoip", AssetNames: []string{"geoip.dat"}},
		{Repo: "Loyalsoldier/v2ray-rules-dat", AssetNames: []string{"geoip.dat"}},
	},
	"geosite": {
		{Repo: "Loyalsoldier/v2ray-rules-dat", AssetNames: []string{"geosite.dat"}},
		{Repo: "v2fly/domain-list-community", AssetNames: []string{"geosite.dat", "dlc.dat"}},
	},
}

var dataDownloadFallbacks = map[string][]dataDownloadFallback{
	"geoip": {
		{Version: "latest", URL: "https://github.com/v2fly/geoip/releases/latest/download/geoip.dat"},
		{Version: "latest", URL: "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat"},
	},
	"geosite": {
		{Version: "latest", URL: "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat"},
		{Version: "latest", URL: "https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat"},
	},
}

// rule-set 文件列表（sing-box 格式）
// geosite/geoip 来自 MetaCubeX/meta-rules-dat（更全，包含 gfw 等）
var ruleSetFiles = []struct {
	Name string
	URL  string
}{
	{"geosite-cn", "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/cn.srs"},
	{"geoip-cn", "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geoip/cn.srs"},
	{"geosite-gfw", "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/gfw.srs"},
	{"geosite-telegram", "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/telegram.srs"},
	{"geosite-netflix", "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/netflix.srs"},
	{"geosite-youtube", "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/youtube.srs"},
	{"geosite-google", "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/google.srs"},
	{"geosite-openai", "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/openai.srs"},
}

// GetLatestRelease 获取 GitHub 最新 release 信息
func GetLatestRelease(kernel string) (string, string, error) {
	repo, ok := kernelRepos[kernel]
	if !ok {
		return "", "", fmt.Errorf("unknown kernel: %v", kernel)
	}

	setting := configure.GetSettingNotNil()
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	client := newHTTPClient(30 * time.Second)
	resp, err := client.Get(url)
	if err != nil {
		return "", "", fmt.Errorf("fetch release info failed (network error or timeout): %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("fetch release info failed (HTTP %d)", resp.StatusCode)
	}
	defer resp.Body.Close()

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", "", fmt.Errorf("parse release: %w", err)
	}

	// 根据当前系统选择合适的资产
	asset := selectAsset(kernel, release)
	if asset.DownloadURL == "" {
		return "", "", fmt.Errorf("no suitable asset found for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	// 应用 GitHub 镜像加速
	downloadURL := applyMirrorURL(asset.DownloadURL, setting.GithubMirror)

	return release.TagName, downloadURL, nil
}

// GetLatestDataRelease 获取数据文件的最新 release 信息
func GetLatestDataRelease(dataType string) (string, string, error) {
	sources, ok := dataReleaseSources[dataType]
	if !ok {
		return "", "", fmt.Errorf("unknown data type: %v", dataType)
	}

	setting := configure.GetSettingNotNil()
	client := newHTTPClient(30 * time.Second)
	var errs []string

	for _, source := range sources {
		url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", source.Repo)
		resp, err := client.Get(url)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", source.Repo, err))
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			errs = append(errs, fmt.Sprintf("%s: HTTP %d", source.Repo, resp.StatusCode))
			continue
		}

		var release githubRelease
		decodeErr := json.NewDecoder(resp.Body).Decode(&release)
		_ = resp.Body.Close()
		if decodeErr != nil {
			errs = append(errs, fmt.Sprintf("%s: parse release: %v", source.Repo, decodeErr))
			continue
		}

		for _, asset := range release.Assets {
			for _, name := range source.AssetNames {
				if strings.EqualFold(asset.Name, name) {
					downloadURL := applyMirrorURL(asset.BrowserDownloadURL, setting.GithubMirror)
					return release.TagName, downloadURL, nil
				}
			}
		}
		errs = append(errs, fmt.Sprintf("%s: no suitable asset (%s)", source.Repo, strings.Join(source.AssetNames, ",")))
	}

	for _, fb := range dataDownloadFallbacks[dataType] {
		if strings.TrimSpace(fb.URL) != "" {
			return fb.Version, applyMirrorURL(fb.URL, setting.GithubMirror), nil
		}
	}

	return "", "", fmt.Errorf("fetch data release failed for %s: %s", dataType, strings.Join(errs, " | "))
}

// selectAsset 根据当前系统选择合适的下载资产
func selectAsset(kernel string, release githubRelease) selectedAsset {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// 标准化架构名
	archMap := map[string][]string{
		"amd64": {"amd64", "x86_64", "64"},
		"arm64": {"arm64", "aarch64"},
		"386":   {"386", "x86_32", "32"},
		"arm":   {"armv7", "arm"},
	}
	osMap := map[string][]string{
		"linux":   {"linux"},
		"windows": {"windows"},
		"darwin":  {"darwin", "macos", "osx"},
	}

	archKeywords := archMap[goarch]
	osKeywords := osMap[goos]

	var fallback selectedAsset

	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		// 跳过签名文件
		if strings.HasSuffix(name, ".sha256") || strings.HasSuffix(name, ".sig") ||
			strings.HasSuffix(name, ".asc") || strings.HasSuffix(name, ".dgst") {
			continue
		}
		// 必须是压缩包
		if !strings.HasSuffix(name, ".zip") && !strings.HasSuffix(name, ".tar.gz") &&
			!strings.HasSuffix(name, ".tgz") {
			continue
		}

		osMatch := false
		for _, kw := range osKeywords {
			if strings.Contains(name, kw) {
				osMatch = true
				break
			}
		}
		archMatch := false
		for _, kw := range archKeywords {
			if strings.Contains(name, kw) {
				archMatch = true
				break
			}
		}

		if osMatch && archMatch {
			// 避免把不明确的 v3/v4 变体误选为通用版本，除非没有更好的候选
			if strings.Contains(name, "amd64v") || strings.Contains(name, "x86_64_v") {
				if fallback.DownloadURL == "" {
					fallback = selectedAsset{Name: asset.Name, DownloadURL: asset.BrowserDownloadURL}
				}
				continue
			}
			return selectedAsset{Name: asset.Name, DownloadURL: asset.BrowserDownloadURL}
		}
	}
	return fallback
}

// DownloadKernel 下载并解压内核到 dataDir/bin/
func DownloadKernel(kernel string, progressCh chan<- DownloadProgress) error {
	fail := func(stage string, err error) error {
		msg := fmt.Sprintf("❌ %s%s: %v", kernel, stage, err)
		progressCh <- DownloadProgress{Kernel: kernel, Status: "error", Message: err.Error()}
		if LogCallback != nil {
			LogCallback(msg)
		}
		return err
	}

	binDir := filepath.Join(Manager.dataDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fail("安装失败", fmt.Errorf("create bin dir: %w", err))
	}

	progressCh <- DownloadProgress{Kernel: kernel, Status: "downloading", Message: "获取最新版本信息…", Percent: 0}

	version, downloadURL, err := GetLatestRelease(kernel)
	if err != nil {
		return fail("下载失败", fmt.Errorf("获取版本信息失败: %w", err))
	}
	assetName := filepath.Base(strings.Split(downloadURL, "?")[0])
	if LogCallback != nil {
		LogCallback(fmt.Sprintf("📦 选择 %s 版本资产: %s (%s/%s)", kernel, assetName, runtime.GOOS, runtime.GOARCH))
	}
	localVersion := GetKernelVersion(kernel)
	if localVersion != "" && CompareVersions(version, localVersion) <= 0 {
		msg := fmt.Sprintf("%s 当前已是最新版本（%s）", kernel, localVersion)
		progressCh <- DownloadProgress{Kernel: kernel, Status: "done", Message: msg, Percent: 100}
		if LogCallback != nil {
			LogCallback("✅ " + msg)
		}
		return nil
	}

	// 注意：GetLatestRelease 已经应用了镜像，不需要再次应用
	progressCh <- DownloadProgress{Kernel: kernel, Status: "downloading",
		Message: fmt.Sprintf("下载 %s %s…", kernel, version), Percent: 5}
	if LogCallback != nil {
		LogCallback(fmt.Sprintf("📥 开始下载 %s %s: %s", kernel, version, downloadURL))
	}

	tmpFile, err := os.CreateTemp("", "proxystation-kernel-*")
	if err != nil {
		return fail("下载失败", fmt.Errorf("创建临时文件失败: %w", err))
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	client := newDownloadClient()
	resp, err := client.Get(downloadURL)
	if err != nil {
		return fail("下载失败", fmt.Errorf("下载失败（网络错误）: %w\nURL: %s", err, downloadURL))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fail("下载失败", fmt.Errorf("下载失败（HTTP %d）\nURL: %s", resp.StatusCode, downloadURL))
	}

	total := resp.ContentLength
	var downloaded int64
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, we := tmpFile.Write(buf[:n]); we != nil {
				return fail("下载失败", fmt.Errorf("写入失败: %w", we))
			}
			downloaded += int64(n)
			if total > 0 {
				pct := float64(downloaded) / float64(total) * 90
				progressCh <- DownloadProgress{Kernel: kernel, Status: "downloading",
					Message: fmt.Sprintf("下载中 %.1f MB / %.1f MB", float64(downloaded)/1e6, float64(total)/1e6),
					Percent: 5 + pct}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fail("下载失败", fmt.Errorf("读取失败: %w\nURL: %s", readErr, downloadURL))
		}
	}

	progressCh <- DownloadProgress{Kernel: kernel, Status: "extracting", Message: "解压中…", Percent: 95}

	tmpFile.Seek(0, 0)
	if strings.HasSuffix(downloadURL, ".zip") {
		err = extractZip(tmpFile.Name(), binDir, kernel)
	} else {
		err = extractTarGz(tmpFile, binDir, kernel)
	}
	if err != nil {
		return fail("安装失败", fmt.Errorf("解压失败: %w", err))
	}

	progressCh <- DownloadProgress{Kernel: kernel, Status: "done",
		Message: fmt.Sprintf("%s %s 安装完成", kernel, version), Percent: 100}
	if LogCallback != nil {
		LogCallback(fmt.Sprintf("✅ %s %s 安装完成", kernel, version))
	}
	return nil
}

// DownloadData 下载数据文件（geoip.dat 或 geosite.dat）到 dataDir/
func DownloadData(dataType string, progressCh chan<- DownloadProgress) error {
	fail := func(err error) error {
		msg := fmt.Sprintf("❌ %s 下载失败: %v", dataType, err)
		progressCh <- DownloadProgress{Kernel: dataType, Status: "error", Message: err.Error()}
		if LogCallback != nil {
			LogCallback(msg)
		}
		return err
	}

	if dataType != "geoip" && dataType != "geosite" {
		return fail(fmt.Errorf("unknown data type: %v", dataType))
	}

	dataDir := Manager.dataDir
	if dataDir == "" {
		return fail(fmt.Errorf("data directory not set"))
	}

	progressCh <- DownloadProgress{Kernel: dataType, Status: "downloading", Message: "获取最新版本信息…", Percent: 0}

	version, downloadURL, err := GetLatestDataRelease(dataType)
	if err != nil {
		return fail(fmt.Errorf("获取版本信息失败: %w", err))
	}

	progressCh <- DownloadProgress{Kernel: dataType, Status: "downloading",
		Message: fmt.Sprintf("下载 %s %s…", dataType, version), Percent: 5}

	tmpFile, err := os.CreateTemp("", "proxystation-data-*")
	if err != nil {
		return fail(fmt.Errorf("创建临时文件失败: %w", err))
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	client := newDownloadClient()
	var lastErr error
	candidates := buildDataDownloadCandidates(dataType, downloadURL)
	for idx, u := range candidates {
		if LogCallback != nil {
			LogCallback(fmt.Sprintf("📥 开始下载 %s %s (%d/%d): %s", dataType, version, idx+1, len(candidates), u))
		}
		if idx > 0 {
			progressCh <- DownloadProgress{Kernel: dataType, Status: "downloading",
				Message: fmt.Sprintf("主链接失败，正在尝试备用源（%d/%d）…", idx+1, len(candidates)), Percent: 8}
		}
		if e := tmpFile.Truncate(0); e != nil {
			return fail(fmt.Errorf("重置临时文件失败: %w", e))
		}
		if _, e := tmpFile.Seek(0, 0); e != nil {
			return fail(fmt.Errorf("重置临时文件偏移失败: %w", e))
		}

		resp, e := client.Get(u)
		if e != nil {
			lastErr = fmt.Errorf("下载失败（网络错误）: %w\nURL: %s", e, u)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("下载失败（HTTP %d）\nURL: %s", resp.StatusCode, u)
			_ = resp.Body.Close()
			continue
		}

		total := resp.ContentLength
		var downloaded int64
		buf := make([]byte, 32*1024)
		ok := true
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				if _, we := tmpFile.Write(buf[:n]); we != nil {
					lastErr = fmt.Errorf("写入失败: %w", we)
					ok = false
					break
				}
				downloaded += int64(n)
				if total > 0 {
					pct := float64(downloaded) / float64(total) * 90
					progressCh <- DownloadProgress{Kernel: dataType, Status: "downloading",
						Message: fmt.Sprintf("下载中 %.1f MB / %.1f MB", float64(downloaded)/1e6, float64(total)/1e6),
						Percent: 5 + pct}
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				lastErr = fmt.Errorf("读取失败: %w\nURL: %s", readErr, u)
				ok = false
				break
			}
		}
		_ = resp.Body.Close()
		if ok {
			lastErr = nil
			break
		}
	}

	if lastErr != nil {
		if fi, statErr := tmpFile.Stat(); statErr == nil && fi.Size() > 0 {
			// 至少有一次成功写入内容，继续后续流程
		} else {
			return fail(lastErr)
		}
	}

	progressCh <- DownloadProgress{Kernel: dataType, Status: "extracting", Message: "处理中…", Percent: 95}

	tmpFile.Seek(0, 0)
	destPath := filepath.Join(dataDir, dataType+".dat")
	destFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fail(fmt.Errorf("创建目标文件失败: %w", err))
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, tmpFile); err != nil {
		return fail(fmt.Errorf("复制文件失败: %w", err))
	}

	progressCh <- DownloadProgress{Kernel: dataType, Status: "done",
		Message: fmt.Sprintf("%s %s 安装完成", dataType, version), Percent: 100}
	if LogCallback != nil {
		LogCallback(fmt.Sprintf("✅ %s %s 安装完成", dataType, version))
	}
	return nil
}

// extractZip 从 zip 中提取内核可执行文件
func extractZip(zipPath, destDir, kernel string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	binNames := kernelBinNames(kernel)
	for _, f := range r.File {
		base := filepath.Base(f.Name)
		if !isBinFile(base, binNames) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		destPath := filepath.Join(destDir, base)
		err = writeFileAtomically(destPath, 0755, func(out *os.File) error {
			_, copyErr := io.Copy(out, rc)
			return copyErr
		})
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// extractTarGz 从 tar.gz 中提取内核可执行文件
func extractTarGz(r io.Reader, destDir, kernel string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	binNames := kernelBinNames(kernel)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		base := filepath.Base(hdr.Name)
		if !isBinFile(base, binNames) {
			continue
		}
		destPath := filepath.Join(destDir, base)
		err = writeFileAtomically(destPath, 0755, func(out *os.File) error {
			_, copyErr := io.Copy(out, tr)
			return copyErr
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFileAtomically(destPath string, perm os.FileMode, write func(*os.File) error) error {
	tmpPath := destPath + ".tmp"
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}

	if err := write(out); err != nil {
		out.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	if runtime.GOOS == "windows" {
		_ = os.Remove(destPath)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func kernelBinNames(kernel string) []string {
	switch kernel {
	case "xray":
		return []string{"xray", "xray.exe"}
	case "singbox":
		return []string{"sing-box", "sing-box.exe"}
	case "v2ray":
		return []string{"v2ray", "v2ray.exe"}
	}
	return []string{kernel}
}

func isBinFile(name string, binNames []string) bool {
	for _, bn := range binNames {
		if strings.EqualFold(name, bn) {
			return true
		}
	}
	return false
}


// GetKernelVersion 获取本地内核版本
func GetKernelVersion(kernel string) string {
	binPath := ""
	switch kernel {
	case "xray":
		p, err := findBin([]string{"xray", "xray.exe"}, []string{
			"/usr/local/bin/xray", "/usr/bin/xray",
		})
		if err != nil {
			return ""
		}
		binPath = p
	case "singbox":
		p, err := findBin([]string{"sing-box", "sing-box.exe"}, []string{
			"/usr/local/bin/sing-box", "/usr/bin/sing-box",
		})
		if err != nil {
			return ""
		}
		binPath = p
	case "v2ray":
		p, err := findBin([]string{"v2ray", "v2ray.exe"}, []string{
			"/usr/local/bin/v2ray", "/usr/bin/v2ray",
		})
		if err != nil {
			return ""
		}
		binPath = p
	default:
		return ""
	}

	for _, args := range kernelVersionArgs(kernel) {
		cmd := exec.Command(binPath, args...)
		output, err := cmd.Output()
		if err != nil {
			continue
		}
		if version := parseKernelVersion(string(output)); version != "" {
			return version
		}
	}
	return ""
}

func kernelVersionArgs(kernel string) [][]string {
	switch kernel {
	case "singbox":
		return [][]string{{"version"}, {"-version"}}
	default:
		return [][]string{{"-version"}, {"version"}}
	}
}

func parseKernelVersion(output string) string {
	lines := strings.Split(output, "\n")
	if len(lines) > 0 {
		versionLine := lines[0]
		parts := strings.Fields(versionLine)
		for _, part := range parts {
			part = strings.TrimSpace(strings.Trim(part, ",;()"))
			if strings.HasPrefix(part, "v") || (len(part) > 0 && part[0] >= '0' && part[0] <= '9') {
				if hasNumericVersion(part) {
					return part
				}
			}
		}
		return ""
	}
	return ""
}

func hasNumericVersion(v string) bool {
	return len(versionSegments(v)) > 0
}

func CompareVersions(v1, v2 string) int {
	a := versionSegments(v1)
	b := versionSegments(v2)
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	for i := 0; i < maxLen; i++ {
		var av, bv int
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av > bv {
			return 1
		}
		if av < bv {
			return -1
		}
	}
	return 0
}

func versionSegments(v string) []int {
	v = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(v), "v"))
	var segments []int
	var current int
	inNumber := false
	for i := 0; i < len(v); i++ {
		ch := v[i]
		if ch >= '0' && ch <= '9' {
			current = current*10 + int(ch-'0')
			inNumber = true
			continue
		}
		if inNumber {
			segments = append(segments, current)
			current = 0
			inNumber = false
		}
	}
	if inNumber {
		segments = append(segments, current)
	}
	return segments
}

func GetKernelMeta(kernel string) KernelMeta {
	meta := KernelMeta{
		Path:         KernelStatus()[kernel],
		LocalVersion: GetKernelVersion(kernel),
	}
	if meta.Path == "" {
		return meta
	}
	latestVersion, _, err := GetLatestRelease(kernel)
	if err != nil {
		meta.CheckError = err.Error()
		return meta
	}
	meta.LatestVersion = latestVersion
	if hasNumericVersion(latestVersion) && hasNumericVersion(meta.LocalVersion) {
		meta.HasUpdate = CompareVersions(latestVersion, meta.LocalVersion) > 0
	}
	return meta
}

// RuleSetFileInfo rule-set 文件信息
type RuleSetFileInfo struct {
	Name      string `json:"name"`
	LocalDate string `json:"localDate"` // 本地文件修改时间，空表示未下载
	URL       string `json:"url"`
}

// GetRuleSetInfos 获取所有 rule-set 的本地文件信息
func GetRuleSetInfos() []RuleSetFileInfo {
	ruleSetDir := filepath.Join(Manager.dataDir, "rule-set")
	result := make([]RuleSetFileInfo, 0, len(ruleSetFiles))
	for _, rs := range ruleSetFiles {
		info := RuleSetFileInfo{Name: rs.Name, URL: rs.URL}
		path := filepath.Join(ruleSetDir, rs.Name+".srs")
		if fi, err := os.Stat(path); err == nil {
			info.LocalDate = fi.ModTime().Format("2006-01-02")
		}
		result = append(result, info)
	}
	return result
}
