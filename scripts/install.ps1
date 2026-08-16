# install.ps1 — download the antiaimark prebuilt binaries from the latest
# GitHub release and install them into ANTIAIMARK_INSTALL_DIR (default
# %LOCALAPPDATA%\antiaimark\bin), add that to the user PATH, then print the
# registration commands for every AI IDE.
#
# Usage:
#   irm https://raw.githubusercontent.com/n0bele/AntiAIMark/main/scripts/install.ps1 | iex
#   # or local copy:  .\scripts\install.ps1
#   # pick a specific release:  $env:ANTIAIMARK_VERSION = "1.2.3"; .\scripts\install.ps1
#   # install elsewhere:         $env:ANTIAIMARK_INSTALL_DIR = "D:\tools\antiaimark"

$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repo = "n0bele/AntiAIMark"
$Version = if ($env:ANTIAIMARK_VERSION) { $env:ANTIAIMARK_VERSION } else { "latest" }
$Dest = if ($env:ANTIAIMARK_INSTALL_DIR) { $env:ANTIAIMARK_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "antiaimark\bin" }

# -- detect architecture ------------------------------------------------------
$Arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { "amd64" }
  "ARM64" { "arm64" }
  default { throw "unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}
$Target = "antiaimark-windows-$Arch"

# -- download -----------------------------------------------------------------
if ($Version -eq "latest") {
  $Url = "https://github.com/$Repo/releases/latest/download/$Target.zip"
} else {
  $Tag = if ($Version.StartsWith("v")) { $Version } else { "v$Version" }
  $Url = "https://github.com/$Repo/releases/download/$Tag/$Target.zip"
}
Write-Host "==> downloading $Url" -ForegroundColor Green

$Tmp = Join-Path $env:TEMP ("antiaimark-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $Tmp | Out-Null
$Zip = Join-Path $Tmp "$Target.zip"
try {
  Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $Zip
} catch {
  throw "download failed: $($_.Exception.Message)`n(was a release published? see .github/workflows/release.yml)"
}
Expand-Archive -Path $Zip -DestinationPath $Tmp -Force

# -- install ------------------------------------------------------------------
Write-Host "==> installing binaries into $Dest" -ForegroundColor Green
New-Item -ItemType Directory -Path $Dest -Force | Out-Null
Copy-Item -Path (Join-Path $Tmp "$Target\*") -Destination $Dest -Force
Write-Host "==> done: $Dest\antiaimark-mcp.exe, $Dest\antiaimark-server.exe"

# -- PATH ---------------------------------------------------------------------
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (-not $UserPath.Split(";") -contains $Dest) {
  [Environment]::SetEnvironmentVariable("Path", "$UserPath;$Dest", "User")
  Write-Host "==> added $Dest to your user PATH (reopen the terminal to use it)" -ForegroundColor Yellow
  $env:Path += ";$Dest"
}

# -- IDE registration ---------------------------------------------------------
$McpBin = Join-Path $Dest "antiaimark-mcp.exe"
$ServerUrl = "http://127.0.0.1:8765/mcp"
@"

============================================================
  antiaimark MCP is ready - register it in any AI IDE
  (stdio) : $McpBin
  (HTTP)  : $ServerUrl   (run antiaimark-server first)
============================================================

Claude Code:
  claude mcp add antiaimark -- $McpBin
  # HTTP:  claude mcp add antiaimark --transport http $ServerUrl

Cursor / Windsurf / Cline / Zed - project root .mcp.json:
  { "mcpServers": { "antiaimark": { "command": "$McpBin" } } }
  # HTTP:  { "mcpServers": { "antiaimark": { "type": "http", "url": "$ServerUrl" } } }

Claude Desktop - claude_desktop_config.json:
  { "mcpServers": { "antiaimark": { "command": "$McpBin" } } }

Cline (VS Code) / Continue / VS Code Copilot / JetBrains AI:
  add "antiaimark" in their MCP settings, same command or $ServerUrl

Full per-IDE steps: docs/MCP.md
"@ | Write-Host

Remove-Item -Recurse -Force $Tmp
