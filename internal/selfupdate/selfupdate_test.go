package selfupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRelease 是一个假的 GitHub：最新的 Release 是 tag，带 claude-switch-linux-amd64（一段打印 printed 的脚本）和 SHA256SUMS。
// badSum 时清单里的哈希是错的。
type fakeRelease struct {
	tag, printed string
	badSum       bool
}

func (f *fakeRelease) serve(t *testing.T) *httptest.Server {
	t.Helper()
	bin := []byte("#!/bin/sh\necho 'claude-switch " + f.printed + "'\n")
	sum := sha256.Sum256(bin)
	hash := hex.EncodeToString(sum[:])
	if f.badSum {
		hash = strings.Repeat("0", 64)
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": f.tag,
				"assets": []map[string]string{
					{"name": AssetName("amd64"), "browser_download_url": srv.URL + "/bin"},
					{"name": SumsAsset, "browser_download_url": srv.URL + "/sums"},
				},
			})
		case "/bin":
			_, _ = w.Write(bin)
		case "/sums":
			_, _ = w.Write([]byte(hash + "  " + AssetName("amd64") + "\n" + strings.Repeat("1", 64) + "  " + AssetName("arm64") + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const oldProgram = "#!/bin/sh\necho 'claude-switch v0.0.2'\n"

// setup 放一份「在跑的」程序，返回更新它的 Updater 和输出。
func setup(t *testing.T, srv *httptest.Server) (*Updater, *bytes.Buffer) {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "claude-switch")
	if err := os.WriteFile(exe, []byte(oldProgram), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	return &Updater{API: srv.URL, Client: srv.Client(), Exe: exe, Current: "v0.0.2", Arch: "amd64", Stdout: &out}, &out
}

// unchanged 核对程序还是原来那份，目录里也没有留下临时文件。
func unchanged(t *testing.T, u *Updater) {
	t.Helper()
	got, err := os.ReadFile(u.Exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != oldProgram {
		t.Error("原来的程序被改了")
	}
	entries, err := os.ReadDir(filepath.Dir(u.Exe))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("目录里多了文件：%v", entries)
	}
}

func TestNewerReleaseReplacesProgram(t *testing.T) {
	t.Parallel()
	f := &fakeRelease{tag: "v0.0.10", printed: "v0.0.10"}
	u, out := setup(t, f.serve(t))
	if err := u.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(u.Exe)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "v0.0.10") {
		t.Errorf("没换成新版：%q", got)
	}
	info, err := os.Stat(u.Exe)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("权限是 %v", info.Mode().Perm())
	}
	if !strings.Contains(out.String(), "已从 v0.0.2 更新到 v0.0.10") {
		t.Errorf("输出：%q", out.String())
	}
}

func TestSameOrOlderReleaseChangesNothing(t *testing.T) {
	t.Parallel()
	for _, tag := range []string{"v0.0.2", "v0.0.1"} {
		f := &fakeRelease{tag: tag, printed: tag}
		u, out := setup(t, f.serve(t))
		if err := u.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		unchanged(t, u)
		if !strings.Contains(out.String(), "已是最新：v0.0.2") {
			t.Errorf("%s 时输出：%q", tag, out.String())
		}
	}
}

func TestBadDownloadChangesNothing(t *testing.T) {
	t.Parallel()
	cases := map[string]*fakeRelease{
		"哈希对不上":  {tag: "v0.0.3", printed: "v0.0.3", badSum: true},
		"报的版本不对": {tag: "v0.0.3", printed: "v0.0.9"},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			u, _ := setup(t, f.serve(t))
			if err := u.Run(context.Background()); err == nil {
				t.Fatal("该失败")
			}
			unchanged(t, u)
		})
	}
}
