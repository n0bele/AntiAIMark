# 上架 Claude 社区插件目录（Discover）提交清单

目标：让 antiaimark 出现在 Claude Code 的 `/plugin` Discover 标签页和 claude.com/plugins 上。
提交后由 Anthropic 自动校验 + 安全审查，通过后收录进社区目录 `anthropics/claude-plugins-community`（marketplace 名为 `claude-community`），每晚同步。

## 提交入口

- 表单地址：**clau.de/plugin-directory-submission**（claude.ai / Claude Console 内的提交流程）
- 直接向 `anthropics/claude-plugins-community` 开 PR 无效，会被自动关闭，必须走表单

## 提交前自查清单（全部满足再提交）

- [ ] 代码已推送到 GitHub，默认分支为 `main`
- [ ] 仓库根存在 `.claude-plugin/marketplace.json`，格式合法
- [ ] 本地已跑通官方校验：`claude plugin validate .` → Validation passed（CI 里也会自动跑，见 `.github/workflows/ci.yml`）
- [ ] 已发布一个 `v*` 标签，GitHub Releases 有 7 个平台的预编译二进制（`antiaimark-mcp` 等），供插件 stdio 拉起
- [ ] marketplace / plugin 名称均满足 kebab-case，未使用 Anthropic 保留名
- [ ] 插件描述准确、无夸大宣传；许可证明确（MIT）
- [ ] 文档/README 说明安装前置条件（先装二进制到 PATH，见 `plugins/antiaimark/README.md`）

## 表单需要准备的资料

| 项目 | 内容 |
| --- | --- |
| 仓库地址 | `n0bele/AntiAIMark` |
| 插件名 | `antiaimark`（kebab-case） |
| Marketplace 名 | `antiaimark` |
| 版本 | `1.0.0`（每次更新需 bump） |
| 简介 | Detect and strip AI provenance marks: invisible Unicode steganography, C2PA/EXIF/XMP metadata, container metadata (PDF/DOCX/ODT/SVG/HTML/Markdown, best-effort video), vendor keywords — via MCP tools (inspect_file, clean_file, inspect_text, clean_text, capabilities) |
| 分类 | MCP / Privacy & content |
| 作者 | n0bele |
| 许可证 | MIT |
| 二进制说明 | 纯 Go 静态编译，来自本仓库 GitHub Releases，无运行时依赖；源码完全开源 |

## 提交后

- 自动校验 + 安全审查由 Anthropic 完成，通过后插件被 **pin 到提交 SHA**（保证可复现）
- 收录后每晚同步进 `claude-community` 目录，用户安装：
  ```bash
  claude plugin marketplace add anthropics/claude-plugins-community
  claude plugin install antiaimark@claude-community
  ```
- 状态查询：表单提交后关注表单给出的状态链接；同步生效最长约 24h

## 常见被拒/退回原因

- `claude plugin validate` 不通过（提交审核用的就是同一个检查）
- 仓库里没有可执行文件 / Releases 缺平台二进制，MCP server 无法拉起
- 描述与实际能力不符，或出现"去除版权水印"等高风险表述（建议强调"检测与个人内容清理"用途）
- 名称使用保留词或伪装官方
- 版本不更新导致用户收不到新版本

## 更新插件

- 修改代码 → bump `marketplace.json` 与 `plugin.json` 的 `version` → 推送 → 重新走表单提交（或按表单提示的更新流程），审核通过后同步
