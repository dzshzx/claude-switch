// Package selfupdate 把从 GitHub Release 装的 claude-switch 换成最新的 Release。
//
// 只有 Release 里的程序会自己更新（发版时编进了 release 标记）。go install 装的，或别的程序当依赖编出来、
// 带在自己身边的（比如 Zeta），版本由装它的一方定，这里拒绝，免得和那一方固定的版本对不上。
//
// 流程：问 GitHub 最新的 Release；它不比在跑的这份新就不动。否则下载本机架构的程序和 SHA256SUMS，核对哈希，
// 在程序所在的目录写成临时文件，跑一次 version 确认能运行、版本对得上，再改名换掉原来的程序。
// 任何一步失败，原来的程序都不动。这是 claude-switch 唯一联网的地方，换号不联网。
package selfupdate

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultAPI 是本仓 Release 的 GitHub 接口地址。
const DefaultAPI = "https://api.github.com/repos/dzshzx/claude-switch"

// SumsAsset 是 Release 里各程序 SHA-256 的清单（sha256sum 的格式）。
const SumsAsset = "SHA256SUMS"

// AssetName 是 Release 里给 arch 架构的 Linux 程序的文件名。
func AssetName(arch string) string { return "claude-switch-linux-" + arch }

// ErrNotRelease 表示在跑的这份不是从 Release 装的，不自己更新。
var ErrNotRelease = errors.New("not a release build")

// Updater 更新 Exe 这个程序。Current 是它的版本（vX.Y.Z），Release 为假时拒绝更新。
type Updater struct {
	API     string
	Client  *http.Client
	Exe     string
	Current string
	Release bool
	Arch    string
	Stdout  io.Writer
}

type release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (r release) asset(name string) (string, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL, true
		}
	}
	return "", false
}

// Run 查最新的 Release，比在跑的新就换上它。
func (u *Updater) Run(ctx context.Context) error {
	if !u.Release {
		return ErrNotRelease
	}
	current, ok := parse(u.Current)
	if !ok {
		return fmt.Errorf("认不出在跑的版本 %q", u.Current)
	}
	var latest release
	if err := u.getJSON(ctx, u.API+"/releases/latest", &latest); err != nil {
		return fmt.Errorf("查不到最新的 Release：%w", err)
	}
	next, ok := parse(latest.Tag)
	if !ok {
		return fmt.Errorf("最新的 Release 的 tag %q 不是 vX.Y.Z", latest.Tag)
	}
	if !newer(next, current) {
		fmt.Fprintf(u.Stdout, "已是最新：%s\n", u.Current)
		return nil
	}
	name := AssetName(u.Arch)
	binURL, ok := latest.asset(name)
	if !ok {
		return fmt.Errorf("%s 里没有 %s", latest.Tag, name)
	}
	sumsURL, ok := latest.asset(SumsAsset)
	if !ok {
		return fmt.Errorf("%s 里没有 %s", latest.Tag, SumsAsset)
	}
	sums, err := u.get(ctx, sumsURL)
	if err != nil {
		return fmt.Errorf("下载 %s：%w", SumsAsset, err)
	}
	want, ok := sumOf(sums, name)
	if !ok {
		return fmt.Errorf("%s 里没有 %s 的哈希", SumsAsset, name)
	}
	bin, err := u.get(ctx, binURL)
	if err != nil {
		return fmt.Errorf("下载 %s：%w", name, err)
	}
	got := sha256.Sum256(bin)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("下载的 %s 和 %s 里的哈希对不上，没有换", name, SumsAsset)
	}
	if err := u.replace(ctx, bin, latest.Tag); err != nil {
		return err
	}
	fmt.Fprintf(u.Stdout, "已从 %s 更新到 %s\n", u.Current, latest.Tag)
	return nil
}

// replace 把 bin 写成 Exe 旁边的临时文件，跑一次 version 核对是 tag 这一版，再改名换掉 Exe。
func (u *Updater) replace(ctx context.Context, bin []byte, tag string) error {
	dir := filepath.Dir(u.Exe)
	tmp, err := os.CreateTemp(dir, ".claude-switch-update-*")
	if err != nil {
		return err
	}
	path := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(path)
		}
	}()
	if _, err := tmp.Write(bin); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return err
	}
	check, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(check, path, "version").Output()
	if err != nil {
		return fmt.Errorf("下载的新版运行不了，没有换：%w", err)
	}
	if got := strings.TrimSpace(string(out)); got != "claude-switch "+tag {
		return fmt.Errorf("下载的新版报的版本是 %q，不是 %s，没有换", got, tag)
	}
	if err := os.Rename(path, u.Exe); err != nil {
		return err
	}
	keep = true
	return nil
}

func (u *Updater) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "claude-switch/"+u.Current)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := u.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s 返回 %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

func (u *Updater) getJSON(ctx context.Context, url string, v any) error {
	body, err := u.get(ctx, url)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

// sumOf 从 sha256sum 格式的清单里取 name 的哈希（小写十六进制）。
func sumOf(sums []byte, name string) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name && len(fields[0]) == 64 {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}

// parse 把 vX.Y.Z 拆成三个数。
func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if !strings.HasPrefix(v, "v") || len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func newer(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
