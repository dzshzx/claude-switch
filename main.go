// claude-switch 在本机多个 claude.ai 账号与非账号目标之间给 Claude Code 冷切换。用法与退出码见 README.md
// 与 internal/claudeswitch 的包注释。
//
//	claude-switch [--json]              显示当前账号或目标、已存档的账号与非账号目标
//	claude-switch <名字>                换到这个账号或非账号目标
//	claude-switch add|remove <账号名>   加一个账号，或删掉一个不在用的账号的存档
//	claude-switch version               打印版本
//	claude-switch update                从 GitHub Release 装的这份换成最新的 Release
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/dzshzx/claude-switch/internal/claudeswitch"
	"github.com/dzshzx/claude-switch/internal/selfupdate"
)

const modulePath = "github.com/dzshzx/claude-switch"

// version 可在构建时经 -ldflags "-X main.version=…" 注入；没注入时取构建信息里本模块的版本。
var version = ""

// channel 是 release 时表示这份是发版时编进 Release 的，能用 update 自己更新（scripts/release.sh 注入）。
var channel = ""

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println("claude-switch", buildVersion(version))
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "update" {
		os.Exit(update())
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "claude-switch: 取不到 HOME：", err)
		os.Exit(2)
	}
	os.Exit(claudeswitch.New(home).Run(os.Args[1:]))
}

// buildVersion 是打印的版本：注入的优先；go install …@版本 时是主模块的版本，作为别的模块的依赖编出来时
// （go build github.com/dzshzx/claude-switch）是依赖里本模块的版本；都没有时是 dev。
func buildVersion(injected string) string {
	if injected != "" {
		return injected
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	mods := append([]*debug.Module{&info.Main}, info.Deps...)
	for _, m := range mods {
		if m.Path != modulePath {
			continue
		}
		if m.Replace != nil {
			m = m.Replace
		}
		if m.Version != "" && m.Version != "(devel)" {
			return m.Version
		}
	}
	return "dev"
}

// update 跑 claude-switch update，返回退出码：0 更新了或已是最新，1 这份不是从 Release 装的，2 出错（原来的程序不动）。
func update() int {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "claude-switch: 找不到自己的程序文件：", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	u := &selfupdate.Updater{
		API:     selfupdate.DefaultAPI,
		Client:  &http.Client{Timeout: 3 * time.Minute},
		Exe:     exe,
		Current: buildVersion(version),
		Release: channel == "release",
		Arch:    runtime.GOARCH,
		Stdout:  os.Stdout,
	}
	err = u.Run(ctx)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, selfupdate.ErrNotRelease):
		fmt.Fprintf(os.Stderr, "claude-switch: 这份（%s，%s）不是从 GitHub Release 装的，版本由装它的一方定（go install、dotfiles、Zeta 等），"+
			"不自己更新；要自己更新，从 https://github.com/dzshzx/claude-switch/releases 下载一份换掉它\n", u.Current, exe)
		return 1
	default:
		fmt.Fprintln(os.Stderr, "claude-switch: 没有更新：", err)
		return 2
	}
}
