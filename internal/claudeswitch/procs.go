package claudeswitch

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/dzshzx/claude-switch/account"
)

// claudeExecutableDirs：Claude Code 本体所在目录（原生安装的各版本与 Claude Desktop 远程会话用的 CLI）；其他安装方式按进程名认。
var claudeExecutableDirs = []string{".local/share/claude/versions", ".claude/remote/ccd-cli"}

// claudeProcesses 列出用 home 下默认配置目录的 Claude Code 进程，按 pid 排序，每项为「pid 命令行」。
// proc 是 procfs 的挂载点；读不了它时报错，调用方据此拒绝换号，不当作没有进程。
func claudeProcesses(proc, home string) ([]string, error) {
	var prefixes []string
	for _, d := range claudeExecutableDirs {
		prefixes = append(prefixes, filepath.Join(home, d)+"/")
	}
	defaultDir := account.ConfigDir(filepath.Join(home, ".claude"), home)
	all, err := pids(proc)
	if err != nil {
		return nil, err
	}
	found := []string{}
	for _, pid := range all {
		dir := filepath.Join(proc, strconv.Itoa(pid))
		if !isClaude(dir, prefixes) {
			continue
		}
		environ, err := os.ReadFile(filepath.Join(dir, "environ"))
		if err != nil {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join(dir, "cmdline"))
		if err != nil {
			continue
		}
		if configDirOf(parseEnviron(environ), home) == defaultDir {
			args := strings.TrimSpace(strings.ToValidUTF8(string(bytes.ReplaceAll(cmdline, []byte{0}, []byte{' '})), "�"))
			found = append(found, strconv.Itoa(pid)+" "+args)
		}
	}
	return found, nil
}

func pids(proc string) ([]int, error) {
	entries, err := os.ReadDir(proc)
	if err != nil {
		return nil, err
	}
	var out []int
	for _, e := range entries {
		if n, err := strconv.Atoi(e.Name()); err == nil && n >= 0 && strconv.Itoa(n) == e.Name() {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out, nil
}

// isClaude：可执行文件在 Claude Code 的安装目录里（被删掉的旧版本显示为「<路径> (deleted)」，照样算），或进程名是 claude。
// 读不了 exe 链接的进程（别的用户的）不算。
func isClaude(dir string, prefixes []string) bool {
	exe, err := os.Readlink(filepath.Join(dir, "exe"))
	if err != nil {
		return false
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(exe, prefix) {
			return true
		}
	}
	comm, err := os.ReadFile(filepath.Join(dir, "comm"))
	return err == nil && strings.TrimSpace(string(comm)) == "claude"
}

func parseEnviron(data []byte) map[string]string {
	env := map[string]string{}
	for _, item := range bytes.Split(data, []byte{0}) {
		if key, value, ok := bytes.Cut(item, []byte("=")); ok {
			env[string(key)] = string(value)
		}
	}
	return env
}

// configDirOf 是进程用的配置目录：CLAUDE_CONFIG_DIR，未设或为空时是它自己 HOME（没有时用 home）下的 .claude。
func configDirOf(env map[string]string, home string) string {
	raw := env["CLAUDE_CONFIG_DIR"]
	if raw == "" {
		processHome, ok := env["HOME"]
		if !ok {
			processHome = home
		}
		raw = processHome + "/.claude"
	}
	return account.ConfigDir(strings.ToValidUTF8(raw, "�"), home)
}
