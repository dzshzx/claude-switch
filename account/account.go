// Package account 是 claude-switch 的切换记录与名字规则，给 claude-switch 自己和别的程序（比如按账号统计会话的
// Claude Code hook）共用。
//
// 切换记录是 ~/.local/state/claude-switch/current，内容是当前的账号名或非账号目标名，加一个换行。
// 没有这个文件、或名字不合 Valid 时算 Default。
//
// 一个 Claude Code 进程属于哪个名字（Name）：用默认配置目录（CLAUDE_CONFIG_DIR 未设、为空，或解析后等于 ~/.claude）
// 时取切换记录；另设配置目录时取目录名去掉开头的点。claude-switch 是冷切换（换号时没有 Claude Code 在跑），
// 所以一个进程运行期间这个名字不变。
package account

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/dzshzx/claude-switch/internal/safefile"
)

// Default 是没有切换记录时的名字，也是默认配置目录里第一个登录的账号的名字。
const Default = "claude"

// StateDir 是 claude-switch 的状态目录（相对 HOME），切换记录 current 在其中。
const StateDir = ".local/state/claude-switch"

const currentFile = StateDir + "/current"

// namePattern 是名字的写法：小写字母、数字和连字符，字母或数字开头，至多 32 个字符。
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// reservedNames 不能当名字：claude-switch 的子命令，以及状态目录里别的文件的名字（存档是 <账号名>.json）。
var reservedNames = []string{"add", "remove", "version", "accounts", "pending"}

// Valid 报告 name 能不能当账号名或非账号目标名：合写法、不是保留名。
func Valid(name string) bool {
	return namePattern.MatchString(name) && !slices.Contains(reservedNames, name)
}

// Name 返回本进程（按 HOME 与 CLAUDE_CONFIG_DIR）所属的名字。
func Name() string {
	home, _ := os.UserHomeDir()
	if raw := os.Getenv("CLAUDE_CONFIG_DIR"); raw != "" {
		resolved := ConfigDir(raw, home)
		if resolved != ConfigDir(filepath.Join(home, ".claude"), home) {
			return strings.TrimLeft(filepath.Base(resolved), ".")
		}
	}
	return Current(home)
}

// Current 读 home 下的切换记录：没有记录或名字不合 Valid 时是 Default。不看 CLAUDE_CONFIG_DIR。
func Current(home string) string {
	data, err := os.ReadFile(filepath.Join(home, currentFile))
	if err != nil {
		return Default
	}
	name := strings.TrimSpace(string(data))
	if Valid(name) {
		return name
	}
	return Default
}

// WriteCurrent 把切换记录整份换成 name 加换行；状态目录由调用方先建好。
func WriteCurrent(home, name string) error {
	return safefile.WriteAtomic(filepath.Join(home, currentFile), []byte(name+"\n"))
}

// ConfigDir 把配置目录的写法解析成真实路径：按 home 展开 ~ 与 ~/…，存在的前缀解析符号链接。
func ConfigDir(raw, home string) string {
	return realpath(expandUser(raw, home))
}

// expandUser 只展开 "~" 与 "~/…"；"~用户名" 这种写法原样返回。
func expandUser(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

// realpath 是不要求路径存在的 realpath：存在的前缀解析符号链接，不存在的后半段原样接上。
func realpath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	parent := filepath.Dir(abs)
	if parent == abs {
		return abs
	}
	return filepath.Join(realpath(parent), filepath.Base(abs))
}
