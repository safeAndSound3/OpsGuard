param(
    [string]$SourceRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path,
    [string]$OutputRoot = (Join-Path $PSScriptRoot '..\dist\test-release-20261015-v7'),
    [string]$BinaryRoot = (Join-Path $PSScriptRoot '..\dist\test-release-20261015-v7\binaries')
)

$ErrorActionPreference = 'Stop'
$SourceRoot = (Resolve-Path $SourceRoot).Path
$oldRelease = Join-Path $SourceRoot 'dist\test-release-20261015'
New-Item -ItemType Directory -Force -Path $OutputRoot | Out-Null

foreach ($arch in @('amd64', 'arm64')) {
    $stageRoot = Join-Path $OutputRoot $arch
    if (Test-Path $stageRoot) { throw "目标目录已存在，避免覆盖：$stageRoot" }
    $install = Join-Path $stageRoot 'opsguard\install'
    foreach ($part in @('server\config')) {
        New-Item -ItemType Directory -Force -Path (Join-Path $install $part) | Out-Null
    }

    $files = @{
        'scripts\install-opsguard.sh' = 'install.sh'
        'backend\config\opsguard.conf.example' = 'server\config\opsguard.conf.example'
    }
    foreach ($entry in $files.GetEnumerator()) {
        Copy-Item -LiteralPath (Join-Path $SourceRoot $entry.Key) -Destination (Join-Path $install $entry.Value)
    }
    Copy-Item -LiteralPath (Join-Path (Join-Path $BinaryRoot $arch) 'server\opsguard-server') -Destination (Join-Path $install 'server\opsguard-server')
    Copy-Item -LiteralPath (Join-Path (Join-Path $oldRelease $arch) 'release-info') -Destination (Join-Path $install 'release-info')

    $readme = @'
OpsGuard Linux 测试发布包

解压后仅有 opsguard/install。执行安装脚本后，在 opsguard 下生成 server、logs、data 等目录。
安装根目录由脚本位置确定，不依赖 /opt/opsguard。

进入解压后的 opsguard 目录，然后执行：
服务端首次安装：sudo bash install/install.sh install
服务端升级：sudo bash install/install.sh upgrade
服务端节点无需安装 Agent。install 目录内的 opsguard-agent-*.zip 可单独复制到其他节点。
Agent ZIP 解压后，进入 opsguard 目录运行 sudo sh install/install-agent.sh。
现有服务端配置在升级时保留。
'@
    [System.IO.File]::WriteAllText((Join-Path $install 'README-DEPLOY.txt'), $readme.Replace("`r`n", "`n"), [System.Text.UTF8Encoding]::new($false))

    foreach ($script in @('install.sh', 'release-info')) {
        $path = Join-Path $install $script
        $content = [System.IO.File]::ReadAllText($path).Replace("`r`n", "`n")
        [System.IO.File]::WriteAllText($path, $content, [System.Text.UTF8Encoding]::new($false))
    }

    $agentStage = Join-Path $OutputRoot "agent-$arch"
    $agentInstall = Join-Path $agentStage 'opsguard\install'
    foreach ($part in @('agent\deploy')) {
        New-Item -ItemType Directory -Force -Path (Join-Path $agentInstall $part) | Out-Null
    }
    $agentFiles = @{
        'scripts\install-agent.sh' = 'install-agent.sh'
        'agent\deploy\install.sh' = 'agent\deploy\install.sh'
        'agent\README.md' = 'agent\README.md'
    }
    foreach ($entry in $agentFiles.GetEnumerator()) {
        Copy-Item -LiteralPath (Join-Path $SourceRoot $entry.Key) -Destination (Join-Path $agentInstall $entry.Value)
    }
    Copy-Item -LiteralPath (Join-Path (Join-Path $BinaryRoot $arch) 'agent\opsguard-agent') -Destination (Join-Path $agentInstall 'agent\opsguard-agent')
    Copy-Item -LiteralPath (Join-Path (Join-Path $oldRelease $arch) 'release-info') -Destination (Join-Path $agentInstall 'release-info')
    foreach ($script in @('install-agent.sh', 'agent\deploy\install.sh', 'release-info')) {
        $path = Join-Path $agentInstall $script
        $content = [System.IO.File]::ReadAllText($path).Replace("`r`n", "`n")
        [System.IO.File]::WriteAllText($path, $content, [System.Text.UTF8Encoding]::new($false))
    }
    $agentZip = Join-Path $install "opsguard-agent-20261015-linux-$arch.zip"
    Compress-Archive -LiteralPath (Join-Path $agentStage 'opsguard') -DestinationPath $agentZip -CompressionLevel Optimal

    $zip = Join-Path $OutputRoot "opsguard-20261015-linux-$arch-v7.zip"
    if (Test-Path $zip) { throw "目标 ZIP 已存在，避免覆盖：$zip" }
    Compress-Archive -LiteralPath (Join-Path $stageRoot 'opsguard') -DestinationPath $zip -CompressionLevel Optimal
    Write-Output $zip
}
