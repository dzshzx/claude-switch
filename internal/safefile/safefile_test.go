package safefile

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	return out
}

func TestWriteAtomicReplacesTheFileAndLeavesNoTemporary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	for _, text := range []string{"first", "second"} {
		if err := WriteAtomic(path, []byte(text)); err != nil {
			t.Fatal(err)
		}
		if data, _ := os.ReadFile(path); string(data) != text {
			t.Errorf("content = %q, want %q", data, text)
		}
	}
	if got := names(t, dir); len(got) != 1 || got[0] != "state.json" {
		t.Errorf("files = %v", got)
	}
}

func TestWriteAtomicFailureKeepsTheOldFileAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	if err := WriteAtomic(filepath.Join(dir, "missing", "state.json"), []byte("x")); err == nil {
		t.Error("writing into a missing directory should fail")
	}
	// 目标是个非空目录：改名失败，临时文件要删掉
	target := filepath.Join(dir, "target")
	if err := os.MkdirAll(filepath.Join(target, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(target, []byte("x")); err == nil {
		t.Error("replacing a non-empty directory should fail")
	}
	if got := names(t, dir); len(got) != 1 || got[0] != "target" {
		t.Errorf("files = %v", got)
	}
}

func TestTempPathIsHiddenBesideTheTarget(t *testing.T) {
	if got, want := tempPath("/a/b/state.json", "0011"), "/a/b/.state.json.0011.tmp"; got != want {
		t.Errorf("tempPath = %s, want %s", got, want)
	}
}

func TestWriteAtomicModeFollowsTheUmask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain")
	previous := syscall.Umask(0o027)
	err := WriteAtomic(path, []byte("x"))
	syscall.Umask(previous)
	if info, statErr := os.Stat(path); err != nil || statErr != nil || info.Mode() != 0o640 {
		t.Errorf("WriteAtomic 的权限 = %v（%v, %v）", info.Mode(), err, statErr)
	}
}

func TestWriteDurableModeIsExactWhateverTheUmask(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, mask := range []int{0o077, 0o022, 0o777, 0} {
		for _, mode := range []os.FileMode{0o644, 0o600, 0o640} {
			previous := syscall.Umask(mask)
			err := WriteDurable(path, []byte("中文\n"), mode)
			syscall.Umask(previous)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode() != mode {
				t.Errorf("umask %o、mode %o：权限 = %v（%v）", mask, mode, info.Mode(), err)
			}
		}
	}
	if data, _ := os.ReadFile(path); string(data) != "中文\n" {
		t.Errorf("内容 = %q", data)
	}
	if got := names(t, dir); len(got) != 1 {
		t.Errorf("目录里有 %v", got)
	}
}

func TestWriteDurableFailureKeepsTheOldFileAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "is-a-directory")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteDurable(target, []byte("x"), 0o600); err == nil {
		t.Error("把非空目录换成文件应当失败")
	}
	if got := names(t, dir); len(got) != 1 {
		t.Errorf("留下了临时文件：%v", got)
	}
	if err := WriteDurable(filepath.Join(dir, "missing", "file"), []byte("x"), 0o600); err == nil {
		t.Error("父目录不存在应当失败")
	}
}
