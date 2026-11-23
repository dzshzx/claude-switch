package account

import (
	"os"
	"path/filepath"
	"testing"
)

// home 建一个临时 HOME，里面有 ~/.claude 与 ~/.claude-batch。
func home(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "home")
	for _, sub := range []string{".claude", ".claude-batch"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return dir
}

func switchedTo(t *testing.T, home, content string) {
	t.Helper()
	path := filepath.Join(home, currentFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultConfigDirReadsTheSwitchRecord(t *testing.T) {
	dir := home(t)
	if got := Name(); got != Default {
		t.Errorf("no record: %q", got)
	}
	// 合写法的名字照用（账号名或非账号目标名），不合写法或保留的回落 Default
	for content, want := range map[string]string{"second\n": "second", "gateway": "gateway", "work-2\n": "work-2",
		"../x": Default, "": Default, "Work": Default, "accounts": Default, "pending": Default, "add": Default} {
		switchedTo(t, dir, content)
		if got := Name(); got != want {
			t.Errorf("record %q: %q, want %q", content, got, want)
		}
	}
}

func TestWriteCurrentRoundTrips(t *testing.T) {
	dir := home(t)
	if err := os.MkdirAll(filepath.Join(dir, StateDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteCurrent(dir, "work"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, currentFile))
	if err != nil || string(data) != "work\n" {
		t.Errorf("current = %q（%v）", data, err)
	}
	if got := Current(dir); got != "work" {
		t.Errorf("Current = %q", got)
	}
}

func TestConfigDirResolvingToTheDefaultCountsAsDefault(t *testing.T) {
	dir := home(t)
	link := filepath.Join(dir, "claude-link")
	if err := os.Symlink(filepath.Join(dir, ".claude"), link); err != nil {
		t.Fatal(err)
	}
	switchedTo(t, dir, "second")
	for _, configDir := range []string{link + "/", filepath.Join(dir, ".claude") + "/", "~/.claude"} {
		t.Setenv("CLAUDE_CONFIG_DIR", configDir)
		if got := Name(); got != "second" {
			t.Errorf("CLAUDE_CONFIG_DIR=%q: %q, want the switch record", configDir, got)
		}
	}
}

// 路径的后半段不存在时，存在的前半段照样解析符号链接：~/.claude 还没建、配置目录经一个指向 HOME 的链接写出来，
// 两边解析后相同，仍算默认目录。
func TestMissingDirectoriesStillResolveThroughLinks(t *testing.T) {
	dir := home(t)
	if err := os.Remove(filepath.Join(dir, ".claude")); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(dir), "home-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	switchedTo(t, dir, "second")
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(link, ".claude"))
	if got := Name(); got != "second" {
		t.Errorf("got %q, want the switch record", got)
	}
}

func TestOtherConfigDirIsNamedAfterItsDirectory(t *testing.T) {
	dir := home(t)
	switchedTo(t, dir, "second") // 另设的配置目录不看记录
	cases := map[string]string{
		"~/.claude-batch":                    "claude-batch",
		filepath.Join(dir, "batch-config"):   "batch-config", // 目录不存在也照目录名
		filepath.Join(dir, "..hidden") + "/": "hidden",
	}
	for configDir, want := range cases {
		t.Setenv("CLAUDE_CONFIG_DIR", configDir)
		if got := Name(); got != want {
			t.Errorf("CLAUDE_CONFIG_DIR=%q: %q, want %q", configDir, got, want)
		}
	}
}

func TestIsTargetNeedsAValidNameAndARegularFile(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, TargetsDir)
	if err := os.MkdirAll(filepath.Join(dir, "folder.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gateway", "add"} {
		if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range map[string]bool{"gateway": true, "folder": false, "add": false, "missing": false, "Bad": false} {
		if got := IsTarget(home, name); got != want {
			t.Errorf("IsTarget(%q) = %v，期望 %v", name, got, want)
		}
	}
}
