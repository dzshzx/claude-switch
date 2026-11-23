//go:build selfupdate

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/dzshzx/claude-switch/internal/selfupdate"
)

// update 跑 claude-switch update（只编进 Release 的程序，见 update.go），返回退出码：0 更新了或已是最新，
// 2 出错（原来的程序不动）。
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
		Arch:    runtime.GOARCH,
		Stdout:  os.Stdout,
	}
	if err := u.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "claude-switch: 没有更新：", err)
		return 2
	}
	return 0
}
