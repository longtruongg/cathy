#Requires -RunAsAdministrator
param(
    [ValidateSet("install", "uninstall", "start", "stop", "restart", "status")]
    [string]$Command = "install",
    [int]$Port = 8080
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $Root
$Exe = Join-Path $Root "cathy.exe"
$FirewallName = "Cathy Activity Tracker"

if (-not (Test-Path $Exe)) {
    Write-Error "cathy.exe not found in $Root. Build it first: go build -o cathy.exe ."
}

function Set-CathyFirewall {
    $existing = Get-NetFirewallRule -DisplayName $FirewallName -ErrorAction SilentlyContinue
    if (-not $existing) {
        New-NetFirewallRule -DisplayName $FirewallName -Direction Inbound -Protocol TCP -LocalPort $Port -Action Allow | Out-Null
        Write-Host "Opened inbound TCP $Port ($FirewallName)"
    }
}

switch ($Command) {
    "install" {
        $envFile = Join-Path $Root ".env"
        if (-not (Test-Path $envFile)) {
            Write-Warning ".env is missing. Copy .env.example to .env and fill in Gmail settings before the service can run."
        }
        & $Exe install
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
        Set-CathyFirewall
        & $Exe start
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
        Write-Host "Cathy service installed and started. Check status with: .\cathy.exe status"
    }
    "uninstall" {
        & $Exe stop
        & $Exe uninstall
        Get-NetFirewallRule -DisplayName $FirewallName -ErrorAction SilentlyContinue | Remove-NetFirewallRule
        Write-Host "Cathy service uninstalled"
    }
    default {
        & $Exe $Command
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    }
}
