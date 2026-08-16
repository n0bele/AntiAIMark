# antiaimark — Claude Code 插件

检测并去除文本、图片、文档、视频、音频中的 AI 溯源标记（不可见 Unicode 隐写、C2PA/EXIF/XMP 元数据、容器元数据、厂商关键词等），以 MCP 工具形式提供给 Claude Code：

- `capabilities` — 查看当前可用的检测/清理能力
- `inspect_file` / `clean_file` — 检测 / 清理文件（图片、PDF、DOCX、视频等）
- `inspect_text` / `clean_text` — 检测 / 清理纯文本

## 前置条件（必须）

插件通过 stdio 以 `antiaimark mcp` 方式拉起二进制，请先安装到 PATH：

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/n0bele/AntiAIMark/main/scripts/install.sh | bash
export PATH="$HOME/.local/bin:$PATH"

# Windows (PowerShell)
irm https://raw.githubusercontent.com/n0bele/AntiAIMark/main/scripts/install.ps1 | iex
```

## 安装插件

方式 A — 图形界面：在 Claude Code 中输入 `/plugin`，打开 **Marketplaces** 标签页 → **Add marketplace** → 填入 `n0bele/AntiAIMark`，然后在 **Discover** 里安装 `antiaimark`。

方式 B — 命令行：

```bash
claude plugin marketplace add n0bele/AntiAIMark
claude plugin install antiaimark@antiaimark
```

方式 C — 本地测试（未推送 GitHub 时）：

```bash
claude plugin marketplace add /path/to/AntiAIMark
claude plugin install antiaimark@antiaimark
```

安装后若提示 reload：运行 `/reload-plugins`。

## 使用示例

```text
帮我清理这张图：inspect_file /path/to/photo.png 然后 clean_file
检查这段文字是否带 AI 水印：inspect_text "……"
```

## 服务端（HTTP）模式

如果想给团队共用或连接远程服务，可以改用 HTTP 模式注册 `antiaimark server`（部署一次，所有 IDE 填同一个 URL）：见仓库根目录 [docs/MCP.md](../../docs/MCP.md)。

## 发布到 Claude 插件社区目录（Discover 页）

上面的安装方式（`claude plugin marketplace add n0bele/AntiAIMark`）已经是一个"自有商店"，但用户不会主动来加。想让插件出现在 Claude Code 的 **Discover** 页和 [claude.com/plugins](https://claude.com/plugins) 里，需要提交到 Anthropic 官方维护的**社区插件目录**（marketplace 名为 `claude-community`，对应 `anthropics/claude-plugins-community` 仓库，每晚同步）：

1. 先把本仓库推送到 GitHub（default 分支），确保 `claude plugin validate` 通过（`.github/workflows/ci.yml` 已内置该校验）。
2. 打开官方提交表单：<https://clau.de/plugin-directory-submission>（Claude.ai / Claude Console 里也能找到入口），填写仓库地址 `n0bele/AntiAIMark` 和插件说明。
3. Anthropic 会跑自动校验与安全审查（与 `claude plugin validate` 同一套检查），通过后插件被固定到某个 commit SHA 并加入社区目录，最多约 24 小时生效。
4. 之后用户直接 `claude plugin marketplace add anthropics/claude-plugins-community`，再 `/plugin install antiaimark@claude-community` 即可在 Discover 页被发现。

> 提示：目录里的插件会固定到提交 SHA，每次更新插件（bump `marketplace.json`/`plugin.json` 里的 `version` 并推送）后，需要重新走一遍提交流程让新版本同步进目录。
