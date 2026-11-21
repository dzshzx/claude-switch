# claude-switch 项目约定

公开仓：提交说明、文档、注释与测试数据按公开可见来写，不写本机路径、私有域名、主机名、真实邮箱或令牌。测试只用假数据；推送前的 gitleaks 扫描命中测试位置的假值时，按值登记在仓根 `.gitleaks-test-values.toml`。

- 门禁：`bash scripts/check.sh`。`scripts/gatelib.sh`、`scripts/format.sh` 是多个仓共用的同一份（由 `gate-sync` 写入），不在本仓单独改；本仓启用哪些格式工具写在 `scripts/format.conf`。
- 改到一半只跑测试：`go test -count=1 ./...`。
- 兼容：有程序读 `--json` 的字段、退出码、标准错误的 `claude-switch: ` 前缀与「还有 Claude Code 进程在跑」「没有叫」两句，以及 `~/.local/state/claude-switch/` 里各文件的格式（已有的存档换新版本后要直接能用）。改动只能向后兼容；`TestJSONFieldNames` 固定字段名。
- `account` 是导出的 Go 包，别的程序按它认账号；改它的导出接口要向后兼容。
- 程序只用标准库；要加第三方依赖先登记 `go-deps-allowlist.txt`。
- 默认分支 `master`。
