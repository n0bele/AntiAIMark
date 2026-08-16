# 将 antiaimark MCP 接入各 AI IDE

antiaimark 以 **MCP（Model Context Protocol）** 标准接入 AI IDE，支持两种传输方式：

| 方式 | 用途 | 注册形式 |
| --- | --- | --- |
| **stdio** | 本机安装 `antiaimark-mcp` 二进制，由 IDE 拉起 | `"command": "<绝对路径>/antiaimark-mcp"` |
| **Streamable HTTP** | 连接已运行的 `antiaimark-server`（本机或服务器） | `"type": "http", "url": "http://<host>:8765/mcp"` |

> 建议：**团队/多 IDE 场景直接用 HTTP 方式**——部署一次 `antiaimark-server`，所有 IDE 填同一个 URL 即可，无需每台机器装二进制。

## 1. 准备工作

### 安装二进制（stdio 方式需要）

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/n0bele/AntiAIMark/main/scripts/install.sh | bash
```

```powershell
# Windows（PowerShell）
irm https://raw.githubusercontent.com/n0bele/AntiAIMark/main/scripts/install.ps1 | iex
```

脚本会自动下载对应平台（win/mac/linux × amd64/arm64）的预编译产物，并打印各 IDE 的注册命令。

### 启动 HTTP 服务（HTTP 方式需要）

```bash
./bin/antiaimark-server                       # 本机 127.0.0.1:8765
ANTIAIMARK_SERVER_API_KEY=xxx ./bin/antiaimark-server   # 加鉴权后填 URL 也要带 key
# Docker:  docker compose up -d
```

部署到服务器后，把下面的 `127.0.0.1` 换成服务器地址，并确保 CORS/端口可达。

## 2. 各 IDE 注册步骤

### Claude Code

```bash
# stdio
claude mcp add antiaimark -- /abs/path/to/antiaimark-mcp
# HTTP（先启动 antiaimark-server）
claude mcp add antiaimark --transport http http://127.0.0.1:8765/mcp
# 查看/移除
claude mcp list
claude mcp remove antiaimark
```

也可以写入项目根目录 `.mcp.json`：

```json
{ "mcpServers": { "antiaimark": { "command": "/abs/path/to/antiaimark-mcp" } } }
```

### Claude Desktop

编辑配置文件（Windows：`%APPDATA%\Claude\claude_desktop_config.json`；macOS：`~/Library/Application Support/Claude/claude_desktop_config.json`）：

```json
{ "mcpServers": { "antiaimark": { "command": "/abs/path/to/antiaimark-mcp" } } }
```

保存后重启 Claude Desktop。

### Cursor

- 方式 A（GUI）：Settings → **MCP** → **+ Add new MCP server** → 选择 HTTP 或 stdio，填入对应配置。
- 方式 B（项目级，推荐团队共享）：项目根目录创建 `.mcp.json`：

```json
{ "mcpServers": { "antiaimark": { "type": "http", "url": "http://127.0.0.1:8765/mcp" } } }
```

（stdio 时把 `type/url` 换成 `command: "/abs/path/to/antiaimark-mcp"`。）

### Windsurf

- GUI：Settings → **MCP** → **Add Server**。
- 或与 Cursor 相同的 `.mcp.json`（见上）。

### Cline（VS Code 扩展）

1. 安装 Cline 扩展，打开其面板。
2. **MCP Servers** → **Configure MCP Servers**，在 `cline_mcp_settings.json` 中添加：

```json
{ "mcpServers": { "antiaimark": { "command": "/abs/path/to/antiaimark-mcp" } } }
```

3. 回到面板点击 **antiaimark** 旁的连接按钮。

### Continue（VS Code / JetBrains）

编辑 `config.json`（VS Code：`~/.continue/config.json`；JetBrains：插件设置里）：

```json
{
  "mcpServers": [
    { "mcpId": "antiaimark", "command": "/abs/path/to/antiaimark-mcp" }
  ]
}
```

### Zed

项目根目录 `.zed/mcp.json` 或全局 `~/.config/zed/mcp.json`：

```json
{ "mcpServers": { "antiaimark": { "command": "/abs/path/to/antiaimark-mcp" } } }
```

### VS Code Copilot

在项目根目录 `.vscode/mcp.json`：

```json
{ "servers": { "antiaimark": { "type": "http", "url": "http://127.0.0.1:8765/mcp" } } }
```

然后在 Copilot Chat 中使用。详情见 VS Code 文档「Model Context Protocol servers」。

### JetBrains AI Assistant

Settings → **Tools** → **MCP Server** → 添加服务器：

- 名称 `antiaimark`，类型选 **HTTP**（填 `http://127.0.0.1:8765/mcp`）或 **Command**（填二进制绝对路径）。

### Trae

Trae 的 **MCP** 设置页 → **添加服务器** → 填 stdio command 或 HTTP URL。

### Codex CLI（OpenAI）

```bash
codex mcp add antiaimark -- /abs/path/to/antiaimark-mcp
# HTTP（先启动 antiaimark-server）
codex mcp add antiaimark --url http://127.0.0.1:8765/mcp
```

### WorkBuddy（腾讯）

桌面版（Windows/macOS，copilot.tencent.com 下载）通过标准 MCP 协议接入。

**方式 A — 配置文件**：编辑 `~/.workbuddy/mcp.json`：

```json
{
  "mcpServers": {
    "antiaimark": {
      "command": "/abs/path/to/antiaimark-mcp"
    }
  }
}
```

HTTP 模式则替换为 `"url": "http://127.0.0.1:8765/mcp"`。

**方式 B — 界面操作**：设置 → **连接器** → 添加连接器 → **自定义连接器** → 配置 MCP，粘贴上面的 JSON → 保存。

注意：配置后需在连接器列表里**手动打开开关启用**，然后让 WorkBuddy 执行"查看可用的 MCP 工具"来验证。

**SkillHub MCP 广场**：可到 <https://skillhub.workbuddy.cn/mcp> 一键导入 antiaimark（后续可把本 server 提交到广场供所有 WorkBuddy 用户使用）。

### Gemini CLI（Google）

```bash
gemini config set mcpServers.antiaimark.command "/abs/path/to/antiaimark-mcp"
```

### Amazon Q CLI

```bash
q mcp add antiaimark --command "/abs/path/to/antiaimark-mcp"
```

### DeepSeek Harness（deepseek-ai/dsh）

官方开源 Agent Harness（`npx @deepseek-ai/dsh web`，默认 http://127.0.0.1:3080）。Harness 采用"一切皆插件"架构（Cordis），**外部 MCP 通过自然语言提示词接入**：把下面这段提示词直接粘贴给 dsh，它会自行读取本文档并完成 antiaimark MCP 的安装与配置（配置后工具以 `mcp__antiaimark__*` 出现在任务轨迹中）：

```text
请接入 antiaimark 的 MCP 服务器（用于检测/去除 AI 溯源标记）。
请先访问 https://raw.githubusercontent.com/n0bele/AntiAIMark/main/docs/MCP.md 阅读接入说明，
然后完成 antiaimark-mcp 的安装与注册：
1. 若本机没有 antiaimark-mcp 可执行文件，先按 install 脚本安装到 PATH；
2. 采用 stdio 方式注册 antiaimark MCP 服务器；
3. 注册完成后调用 capabilities 验证连接成功。
```

> 提示：dsh 目前是 **developer preview**（v0.1.0-rc.x），插件与配置 API 会破坏性变更；如需把 antiaimark 做成一键安装的 `dsh-plugin`，可关注官方 `dsh-plugin` 生态，本 server 为标准 stdio/HTTP MCP，协议层兼容。

## 3. 可用的工具

| 工具 | 作用 |
| --- | --- |
| `capabilities` | 检查可选工具 / 像素后端的可用状态 |
| `inspect_file` | 检查单个本地文件（自动识别文本/图片/容器） |
| `clean_file` | 清洗单个本地文件（输出 `*.cleaned.*` 或原地 + `.bak`） |
| `inspect_text` | 检查文本中的不可见 Unicode 隐写 |
| `clean_text` | 清洗文本，返回清洗结果与统计 |

工具描述会随 IDE 的语言（en/zh/es/fr/ru）自动本地化。

## 4. 常见问题

- **HTTP 方式连不上？** 确认 `antiaimark-server` 已启动（`curl http://127.0.0.1:8765/health`）；服务器部署时 URL 用公网/内网地址，且端口已放行。
- **设置了 API Key？** `ANTIAIMARK_SERVER_API_KEY` 非空时，MCP 请求也需要在客户端配置认证（各 IDE 的 MCP 设置支持加 header）。未配置则保持 API Key 为空。
- **工具列表为空？** 部分 IDE 需要在 `initialize` 完成后刷新/重启才加载工具。
- **没有预编译产物？** 仓库打 tag（`git tag v1.0.0 && git push origin v1.0.0`）触发 Release 工作流，产物发布到 GitHub Releases。
