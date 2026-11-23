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
	"fmt"
	"os"
	"runtime/debug"

	"github.com/dzshzx/claude-switch/internal/claudeswitch"
)

const modulePath = "github.com/dzshzx/claude-switch"

// version 可在构建时经 -ldflags "-X main.version=…" 注入；没注入时取构建信息里本模块的版本。
var version = ""

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
