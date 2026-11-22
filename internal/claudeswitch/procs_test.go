package claudeswitch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeProc 在 root 下摆一个 /proc/<pid>：exe 指向 home 下 Claude Code 的安装目录，进程名 claude，环境变量是 environ。
func fakeProc(t *testing.T, root, home, pid, environ string) {
	t.Helper()
	dir := filepath.Join(root, pid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(home, claudeExecutableDirs[0], "2.1.330")
	if err := os.Symlink(exe, filepath.Join(dir, "exe")); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"comm": "claude\n", "cmdline": "claude\x00-p\x00", "environ": environ} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// 正在退出的进程：exe 还读得到，读 environ 时内存已经放掉、读到空的。它不算在跑，否则换号会被正好在退出的进程挡下
// （同一台机器上别处的 claude 进程退出时都会撞上）。
func TestExitingProcessIsNotRunning(t *testing.T) {
	t.Parallel()
	root, home := t.TempDir(), t.TempDir()
	fakeProc(t, root, home, "100", "")
	fakeProc(t, root, home, "200", "HOME="+home+"\x00")
	fakeProc(t, root, home, "300", "PATH=/usr/bin\x00")
	found, err := claudeProcesses(root, home)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(found, ";") != "200 claude -p;300 claude -p" {
		t.Fatalf("找到 %q：正在退出的 100 不算，HOME 是 home 的 200、没设 HOME 的 300 都算", found)
	}
}
