param(
    [Parameter(Mandatory = $true)]
    [string]$ExecutablePath,
    [int]$TimeoutSeconds = 60,
    [int]$HealthySeconds = 5
)

$ErrorActionPreference = "Stop"

function Get-DescendantProcesses {
    param([int]$RootProcessId)

    $snapshot = @(Get-CimInstance Win32_Process | Select-Object ProcessId, ParentProcessId, Name, CommandLine)
    $pending = @([uint32]$RootProcessId)
    $seen = @{}
    $descendants = @()
    while ($pending.Count -gt 0) {
        $parentProcessId = [uint32]$pending[0]
        $pending = if ($pending.Count -gt 1) { @($pending[1..($pending.Count - 1)]) } else { @() }
        foreach ($child in @($snapshot | Where-Object { $_.ParentProcessId -eq $parentProcessId })) {
            $childProcessId = [uint32]$child.ProcessId
            if ($seen.ContainsKey($childProcessId)) {
                continue
            }
            $seen[$childProcessId] = $true
            $descendants += $child
            $pending += $childProcessId
        }
    }
    return @($descendants)
}

function Get-NativeSmokeState {
    param([System.Diagnostics.Process]$Process)

    $Process.Refresh()
    $descendants = @(Get-DescendantProcesses -RootProcessId $Process.Id)
    $renderer = @($descendants | Where-Object {
        $_.Name -ieq "msedgewebview2.exe" -and $_.CommandLine -match "--type=renderer"
    })
    return [pscustomobject]@{
        Exited = $Process.HasExited
        WindowHandle = if ($Process.HasExited) { [IntPtr]::Zero } else { $Process.MainWindowHandle }
        RendererCount = $renderer.Count
        Descendants = @($descendants | ForEach-Object { "$($_.Name)[$($_.ProcessId)]" })
    }
}

$exe = (Resolve-Path $ExecutablePath).Path
$tempRoot = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { [IO.Path]::GetTempPath() }
$smokeRoot = Join-Path $tempRoot ("reasonix-webview2-native-" + [guid]::NewGuid().ToString("N"))
$smokeHome = Join-Path $smokeRoot "home"
$smokeState = Join-Path $smokeRoot "state"
$smokeCache = Join-Path $smokeRoot "cache"
New-Item -ItemType Directory -Path $smokeHome, $smokeState, $smokeCache | Out-Null
Set-Content -LiteralPath (Join-Path $smokeHome "config.toml") -Encoding utf8 -Value @"
[desktop]
close_behavior = "quit"
"@

$oldHome = $env:REASONIX_HOME
$oldStateHome = $env:REASONIX_STATE_HOME
$oldCacheHome = $env:REASONIX_CACHE_HOME
$process = $null
try {
    $env:REASONIX_HOME = $smokeHome
    $env:REASONIX_STATE_HOME = $smokeState
    $env:REASONIX_CACHE_HOME = $smokeCache
    $process = Start-Process -FilePath $exe -WorkingDirectory (Split-Path $exe) -PassThru

    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    $readyState = $null
    while ([DateTime]::UtcNow -lt $deadline) {
        $state = Get-NativeSmokeState -Process $process
        if ($state.Exited) {
            throw "Reasonix exited before the native window became healthy (exit code $($process.ExitCode))"
        }
        if ($state.WindowHandle -ne [IntPtr]::Zero -and $state.RendererCount -gt 0) {
            $readyState = $state
            break
        }
        Start-Sleep -Milliseconds 250
    }
    if ($null -eq $readyState) {
        $state = Get-NativeSmokeState -Process $process
        throw "Reasonix did not expose a main window plus WebView2 renderer within $TimeoutSeconds seconds; descendants=$($state.Descendants -join ', ')"
    }

    $healthyDeadline = [DateTime]::UtcNow.AddSeconds($HealthySeconds)
    while ([DateTime]::UtcNow -lt $healthyDeadline) {
        $state = Get-NativeSmokeState -Process $process
        if ($state.Exited) {
            throw "Reasonix exited during the $HealthySeconds-second health window (exit code $($process.ExitCode))"
        }
        if ($state.WindowHandle -eq [IntPtr]::Zero -or $state.RendererCount -eq 0) {
            throw "Reasonix lost its main window or WebView2 renderer during the health window; descendants=$($state.Descendants -join ', ')"
        }
        Start-Sleep -Milliseconds 250
    }

    if (-not $process.CloseMainWindow()) {
        throw "Reasonix main window rejected the graceful close request"
    }
    if (-not $process.WaitForExit(10000)) {
        throw "Reasonix did not exit within 10 seconds after the graceful close request"
    }
    if ($process.ExitCode -ne 0) {
        throw "Reasonix exited with code $($process.ExitCode) after the graceful close request"
    }

    Write-Host "Wails/WebView2 native startup smoke passed (window + renderer healthy for $HealthySeconds seconds)"
}
finally {
    $env:REASONIX_HOME = $oldHome
    $env:REASONIX_STATE_HOME = $oldStateHome
    $env:REASONIX_CACHE_HOME = $oldCacheHome
    if ($null -ne $process -and -not $process.HasExited) {
        & taskkill.exe /PID $process.Id /T /F 2>$null | Out-Null
    }
    if (Test-Path $smokeRoot) {
        Remove-Item -LiteralPath $smokeRoot -Recurse -Force
    }
}
