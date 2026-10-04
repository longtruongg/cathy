#Requires -RunAsAdministrator
# Windows PowerShell 5.1 (Windows 10). If execution policy blocks this file:
#   powershell -ExecutionPolicy Bypass -File .\install-windows-service.ps1
param(
    [ValidateSet("install", "uninstall", "start", "stop", "restart", "status")]
    [string]$Command = "install",
    [int]$Port = 8080
)

$ErrorActionPreference = "Stop"
# cathy listens on this port. main.go hardcodes listenAddr = ":8080".
$ListenPort = 8080
if ($Port -ne $ListenPort) {
    Write-Error "Cathy listens on TCP $ListenPort (fixed in main.go). Refusing -Port $Port so the firewall rule cannot point at the wrong port."
}

$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $Root
$Exe = Join-Path $Root "cathy.exe"
$ServiceName = "Cathy"
$FirewallName = "Cathy Activity Tracker"
$MdnsFirewallName = "Cathy mDNS"

if (-not (Test-Path $Exe)) {
    Write-Error "cathy.exe not found in $Root. On the build machine: GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o cathy.exe ."
}

# Copied from another PC, the exe is often marked blocked (Zone.Identifier).
Unblock-File -Path $Exe -ErrorAction SilentlyContinue

function Invoke-Cathy {
    param([Parameter(Mandatory = $true)][string]$Cmd)
    & $Exe $Cmd
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
}

function Test-CathyEnv {
    $envFile = Join-Path $Root ".env"
    if (-not (Test-Path $envFile)) {
        Write-Error ".env is missing next to cathy.exe. Create it with APP_PASSWORD, TO_MAIL, and FROM_MAIL before the service starts. Without it, Windows restarts Cathy every 5 seconds."
    }
    $keys = @{}
    foreach ($line in Get-Content -Path $envFile) {
        $trimmed = $line.Trim()
        if ($trimmed -eq "" -or $trimmed.StartsWith("#")) {
            continue
        }
        $eq = $trimmed.IndexOf("=")
        if ($eq -lt 1) {
            continue
        }
        $name = $trimmed.Substring(0, $eq).Trim()
        $value = $trimmed.Substring($eq + 1).Trim().Trim('"').Trim("'")
        $keys[$name] = $value
    }
    foreach ($name in @("APP_PASSWORD", "TO_MAIL", "FROM_MAIL")) {
        if (-not $keys.ContainsKey($name) -or [string]::IsNullOrWhiteSpace($keys[$name])) {
            Write-Error ".env is missing a value for $name"
        }
    }
}

function Set-CathyFirewall {
    Get-NetFirewallRule -DisplayName $FirewallName -ErrorAction SilentlyContinue |
        Remove-NetFirewallRule
    # Port rule, not -Program: Windows often does not match multicast sockets to a program rule.
    New-NetFirewallRule -DisplayName $FirewallName -Direction Inbound -Action Allow `
        -Profile Any -Protocol TCP -LocalPort $ListenPort -RemoteAddress LocalSubnet |
        Out-Null
    Write-Host "Opened inbound TCP $ListenPort from the local subnet ($FirewallName)"

    Get-NetFirewallRule -DisplayName $MdnsFirewallName -ErrorAction SilentlyContinue |
        Remove-NetFirewallRule
    New-NetFirewallRule -DisplayName $MdnsFirewallName -Direction Inbound -Action Allow `
        -Profile Any -Protocol UDP -LocalPort 5353 -RemoteAddress LocalSubnet |
        Out-Null
    Write-Host "Opened inbound UDP 5353 from the local subnet ($MdnsFirewallName)"
}

function Remove-CathyFirewall {
    Get-NetFirewallRule -DisplayName $FirewallName -ErrorAction SilentlyContinue |
        Remove-NetFirewallRule
    Get-NetFirewallRule -DisplayName $MdnsFirewallName -ErrorAction SilentlyContinue |
        Remove-NetFirewallRule
}

switch ($Command) {
    "install" {
        Test-CathyEnv
        $existing = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
        if (-not $existing) {
            Invoke-Cathy "install"
        } else {
            Write-Host "Service $ServiceName is already installed"
        }
        Set-CathyFirewall
        $existing = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
        if ($existing -and $existing.Status -eq "Running") {
            Write-Host "Cathy service is already running"
        } else {
            Invoke-Cathy "start"
        }
        Write-Host "Cathy service installed and started. Check status with: .\cathy.exe status"
    }
    "uninstall" {
        $existing = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
        if ($existing) {
            if ($existing.Status -eq "Running") {
                Invoke-Cathy "stop"
            }
            Invoke-Cathy "uninstall"
        } else {
            Write-Host "Service $ServiceName is not installed"
        }
        Remove-CathyFirewall
        Write-Host "Cathy service uninstalled"
    }
    "start" {
        Test-CathyEnv
        Invoke-Cathy "start"
    }
    "restart" {
        Test-CathyEnv
        Invoke-Cathy "restart"
    }
    default {
        Invoke-Cathy $Command
    }
}
