//go:build !selfupdate

package main

import (
	"fmt"
	"os"
)

// update 在默认编出的程序里只说明为什么不更新，返回 1。会自己更新的程序只在发版时带构建标签 selfupdate 编
// （.github/workflows/release.yml，见 update_release.go）：go install 装的，或别的程序当依赖编出来、带在身边的，
// 版本由装它的一方定，也不必带上联网的代码。
func update() int {
	fmt.Fprintf(os.Stderr, "claude-switch: 这份（%s）不是从 GitHub Release 装的，版本由装它的一方定（go install、dotfiles、Zeta 等），"+
		"不自己更新；要自己更新，从 https://github.com/dzshzx/claude-switch/releases 下载一份换掉它\n", buildVersion(version))
	return 1
}
