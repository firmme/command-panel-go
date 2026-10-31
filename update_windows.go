//go:build windows

package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v3/pkg/updater"
	"golang.org/x/mod/semver"
)

var appVersion = "1.0.0"

const updateRepository = "firmme/command-panel-go"
const updateAPI = "https://api.github.com"

type githubUpdateProvider struct {
	client  *http.Client
	baseURL string
	token   string
}
type githubRelease struct {
	Tag        string `json:"tag_name"`
	Name       string `json:"name"`
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

func (p *githubUpdateProvider) Name() string { return "github" }
func (p *githubUpdateProvider) request(ctx context.Context, path, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "command-panel-updater")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	return p.client.Do(req)
}
func (p *githubUpdateProvider) Check(ctx context.Context, req updater.CheckRequest) (*updater.Release, error) {
	resp, err := p.request(ctx, "/repos/"+updateRepository+"/releases?per_page=100", "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub 返回 HTTP %d；私有仓库需要先运行 gh auth login", resp.StatusCode)
	}
	var releases []githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&releases); err != nil {
		return nil, err
	}
	current := "v" + strings.TrimPrefix(req.CurrentVersion, "v")
	if !semver.IsValid(current) {
		return nil, fmt.Errorf("当前版本号无效")
	}
	var best *githubRelease
	for i := range releases {
		r := &releases[i]
		v := "v" + strings.TrimPrefix(r.Tag, "v")
		if r.Draft || r.Prerelease || !semver.IsValid(v) || semver.Prerelease(v) != "" || semver.Compare(v, current) <= 0 {
			continue
		}
		if best == nil || semver.Compare(v, "v"+strings.TrimPrefix(best.Tag, "v")) > 0 {
			best = r
		}
	}
	if best == nil {
		return nil, nil
	}
	name := "command-panel-windows-" + req.Arch + ".exe"
	for _, asset := range best.Assets {
		if asset.Name != name {
			continue
		}
		digest, err := hex.DecodeString(strings.TrimPrefix(asset.Digest, "sha256:"))
		if !strings.HasPrefix(asset.Digest, "sha256:") || err != nil || len(digest) != 32 {
			return nil, fmt.Errorf("版本 %s 缺少有效的 SHA-256 校验值", best.Tag)
		}
		if asset.ID <= 0 || asset.Size <= 0 || asset.Size > 200<<20 {
			return nil, fmt.Errorf("更新文件信息无效")
		}
		return &updater.Release{Version: strings.TrimPrefix(best.Tag, "v"), Name: best.Name, Notes: best.Body, Artifact: updater.Artifact{Filename: asset.Name, Filetype: "exe", Size: asset.Size, Platform: req.Platform, Arch: req.Arch}, Verification: &updater.Verification{DigestAlgo: "sha256", Digest: digest}, Metadata: map[string]any{"assetID": asset.ID}}, nil
	}
	return nil, fmt.Errorf("版本 %s 没有当前系统的更新文件 %s", best.Tag, name)
}
func (p *githubUpdateProvider) Download(ctx context.Context, rel *updater.Release, dst io.Writer, progress func(int64, int64)) error {
	id, ok := rel.Metadata["assetID"].(int64)
	if !ok || id <= 0 {
		return fmt.Errorf("更新文件 ID 无效")
	}
	resp, err := p.request(ctx, fmt.Sprintf("/repos/%s/releases/assets/%d", updateRepository, id), "application/octet-stream")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载更新失败：HTTP %d", resp.StatusCode)
	}
	buf := make([]byte, 64*1024)
	var total int64
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > rel.Artifact.Size {
				return fmt.Errorf("更新文件大小不匹配")
			}
			written, err := dst.Write(buf[:n])
			if err != nil {
				return err
			}
			if written != n {
				return io.ErrShortWrite
			}
			progress(total, rel.Artifact.Size)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if total != rel.Artifact.Size {
		return fmt.Errorf("更新文件大小不匹配")
	}
	return nil
}
func githubToken() string {
	if token := strings.TrimSpace(os.Getenv("GH_TOKEN")); token != "" {
		return token
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", "github.com")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

type updateSettings struct {
	IgnoredVersion string `json:"ignoredVersion"`
}
type updateInfo struct {
	CurrentVersion string `json:"currentVersion"`
	Version        string `json:"version"`
	Name           string `json:"name"`
	Notes          string `json:"notes"`
	Ignored        bool   `json:"ignored"`
}

var updateMu sync.Mutex

func (a *App) initUpdater() error {
	provider := &githubUpdateProvider{client: &http.Client{Timeout: 2 * time.Minute}, baseURL: updateAPI, token: githubToken()}
	return a.wails.Updater.Init(updater.Config{CurrentVersion: appVersion, Providers: []updater.Provider{provider}, Window: updater.WindowNone})
}
func (a *App) CheckForUpdates(manual bool) (*updateInfo, error) {
	updateMu.Lock()
	defer updateMu.Unlock()
	if a.wails == nil {
		return nil, fmt.Errorf("更新服务未启动")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rel, err := a.wails.Updater.Check(ctx)
	if err != nil {
		return nil, err
	}
	if rel == nil {
		return &updateInfo{CurrentVersion: appVersion}, nil
	}
	settings := updateSettings{}
	path := filepath.Join(filepath.Dir(a.config.filePath), "update-settings.json")
	if data, err := os.ReadFile(path); err == nil {
		if err = json.Unmarshal(data, &settings); err != nil {
			return nil, fmt.Errorf("读取更新设置失败: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	ignored := settings.IgnoredVersion == rel.Version
	if ignored && !manual {
		return &updateInfo{CurrentVersion: appVersion, Ignored: true}, nil
	}
	return &updateInfo{CurrentVersion: appVersion, Version: rel.Version, Name: rel.Name, Notes: rel.Notes, Ignored: ignored}, nil
}
func (a *App) IgnoreUpdate(version string) error {
	if !semver.IsValid("v" + strings.TrimPrefix(version, "v")) {
		return fmt.Errorf("版本号无效")
	}
	data, err := json.Marshal(updateSettings{IgnoredVersion: strings.TrimPrefix(version, "v")})
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(filepath.Dir(a.config.filePath), "update-settings.json"), data)
}
func (a *App) InstallUpdate() error {
	updateMu.Lock()
	defer updateMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := a.wails.Updater.DownloadAndInstall(ctx); err != nil {
		return fmt.Errorf("下载或校验更新失败: %w", err)
	}
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	if err := a.manager.StopAll(); err != nil {
		return fmt.Errorf("停止运行中的命令失败: %w", err)
	}
	if err := a.wails.Updater.Restart(ctx); err != nil {
		a.manager.mu.Lock()
		a.manager.closed = false
		a.manager.mu.Unlock()
		return fmt.Errorf("重启更新失败: %w", err)
	}
	return nil
}
