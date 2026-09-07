[CmdletBinding()]
param(
    [string]$HostAlias = "gopherai-aliyun",
    [string]$SshConfigPath = "$env:USERPROFILE\.ssh\config",
    [string]$ContainerName = "gopherai2",
    [string]$RemoteHostBundleDir = "/root/GopherAI_Deploy/bundles",
    [string]$RemoteContainerBundleDir = "/root/GopherAI_Deploy/bundles",
    [string]$ContainerProjectPath = "/root/GopherAI-",
    [string]$ContainerRuntimePath = "/root/GopherAI_Runtime",
    [string[]]$DependencyContainers = @("rabbitmq", "redis-vector", "gopherai2"),
    [switch]$SkipFrontend,
    [switch]$AllowFrontendDowntime,
    [switch]$DeployConfig,
    [switch]$BuildInContainer,
    [switch]$DryRun,
    [switch]$RunLocalTests
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

if ($SkipFrontend -and -not $AllowFrontendDowntime) {
    throw "-SkipFrontend stops the live Vue process during the atomic release. Add -AllowFrontendDowntime only when frontend downtime is intentional."
}

function Require-Command {
    param([string]$Name)
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "Required command '$Name' was not found in PATH."
    }
}

function Invoke-Checked {
    param(
        [string]$FilePath,
        [string[]]$Arguments,
        [string]$WorkingDirectory = (Get-Location).Path
    )

    Write-Host "[local] $FilePath $($Arguments -join ' ')"
    Push-Location $WorkingDirectory
    try {
        & $FilePath @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "Command failed with exit code ${LASTEXITCODE}: $FilePath"
        }
    }
    finally {
        Pop-Location
    }
}

function Quote-Bash {
    param([string]$Value)
    return "'" + ($Value -replace "'", "'\''") + "'"
}

function Assert-RemoteDeploymentCapacity {
    $capacityScript = @'
set -Eeuo pipefail
load_one="$(cut -d' ' -f1 /proc/loadavg)"
cores="$(nproc)"
load_limit="$((cores * 4))"
[ "$load_limit" -ge 4 ] || load_limit=4
io_full_avg10="$(awk '/^full / { for (i=1; i<=NF; i++) if ($i ~ /^avg10=/) { split($i, pair, "="); print pair[2] } }' /proc/pressure/io 2>/dev/null || true)"
[ -n "$io_full_avg10" ] || io_full_avg10=0
memory_available_kib="$(awk '/^MemAvailable:/ { print $2 }' /proc/meminfo)"
printf '[preflight] remote capacity: load1=%s/%s cores=%s io_full_avg10=%s%% mem_available=%s KiB\n' "$load_one" "$load_limit" "$cores" "$io_full_avg10" "$memory_available_kib"
awk -v current_load="$load_one" -v limit="$load_limit" -v io_pressure="$io_full_avg10" 'BEGIN { exit !((current_load + 0) <= (limit + 0) && (io_pressure + 0) <= 10) }' || {
  echo "remote capacity guard rejected deployment before upload; wait for load/I/O pressure to recover" >&2
  exit 75
}
'@
    Invoke-RemoteScript -Script $capacityScript
}

function Invoke-Remote {
    param([string]$Command)
    if ($DryRun) {
        Write-Host "[dry-run][ssh] $Command"
        return
    }
    Require-Command "ssh"
    Invoke-Checked -FilePath "ssh" -Arguments @(
        "-F", $SshConfigPath,
        "-o", "BatchMode=yes",
        "-o", "ConnectTimeout=30",
        "-o", "ServerAliveInterval=10",
        "-o", "ServerAliveCountMax=6",
        $HostAlias,
        $Command
    )
}

function Invoke-RemoteScript {
    param([string]$Script)
    if ($DryRun) {
        Write-Host "[dry-run][ssh stdin] bash -s ($([System.Text.Encoding]::UTF8.GetByteCount($Script)) bytes)"
        return
    }

    Require-Command "ssh"
    $sshPath = (Get-Command "ssh" -ErrorAction Stop).Source
    $arguments = @(
        "-F", $SshConfigPath,
        "-o", "BatchMode=yes",
        "-o", "ConnectTimeout=30",
        "-o", "ServerAliveInterval=10",
        "-o", "ServerAliveCountMax=6",
        $HostAlias,
        "bash", "-s"
    )
    Write-Host "[local] $sshPath $($arguments -join ' ') < remote-deploy-script"

    $startInfo = [System.Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $sshPath
    $startInfo.UseShellExecute = $false
    $startInfo.RedirectStandardInput = $true
    foreach ($argument in $arguments) {
        $startInfo.ArgumentList.Add($argument)
    }

    $process = [System.Diagnostics.Process]::new()
    $process.StartInfo = $startInfo
    try {
        if (-not $process.Start()) {
            throw "Failed to start ssh for remote script execution."
        }
        $process.StandardInput.Write($Script)
        $process.StandardInput.Close()
        $process.WaitForExit()
        if ($process.ExitCode -ne 0) {
            throw "Remote script failed with exit code $($process.ExitCode)."
        }
    }
    finally {
        $process.Dispose()
    }
}

function Upload-File {
    param([string]$LocalPath, [string]$RemotePath)
    if ($DryRun) {
        Write-Host "[dry-run][scp] $LocalPath -> $RemotePath"
        return
    }
    Require-Command "scp"
    Invoke-Checked -FilePath "scp" -Arguments @(
        "-F", $SshConfigPath,
        "-o", "BatchMode=yes",
        "-o", "ConnectTimeout=30",
        $LocalPath,
        "${HostAlias}:$RemotePath"
    )
}

function Set-GoBuildEnvironment {
    param([string]$GoExecutable, [string]$TempRoot)

    $goRoot = Split-Path -Parent (Split-Path -Parent $GoExecutable)
    $originalGoPath = [Environment]::GetEnvironmentVariable("GOPATH", "Process")
    $moduleCacheCandidates = @()
    if (-not [string]::IsNullOrWhiteSpace($env:GOMODCACHE)) {
        $moduleCacheCandidates += $env:GOMODCACHE
    }
    if (-not [string]::IsNullOrWhiteSpace($originalGoPath)) {
        $firstGoPath = ($originalGoPath -split [System.IO.Path]::PathSeparator)[0]
        $moduleCacheCandidates += (Join-Path $firstGoPath "pkg\mod")
    }
    $workspaceDrive = [System.IO.Path]::GetPathRoot($PSScriptRoot)
    if (-not [string]::IsNullOrWhiteSpace($workspaceDrive)) {
        $moduleCacheCandidates += (Join-Path $workspaceDrive "Golang\pkg\mod")
    }
    $moduleCacheCandidates += (Join-Path $env:USERPROFILE "go\pkg\mod")
    $moduleCacheCandidate = $moduleCacheCandidates |
        Where-Object { -not [string]::IsNullOrWhiteSpace($_) -and (Test-Path -LiteralPath $_) } |
        Select-Object -First 1
    if ($moduleCacheCandidate) {
        Write-Host "[build] module cache: $moduleCacheCandidate"
    }

    $env:GOENV = "off"
    $env:GOROOT = $goRoot
    $env:GOPATH = Join-Path $TempRoot "gopath"
    $env:GOCACHE = Join-Path ([System.IO.Path]::GetTempPath()) "gopherai-go-build-cache"
    $env:GOTOOLCHAIN = "local"
    Remove-Item Env:GO111MODULE -ErrorAction SilentlyContinue
    if ($moduleCacheCandidate) {
        $env:GOMODCACHE = $moduleCacheCandidate
    }
    else {
        Remove-Item Env:GOMODCACHE -ErrorAction SilentlyContinue
    }
    New-Item -ItemType Directory -Force -Path $env:GOPATH, $env:GOCACHE | Out-Null
}

function Build-LocalLinuxArtifacts {
    param([string]$GoExecutable, [string]$RepoRoot, [string]$ArtifactDirectory)

    New-Item -ItemType Directory -Force -Path $ArtifactDirectory | Out-Null
    $env:GOOS = "linux"
    $env:GOARCH = "amd64"
    $env:CGO_ENABLED = "0"
    Write-Host "[build] backend linux/amd64 CGO_ENABLED=0"
    Invoke-Checked -FilePath $GoExecutable -Arguments @(
        "-C", $RepoRoot, "build", "-p", "1", "-trimpath", "-ldflags=-s -w",
        "-o", (Join-Path $ArtifactDirectory "GopherAI"),
        "main.go", "pprof_server.go"
    )
    Write-Host "[build] mcp linux/amd64 CGO_ENABLED=0"
    Invoke-Checked -FilePath $GoExecutable -Arguments @(
        "-C", (Join-Path $RepoRoot "common\mcp"), "build", "-p", "1", "-trimpath", "-ldflags=-s -w",
        "-o", (Join-Path $ArtifactDirectory "gopherai-mcp"), "."
    )
    Write-Host "[build] index worker linux/amd64 CGO_ENABLED=0"
    Invoke-Checked -FilePath $GoExecutable -Arguments @(
        "-C", $RepoRoot, "build", "-p", "1", "-trimpath", "-ldflags=-s -w",
        "-o", (Join-Path $ArtifactDirectory "GopherAI-index-worker"),
        "./cmd/index-worker"
    )
    Write-Host "[build] static frontend gateway linux/amd64 CGO_ENABLED=0"
    Invoke-Checked -FilePath $GoExecutable -Arguments @(
        "-C", $RepoRoot, "build", "-p", "1", "-trimpath", "-ldflags=-s -w",
        "-o", (Join-Path $ArtifactDirectory "GopherAI-frontend"),
        "./cmd/frontend-gateway"
    )
    Write-Host "[build] collaboration evaluation runner linux/amd64 CGO_ENABLED=0"
    Invoke-Checked -FilePath $GoExecutable -Arguments @(
        "-C", $RepoRoot, "build", "-p", "1", "-trimpath", "-ldflags=-s -w",
        "-o", (Join-Path $ArtifactDirectory "GopherAI-collaboration-eval"),
        "./cmd/collaboration-eval"
    )
    Write-Host "[build] parent-context evaluation runner linux/amd64 CGO_ENABLED=0"
    Invoke-Checked -FilePath $GoExecutable -Arguments @(
        "-C", $RepoRoot, "build", "-p", "1", "-trimpath", "-ldflags=-s -w",
        "-o", (Join-Path $ArtifactDirectory "GopherAI-parent-context-eval"),
        "./cmd/parent-context-eval"
    )
    Write-Host "[build] unified evaluation runner linux/amd64 CGO_ENABLED=0"
    Invoke-Checked -FilePath $GoExecutable -Arguments @(
        "-C", $RepoRoot, "build", "-p", "1", "-trimpath", "-ldflags=-s -w",
        "-o", (Join-Path $ArtifactDirectory "GopherAI-eval-runner"),
        "./cmd/eval-runner"
    )
    Write-Host "[build] Grafana dashboard validator linux/amd64 CGO_ENABLED=0"
    Invoke-Checked -FilePath $GoExecutable -Arguments @(
        "-C", $RepoRoot, "build", "-p", "1", "-trimpath", "-ldflags=-s -w",
        "-o", (Join-Path $ArtifactDirectory "GopherAI-dashboard-validator"),
        "./cmd/dashboard-validator"
    )
    Write-Host "[build] bounded performance evaluation runner linux/amd64 CGO_ENABLED=0"
    Invoke-Checked -FilePath $GoExecutable -Arguments @(
        "-C", $RepoRoot, "build", "-p", "1", "-trimpath", "-ldflags=-s -w",
        "-o", (Join-Path $ArtifactDirectory "GopherAI-perf-eval"),
        "./cmd/perf-eval"
    )
    Write-Host "[build] Judge calibration runner linux/amd64 CGO_ENABLED=0"
    Invoke-Checked -FilePath $GoExecutable -Arguments @(
        "-C", $RepoRoot, "build", "-p", "1", "-trimpath", "-ldflags=-s -w",
        "-o", (Join-Path $ArtifactDirectory "GopherAI-judge-calibration"),
        "./cmd/judge-calibration"
    )
    Write-Host "[build] post-release cleanup evidence sealer linux/amd64 CGO_ENABLED=0"
    Invoke-Checked -FilePath $GoExecutable -Arguments @(
        "-C", $RepoRoot, "build", "-p", "1", "-trimpath", "-ldflags=-s -w",
        "-o", (Join-Path $ArtifactDirectory "GopherAI-cleanup-audit"),
        "./cmd/cleanup-audit"
    )
    Write-Host "[build] Full 320 review evidence verifier linux/amd64 CGO_ENABLED=0"
    Invoke-Checked -FilePath $GoExecutable -Arguments @(
        "-C", $RepoRoot, "build", "-p", "1", "-trimpath", "-ldflags=-s -w",
        "-o", (Join-Path $ArtifactDirectory "GopherAI-catalog-review-evidence"),
        "./cmd/catalog-review-evidence"
    )
    Write-Host "[build] immutable Full 320 sealed candidate verifier linux/amd64 CGO_ENABLED=0"
    Invoke-Checked -FilePath $GoExecutable -Arguments @(
        "-C", $RepoRoot, "build", "-p", "1", "-trimpath", "-ldflags=-s -w",
        "-o", (Join-Path $ArtifactDirectory "GopherAI-catalog-seal-verify"),
        "./cmd/catalog-seal-verify"
    )
}

function Assert-EvaluationCatalogReleaseIntegrity {
    param([string]$TrackedSourceRoot)

    $manifestPath = Join-Path $TrackedSourceRoot "evals\devsupport-eval-v1.manifest.json"
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        throw "Release source is missing the evaluation catalog manifest."
    }

    $catalog = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    $actualTotal = 0
    foreach ($slice in $catalog.slices) {
        $slicePath = Join-Path (Split-Path -Parent $manifestPath) ([string]$slice.path)
        if (-not (Test-Path -LiteralPath $slicePath -PathType Leaf)) {
            throw "Release source is missing evaluation slice '$($slice.name)': $($slice.path)"
        }

        $actualHash = (Get-FileHash -LiteralPath $slicePath -Algorithm SHA256).Hash.ToLowerInvariant()
        $expectedHash = ([string]$slice.sha256).ToLowerInvariant()
        if ($actualHash -ne $expectedHash) {
            throw "Release evaluation slice '$($slice.name)' SHA-256 mismatch after git archive: got $actualHash want $expectedHash."
        }

        $actualCount = (Get-Content -LiteralPath $slicePath | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }).Count
        if ($actualCount -ne [int]$slice.expected_count) {
            throw "Release evaluation slice '$($slice.name)' count mismatch: got $actualCount want $($slice.expected_count)."
        }
        $actualTotal += $actualCount
    }

    if ($actualTotal -ne [int]$catalog.total_cases) {
        throw "Release evaluation catalog total mismatch: got $actualTotal want $($catalog.total_cases)."
    }
    Write-Host "[deploy] evaluation catalog release gate passed: $actualTotal cases with exact SHA-256"
}

$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot "..\..")).Path
$timestamp = Get-Date -Format "yyyyMMddHHmmss"
$branch = (& git -C $repoRoot branch --show-current).Trim()
if ([string]::IsNullOrWhiteSpace($branch)) { $branch = "detached" }
$gitSha = (& git -C $repoRoot rev-parse HEAD).Trim()
$shortSha = $gitSha.Substring(0, [Math]::Min(12, $gitSha.Length))
$statusLines = & git -C $repoRoot status --porcelain --untracked-files=all
$relevantStatusLines = $statusLines | Where-Object { $_ -notmatch '^\?\? \.claude(?:/|\\)' }
$dirty = -not [string]::IsNullOrWhiteSpace(($relevantStatusLines | Out-String))
$releaseId = "$timestamp-$shortSha"
if ($dirty) { $releaseId += "-dirty" }
$safeBranch = $branch -replace '[^A-Za-z0-9_.-]', '_'
$bundleName = "GopherAI_${safeBranch}_${releaseId}.tar.gz"
$tempRoot = Join-Path ([System.IO.Path]::GetTempPath()) "gopherai-deploy-$timestamp"
$payloadRoot = Join-Path $tempRoot "payload"
$trackedSourceRoot = Join-Path $tempRoot "tracked-source"
$trackedSourceArchive = Join-Path $tempRoot "tracked-source.tar"
$artifactDirectory = Join-Path $payloadRoot ".deploy-bin"
$manifestPath = Join-Path $payloadRoot "release-manifest.json"
$sourceInventoryPath = Join-Path $payloadRoot ".release-source-files.txt"
$bundlePath = Join-Path $tempRoot $bundleName
$checksumPath = "$bundlePath.sha256"
$remoteBundlePath = "$RemoteHostBundleDir/$bundleName"

$environmentNames = @("GOENV", "GOROOT", "GOPATH", "GOMODCACHE", "GOCACHE", "GOTOOLCHAIN", "GO111MODULE", "GOOS", "GOARCH", "CGO_ENABLED")
$originalEnvironment = @{}
foreach ($name in $environmentNames) {
    $originalEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
}

try {
    Require-Command "git"
    Require-Command "tar"
    New-Item -ItemType Directory -Force -Path $tempRoot, $payloadRoot, $trackedSourceRoot | Out-Null
    Write-Host "[deploy] repo: $repoRoot"
    Write-Host "[deploy] branch: $branch"
    Write-Host "[deploy] git sha: $gitSha"
    Write-Host "[deploy] release: $releaseId"
    Write-Host "[deploy] host: $HostAlias"
    Write-Host "[deploy] source dirty: $dirty"
	if ($dirty) {
		throw "Deployment requires a clean Git tree so the release can be reproduced exactly from its manifest Git SHA. Commit or remove local changes first."
	}

    Assert-RemoteDeploymentCapacity

    $goExecutable = $null
    $goVersion = "container-build"
    if (-not $BuildInContainer -or $RunLocalTests) {
        Require-Command "go"
        $goExecutable = (Get-Command go).Source
        Set-GoBuildEnvironment -GoExecutable $goExecutable -TempRoot $tempRoot
        $goVersion = (& $goExecutable version | Out-String).Trim()
    }

    if ($RunLocalTests) {
        Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
        Invoke-Checked -FilePath $goExecutable -Arguments @("-C", $repoRoot, "test", "-p", "1", "./...")
        Invoke-Checked -FilePath $goExecutable -Arguments @("-C", (Join-Path $repoRoot "common\mcp"), "test", "-p", "1", "./...")
    }

    if (-not $SkipFrontend) {
        Require-Command "npm"
        Write-Host "[build] Vue production assets (local, never on the 1.6 GiB ECS)"
        Invoke-Checked -FilePath "npm" -Arguments @("run", "build") -WorkingDirectory (Join-Path $repoRoot "vue-frontend")
        if (-not (Test-Path -LiteralPath (Join-Path $repoRoot "vue-frontend\dist\index.html"))) {
            throw "Vue production build did not create dist/index.html."
        }
    }

    if ($BuildInContainer) {
        Write-Warning "Container build can exhaust the 1.6 GiB ECS; use only as an explicit fallback."
        $buildStrategy = "container"
    }
    elseif ($DryRun) {
        $buildStrategy = "local-linux-amd64-dry-run"
    }
    else {
        $buildStrategy = "local-linux-amd64-nocgo"
        Build-LocalLinuxArtifacts -GoExecutable $goExecutable -RepoRoot $repoRoot -ArtifactDirectory $artifactDirectory
    }

	# Build the release source from the committed tree, never from the ambient
	# working directory. This excludes ignored screenshots, local logs and other
	# machine-only files even when they happen to exist beside the repository.
	# Disable the developer machine's core.autocrlf setting for archive export.
	# Evaluation fixture hashes are calculated over repository bytes; allowing a
	# Windows checkout policy to rewrite LF to CRLF makes the same Git SHA deploy
	# different evidence bytes on Linux.
	$archiveArgs = @("-c", "core.autocrlf=false", "-C", $repoRoot, "archive", "--format=tar", "--output=$trackedSourceArchive", $gitSha, "--", ".")
	if (-not $DeployConfig) {
		$archiveArgs += ":(exclude)config/config.toml"
	}
	$archiveArgs += ":(exclude)uploads"
	$archiveArgs += ":(exclude).idea"
	Invoke-Checked -FilePath "git" -Arguments $archiveArgs
	Invoke-Checked -FilePath "tar" -Arguments @("-xf", $trackedSourceArchive, "-C", $trackedSourceRoot)
	Assert-EvaluationCatalogReleaseIntegrity -TrackedSourceRoot $trackedSourceRoot

    $manifest = [ordered]@{
        release_id = $releaseId
        branch = $branch
        git_sha = $gitSha
        source_dirty = $dirty
        built_at = (Get-Date).ToUniversalTime().ToString("o")
        build_strategy = $buildStrategy
        target = "linux/amd64"
        go_version = $goVersion
        go_build_flags = @("-p=1", "-trimpath", "-ldflags=-s -w")
        included_components = @("backend", "index-worker", "mcp", "frontend-static-gateway", "frontend-dist", "collaboration-eval", "parent-context-eval", "unified-eval-runner", "grafana-dashboard-validator", "bounded-performance-eval", "judge-calibration", "cleanup-audit-sealer", "catalog-review-evidence-verifier", "catalog-sealed-candidate-verifier")
        config_included = [bool]$DeployConfig
        migrations = @()
        rollback = "previous-directory"
    }
    $manifestJson = $manifest | ConvertTo-Json -Depth 5
    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($manifestPath, $manifestJson + [Environment]::NewLine, $utf8NoBom)

    $trackedSourceFiles = @(& git -C $repoRoot ls-files)
    if ($LASTEXITCODE -ne 0 -or $trackedSourceFiles.Count -eq 0) {
        throw "Unable to create the tracked source inventory."
    }
    $trackedSourceFiles = [string[]]@($trackedSourceFiles | ForEach-Object { $_ -replace '\\', '/' })
	$trackedSourceFiles = [string[]]@($trackedSourceFiles | Where-Object {
		$_ -notmatch '^(uploads|\.idea)/' -and ($DeployConfig -or $_ -ne 'config/config.toml')
	})
	if ($trackedSourceFiles.Count -eq 0) {
		throw "Tracked source inventory became empty after release exclusions."
	}
    [Array]::Sort($trackedSourceFiles, [System.StringComparer]::Ordinal)
    [System.IO.File]::WriteAllLines($sourceInventoryPath, $trackedSourceFiles, $utf8NoBom)

    $tarArgs = @(
        "-czf", $bundlePath,
		"--exclude=config/config.toml"
    )
	if ($DeployConfig) { $tarArgs = @("-czf", $bundlePath) }
	$tarArgs += @("-C", $trackedSourceRoot, ".")
	if (-not $SkipFrontend) { $tarArgs += @("-C", $repoRoot, "vue-frontend/dist") }
	$tarArgs += @("-C", $payloadRoot, "release-manifest.json", ".release-source-files.txt")
    if (-not $BuildInContainer -and -not $DryRun) { $tarArgs += ".deploy-bin" }
    Invoke-Checked -FilePath "tar" -Arguments $tarArgs -WorkingDirectory $repoRoot

	$bundleEntries = @(& tar -tzf $bundlePath)
	if ($LASTEXITCODE -ne 0 -or $bundleEntries.Count -eq 0) {
		throw "Unable to inspect the generated release bundle."
	}
	$forbiddenBundleEntries = @($bundleEntries | Where-Object {
		$_ -match '(^|/)(f[123]|opts(?:_big)?|q|top(?:_big)?)\.png$' -or
		$_ -match '(^|/)(node_modules|uploads|\.git|\.idea|\.claude|\.codex-tmp)(/|$)' -or
		(-not $DeployConfig -and $_ -match '(^|/)config/config\.toml$')
	})
	if ($forbiddenBundleEntries.Count -ne 0) {
		throw "Release bundle contains forbidden working-tree artifacts: $($forbiddenBundleEntries -join ', ')"
	}
	Write-Host "[deploy] provenance: git archive $gitSha + controlled build artifacts ($($bundleEntries.Count) entries)"

    $bundleHash = (Get-FileHash -LiteralPath $bundlePath -Algorithm SHA256).Hash.ToLowerInvariant()
    [System.IO.File]::WriteAllText($checksumPath, "$bundleHash  $bundleName`n", $utf8NoBom)
    Write-Host "[deploy] bundle: $bundlePath ($((Get-Item -LiteralPath $bundlePath).Length) bytes)"
    Write-Host "[deploy] sha256: $bundleHash"

    Invoke-Remote -Command "mkdir -p $(Quote-Bash $RemoteHostBundleDir)"
    Upload-File -LocalPath $bundlePath -RemotePath $remoteBundlePath
    Upload-File -LocalPath $checksumPath -RemotePath "$remoteBundlePath.sha256"
    Upload-File -LocalPath $manifestPath -RemotePath "$remoteBundlePath.manifest.json"

    $dependencyList = ($DependencyContainers | ForEach-Object { Quote-Bash $_ }) -join " "
    $skipFrontendText = if ($SkipFrontend) { "true" } else { "false" }
    $deployConfigText = if ($DeployConfig) { "true" } else { "false" }
    $buildInContainerText = if ($BuildInContainer) { "true" } else { "false" }

    $remoteScriptTemplate = @'
set -Eeuo pipefail
container=__CONTAINER__
bundle_host_path=__BUNDLE_HOST_PATH__
bundle_name=__BUNDLE_NAME__
expected_sha=__EXPECTED_SHA__
container_bundle_dir=__CONTAINER_BUNDLE_DIR__
dependency_containers=(__DEPENDENCY_CONTAINERS__)

echo "[remote] verifying release bundle"
actual_sha="$(sha256sum "$bundle_host_path" | awk '{print $1}')"
[ "$actual_sha" = "$expected_sha" ] || { echo "bundle checksum mismatch" >&2; exit 1; }
echo "[remote] starting containers: ${dependency_containers[*]}"
docker start "${dependency_containers[@]}" >/dev/null
for dependency in "${dependency_containers[@]}"; do
  for attempt in $(seq 1 30); do
    [ "$(docker inspect -f '{{.State.Running}}' "$dependency")" = "true" ] && break
    sleep 1
  done
  [ "$(docker inspect -f '{{.State.Running}}' "$dependency")" = "true" ] || { echo "container did not start: $dependency" >&2; exit 1; }
done

if [ -n "$(docker port "$container" 9093/tcp 2>/dev/null || true)" ]; then
  echo "Grafana port 9093 must not be published by Docker" >&2
  exit 1
fi

echo "[remote] copying verified bundle into container"
docker exec "$container" bash -lc "mkdir -p '$container_bundle_dir'"
docker cp "$bundle_host_path" "$container:$container_bundle_dir/$bundle_name"

docker exec -i "$container" bash -s <<'EOS'
set -Eeuo pipefail
export PATH="/usr/local/go/bin:$PATH"
bundle_path="__CONTAINER_BUNDLE_DIR_RAW__/__BUNDLE_NAME_RAW__"
project_path="__PROJECT_PATH_RAW__"
runtime_path="__RUNTIME_PATH_RAW__"
release_id="__RELEASE_ID_RAW__"
git_sha="__GIT_SHA_RAW__"
expected_sha="__EXPECTED_SHA_RAW__"
skip_frontend="__SKIP_FRONTEND_RAW__"
deploy_config="__DEPLOY_CONFIG_RAW__"
build_in_container="__BUILD_IN_CONTAINER_RAW__"
new_path="${project_path}.__new_${release_id}"
backup_path="${project_path}.__previous_${release_id}"
failed_path="${project_path}.__failed_${release_id}"
run_path="$runtime_path/run"

case "$new_path" in /root/GopherAI-.__new_*) ;; *) echo "unsafe staged path: $new_path" >&2; exit 1 ;; esac
mkdir -p "$run_path"
rm -rf -- "$new_path"
mkdir -p "$new_path"
echo "[container] extracting release $release_id"
container_bundle_sha="$(sha256sum "$bundle_path" | awk '{print $1}')"
[ "$container_bundle_sha" = "$expected_sha" ] || { echo "container bundle checksum mismatch" >&2; exit 1; }
tar -xzf "$bundle_path" -C "$new_path"
test -f "$new_path/release-manifest.json"

if [ "$deploy_config" != "true" ] && [ -f "$project_path/config/config.toml" ]; then
  mkdir -p "$new_path/config"
  cp -a "$project_path/config/config.toml" "$new_path/config/config.toml"
fi

if [ "$build_in_container" = "true" ]; then
  echo "[container] building backend, index worker, MCP and frontend gateway with -p 1"
  (cd "$new_path" && go build -p 1 -trimpath -ldflags='-s -w' -o GopherAI main.go pprof_server.go)
  (cd "$new_path" && go build -p 1 -trimpath -ldflags='-s -w' -o GopherAI-index-worker ./cmd/index-worker)
  (cd "$new_path/common/mcp" && go build -p 1 -trimpath -ldflags='-s -w' -o gopherai-mcp .)
  (cd "$new_path" && go build -p 1 -trimpath -ldflags='-s -w' -o GopherAI-frontend ./cmd/frontend-gateway)
  (cd "$new_path" && go build -p 1 -trimpath -ldflags='-s -w' -o GopherAI-collaboration-eval ./cmd/collaboration-eval)
  (cd "$new_path" && go build -p 1 -trimpath -ldflags='-s -w' -o GopherAI-parent-context-eval ./cmd/parent-context-eval)
  (cd "$new_path" && go build -p 1 -trimpath -ldflags='-s -w' -o GopherAI-eval-runner ./cmd/eval-runner)
  (cd "$new_path" && go build -p 1 -trimpath -ldflags='-s -w' -o GopherAI-dashboard-validator ./cmd/dashboard-validator)
  (cd "$new_path" && go build -p 1 -trimpath -ldflags='-s -w' -o GopherAI-perf-eval ./cmd/perf-eval)
  (cd "$new_path" && go build -p 1 -trimpath -ldflags='-s -w' -o GopherAI-judge-calibration ./cmd/judge-calibration)
  (cd "$new_path" && go build -p 1 -trimpath -ldflags='-s -w' -o GopherAI-cleanup-audit ./cmd/cleanup-audit)
  (cd "$new_path" && go build -p 1 -trimpath -ldflags='-s -w' -o GopherAI-catalog-review-evidence ./cmd/catalog-review-evidence)
  (cd "$new_path" && go build -p 1 -trimpath -ldflags='-s -w' -o GopherAI-catalog-seal-verify ./cmd/catalog-seal-verify)
else
  echo "[container] installing locally built Linux binaries"
  test -f "$new_path/.deploy-bin/GopherAI"
  test -f "$new_path/.deploy-bin/GopherAI-index-worker"
  test -f "$new_path/.deploy-bin/gopherai-mcp"
  test -f "$new_path/.deploy-bin/GopherAI-frontend"
  test -f "$new_path/.deploy-bin/GopherAI-collaboration-eval"
  test -f "$new_path/.deploy-bin/GopherAI-parent-context-eval"
  test -f "$new_path/.deploy-bin/GopherAI-eval-runner"
  test -f "$new_path/.deploy-bin/GopherAI-dashboard-validator"
  test -f "$new_path/.deploy-bin/GopherAI-perf-eval"
  test -f "$new_path/.deploy-bin/GopherAI-judge-calibration"
  test -f "$new_path/.deploy-bin/GopherAI-cleanup-audit"
  test -f "$new_path/.deploy-bin/GopherAI-catalog-review-evidence"
  test -f "$new_path/.deploy-bin/GopherAI-catalog-seal-verify"
  cp "$new_path/.deploy-bin/GopherAI" "$new_path/GopherAI"
  cp "$new_path/.deploy-bin/GopherAI-index-worker" "$new_path/GopherAI-index-worker"
  cp "$new_path/.deploy-bin/gopherai-mcp" "$new_path/common/mcp/gopherai-mcp"
  cp "$new_path/.deploy-bin/GopherAI-frontend" "$new_path/GopherAI-frontend"
  cp "$new_path/.deploy-bin/GopherAI-collaboration-eval" "$new_path/GopherAI-collaboration-eval"
  cp "$new_path/.deploy-bin/GopherAI-parent-context-eval" "$new_path/GopherAI-parent-context-eval"
  cp "$new_path/.deploy-bin/GopherAI-eval-runner" "$new_path/GopherAI-eval-runner"
  cp "$new_path/.deploy-bin/GopherAI-dashboard-validator" "$new_path/GopherAI-dashboard-validator"
  cp "$new_path/.deploy-bin/GopherAI-perf-eval" "$new_path/GopherAI-perf-eval"
  cp "$new_path/.deploy-bin/GopherAI-judge-calibration" "$new_path/GopherAI-judge-calibration"
  cp "$new_path/.deploy-bin/GopherAI-cleanup-audit" "$new_path/GopherAI-cleanup-audit"
  cp "$new_path/.deploy-bin/GopherAI-catalog-review-evidence" "$new_path/GopherAI-catalog-review-evidence"
  cp "$new_path/.deploy-bin/GopherAI-catalog-seal-verify" "$new_path/GopherAI-catalog-seal-verify"
  chmod 0755 "$new_path/GopherAI" "$new_path/GopherAI-index-worker" "$new_path/common/mcp/gopherai-mcp" "$new_path/GopherAI-frontend" "$new_path/GopherAI-collaboration-eval" "$new_path/GopherAI-parent-context-eval" "$new_path/GopherAI-eval-runner" "$new_path/GopherAI-dashboard-validator" "$new_path/GopherAI-perf-eval" "$new_path/GopherAI-judge-calibration" "$new_path/GopherAI-cleanup-audit" "$new_path/GopherAI-catalog-review-evidence" "$new_path/GopherAI-catalog-seal-verify"
  rm -rf -- "$new_path/.deploy-bin"
fi

if [ -f "$new_path/deploy/observability/prometheus.yml" ]; then
  command -v prometheus >/dev/null 2>&1 || { echo "prometheus runtime is missing; run scripts/deploy/bootstrap-prometheus-aliyun.ps1" >&2; exit 1; }
  command -v promtool >/dev/null 2>&1 || { echo "promtool is missing; run scripts/deploy/bootstrap-prometheus-aliyun.ps1" >&2; exit 1; }
  echo "[container] validating Prometheus scrape config and recording rules"
  (cd "$new_path" && promtool check config deploy/observability/prometheus.yml)
  promtool check rules "$new_path/deploy/observability/recording-rules.yml"
  (cd "$new_path/deploy/observability" && promtool test rules recording-rules.test.yml)
fi

if [ -f "$new_path/deploy/observability/grafana/dashboards/gopherai-closed-loop.json" ]; then
  command -v grafana >/dev/null 2>&1 || { echo "Grafana runtime is missing; run scripts/deploy/bootstrap-grafana-aliyun.ps1" >&2; exit 1; }
  echo "[container] validating immutable Grafana dashboard and provisioning"
  (cd "$new_path" && ./GopherAI-dashboard-validator -root . > grafana-dashboard-validation.json)
fi

stop_pid_file() {
  component="$1"; pid_file="$run_path/$component.pid"
  [ -f "$pid_file" ] || return 0
  pid="$(tr -cd '0-9' < "$pid_file")"
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
    for attempt in $(seq 1 15); do kill -0 "$pid" 2>/dev/null || break; sleep 1; done
    kill -0 "$pid" 2>/dev/null && kill -9 "$pid" 2>/dev/null || true
  fi
  rm -f -- "$pid_file"
}

stop_legacy_matches() {
  pattern="$1"
  pids="$(pgrep -f "$pattern" 2>/dev/null || true)"
  for pid in $pids; do [ "$pid" = "$$" ] || kill "$pid" 2>/dev/null || true; done
}

stop_grafana_plugin_processes() {
  pattern='^/var/lib/grafana/plugins-bundled/.*/gpx_grafana_'
  pids="$(pgrep -f "$pattern" 2>/dev/null || true)"
  [ -z "$pids" ] && return 0
  for pid in $pids; do kill "$pid" 2>/dev/null || true; done
  for attempt in $(seq 1 5); do
    remaining="$(pgrep -f "$pattern" 2>/dev/null || true)"
    [ -z "$remaining" ] && return 0
    sleep 1
  done
  for pid in $remaining; do kill -9 "$pid" 2>/dev/null || true; done
}

stop_application() {
  stop_pid_file frontend; stop_pid_file mcp; stop_pid_file grafana; stop_pid_file prometheus; stop_pid_file index-worker; stop_pid_file backend
  stop_legacy_matches '^\./GopherAI$'
  stop_legacy_matches '^\./GopherAI-index-worker$'
  stop_legacy_matches '^\./gopherai-mcp -mode server$'
  stop_legacy_matches '^\./GopherAI-frontend '
  stop_legacy_matches '^prometheus .*deploy/observability/prometheus.yml'
  stop_legacy_matches '^/usr/share/grafana/bin/grafana server '
  stop_grafana_plugin_processes
  stop_legacy_matches 'node .*vue-cli-service.*serve'
  sleep 2
}

wait_tcp() {
  host="$1"; port="$2"; timeout_seconds="$3"
  for attempt in $(seq 1 "$timeout_seconds"); do
    if (exec 3<>"/dev/tcp/$host/$port") 2>/dev/null; then exec 3>&- 3<&-; return 0; fi
    sleep 1
  done
  echo "tcp readiness timeout: $host:$port" >&2
  return 1
}

wait_http_health() {
  endpoint="$1"; timeout_seconds="$2"; response_file="$run_path/health-response-$$.json"
  last_code="000"; deadline=$((SECONDS + timeout_seconds))
  while [ "$SECONDS" -lt "$deadline" ]; do
    last_code="$(curl --silent --show-error --output "$response_file" --write-out '%{http_code}' --max-time 3 "$endpoint" 2>/dev/null || true)"
    if [ "$last_code" = "200" ]; then
      echo "health check passed: $endpoint"
      cat "$response_file"; echo
      rm -f -- "$response_file"
      return 0
    fi
    if [ "$last_code" = "404" ]; then
      rm -f -- "$response_file"
      return 2
    fi
    sleep 1
  done
  echo "health check timeout: $endpoint (last HTTP $last_code)" >&2
  cat "$response_file" >&2 2>/dev/null || true
  rm -f -- "$response_file"
  return 1
}

wait_backend_health() {
  port="$1"
  if ! command -v curl >/dev/null 2>&1; then
    echo "curl is unavailable; using TCP readiness for backend"
    wait_tcp 127.0.0.1 "$port" 90
    return
  fi

  if wait_http_health "http://127.0.0.1:$port/health/live" 90; then
    wait_http_health "http://127.0.0.1:$port/health/ready" 90
    return
  else
    health_result="$?"
  fi
  if [ "$health_result" = "2" ]; then
    echo "health endpoints are absent in rollback release; using TCP readiness"
    wait_tcp 127.0.0.1 "$port" 90
    return
  fi
  return "$health_result"
}

wait_frontend_ready() {
  port="$1"; timeout_seconds="$2"; log_file="$project_path/vue-frontend/frontend.log"
  deadline=$((SECONDS + timeout_seconds)); last_code="000"
  while [ "$SECONDS" -lt "$deadline" ]; do
    if command -v curl >/dev/null 2>&1; then
      last_code="$(curl --silent --output /dev/null --write-out '%{http_code}' --max-time 3 "http://127.0.0.1:$port/" 2>/dev/null || true)"
      [ "$last_code" = "200" ] && { echo "frontend static gateway HTTP check passed"; return 0; }
    elif (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
      exec 3>&- 3<&-; echo "frontend static gateway TCP check passed"; return 0
    fi
    if [ -f "$run_path/frontend.pid" ]; then
      frontend_pid="$(tr -cd '0-9' < "$run_path/frontend.pid")"
      if [ -n "$frontend_pid" ] && ! kill -0 "$frontend_pid" 2>/dev/null; then
        echo "frontend process exited before readiness" >&2; tail -n 80 "$log_file" >&2; return 1
      fi
    fi
    sleep 1
  done
  echo "frontend readiness timeout on port $port (last HTTP $last_code)" >&2
  tail -n 80 "$log_file" >&2 2>/dev/null || true
  return 1
}

backend_port() {
  awk '/^\[mainConfig\]/ { active=1; next } /^\[/ { if (active) exit } active && /^[[:space:]]*port[[:space:]]*=/ { split($0,v,"="); gsub(/[[:space:]]/,"",v[2]); print v[2]; exit }' "$project_path/config/config.toml"
}

start_release() {
  release_path="$1"
  skip_grafana="${2:-false}"
  service mysql start >/dev/null 2>&1 || true
  for attempt in $(seq 1 30); do mysqladmin ping --silent >/dev/null 2>&1 && break; sleep 1; done
  mysqladmin ping --silent >/dev/null 2>&1 || return 1
  wait_tcp rabbitmq 5672 60 || return 1
  wait_tcp redis-vector 6379 60 || return 1

  webhook_secret_path="$runtime_path/control-webhook-secret"
  if [ ! -s "$webhook_secret_path" ]; then
    old_umask="$(umask)"; umask 077
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' > "$webhook_secret_path"
    umask "$old_umask"
  fi
  chmod 0600 "$webhook_secret_path"
  export GOPHERAI_CONTROL_WEBHOOK_URL="${GOPHERAI_CONTROL_WEBHOOK_URL:-http://127.0.0.1:9090/internal/v1/webhooks/control}"
  export GOPHERAI_CONTROL_WEBHOOK_SECRET_FILE="${GOPHERAI_CONTROL_WEBHOOK_SECRET_FILE:-$webhook_secret_path}"
  export GOPHERAI_CONTROL_WEBHOOK_LOOPBACK_RECEIVER="${GOPHERAI_CONTROL_WEBHOOK_LOOPBACK_RECEIVER:-true}"

  cd "$release_path"; : > backend.log; nohup ./GopherAI > backend.log 2>&1 & echo "$!" > "$run_path/backend.pid"
  port="$(backend_port)"; [ -n "$port" ] || port=9090
  wait_backend_health "$port" || return 1

  if [ -x "$release_path/GopherAI-index-worker" ]; then
    cd "$release_path"; : > index-worker.log; nohup ./GopherAI-index-worker > index-worker.log 2>&1 & echo "$!" > "$run_path/index-worker.pid"
    wait_http_health "http://127.0.0.1:9091/health/live" 90 || return 1
    wait_http_health "http://127.0.0.1:9091/health/ready" 90 || return 1
  else
    echo "index worker is absent in historical release; skipping worker start"
  fi

  if [ -f "$release_path/deploy/observability/prometheus.yml" ]; then
    command -v prometheus >/dev/null 2>&1 || { echo "prometheus runtime is missing" >&2; return 1; }
    mkdir -p "$runtime_path/prometheus-data"
    cd "$release_path"; : > prometheus.log
    nohup prometheus --config.file=deploy/observability/prometheus.yml --storage.tsdb.path="$runtime_path/prometheus-data" --storage.tsdb.retention.time=72h --storage.tsdb.retention.size=128MB --web.listen-address=127.0.0.1:9092 > prometheus.log 2>&1 & echo "$!" > "$run_path/prometheus.pid"
    wait_http_health "http://127.0.0.1:9092/-/ready" 90 || return 1
    scrape_deadline=$((SECONDS + 45)); scrape_up=0
    while [ "$SECONDS" -lt "$scrape_deadline" ]; do
      scrape_up="$(curl -fsS 'http://127.0.0.1:9092/api/v1/targets?state=active' 2>/dev/null | grep -o '"health":"up"' | wc -l | tr -d ' ' || true)"
      [ "$scrape_up" = "2" ] && break
      sleep 1
    done
    [ "$scrape_up" = "2" ] || { echo "Prometheus did not report both application targets up" >&2; tail -n 80 "$release_path/prometheus.log" >&2; return 1; }
    echo "Prometheus scrape targets healthy: $scrape_up/2"
  else
    echo "Prometheus config is absent in historical release; skipping metrics server"
  fi

  if [ "$skip_grafana" = "true" ]; then
    echo "Grafana skipped while restoring the core application after a failed deployment"
  elif [ -f "$release_path/deploy/observability/grafana/grafana.ini" ]; then
    command -v grafana >/dev/null 2>&1 || { echo "Grafana runtime is missing" >&2; return 1; }
    grafana_runtime_root=/var/lib/gopherai-grafana
    [ "$(realpath -m -- "$grafana_runtime_root")" = "/var/lib/gopherai-grafana" ] || { echo "unsafe Grafana runtime root" >&2; return 1; }
    install -d -o grafana -g grafana -m 0750 "$grafana_runtime_root/data" "$grafana_runtime_root/logs" "$grafana_runtime_root/plugins"
    install -d -o root -g root -m 0755 "$grafana_runtime_root/provisioning/datasources" "$grafana_runtime_root/provisioning/dashboards" "$grafana_runtime_root/provisioning/plugins" "$grafana_runtime_root/provisioning/alerting" "$grafana_runtime_root/dashboards"
    find "$grafana_runtime_root/provisioning" "$grafana_runtime_root/dashboards" -type f -delete
    install -o root -g root -m 0444 "$release_path/deploy/observability/grafana/grafana.ini" "$grafana_runtime_root/grafana.ini"
    install -o root -g root -m 0444 "$release_path/deploy/observability/grafana/provisioning/datasources/gopherai-prometheus.yml" "$grafana_runtime_root/provisioning/datasources/gopherai-prometheus.yml"
    install -o root -g root -m 0444 "$release_path/deploy/observability/grafana/provisioning/dashboards/gopherai.yml" "$grafana_runtime_root/provisioning/dashboards/gopherai.yml"
    install -o root -g root -m 0444 "$release_path/deploy/observability/grafana/dashboards/gopherai-closed-loop.json" "$grafana_runtime_root/dashboards/gopherai-closed-loop.json"
    grafana_admin_secret_path="$runtime_path/grafana-admin-secret"
    grafana_signing_secret_path="$runtime_path/grafana-signing-secret"
    for secret_path in "$grafana_admin_secret_path" "$grafana_signing_secret_path"; do
      if [ ! -s "$secret_path" ]; then
        old_umask="$(umask)"; umask 077
        head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' > "$secret_path"
        umask "$old_umask"
      fi
      chmod 0600 "$secret_path"
    done
    cd "$release_path"; : > grafana.log
    if grep -q '/root/GopherAI-' "$release_path/deploy/observability/grafana/provisioning/dashboards/gopherai.yml"; then
      echo "Grafana rollback compatibility mode: running legacy root-scoped provisioning"
      GF_SECURITY_ADMIN_PASSWORD="$(cat "$grafana_admin_secret_path")" GF_SECURITY_SECRET_KEY="$(cat "$grafana_signing_secret_path")" GOMEMLIMIT=170MiB GOGC=40 nohup grafana server --homepath=/usr/share/grafana --config="$release_path/deploy/observability/grafana/grafana.ini" > grafana.log 2>&1 & echo "$!" > "$run_path/grafana.pid"
    else
      GF_SECURITY_ADMIN_PASSWORD="$(cat "$grafana_admin_secret_path")" GF_SECURITY_SECRET_KEY="$(cat "$grafana_signing_secret_path")" GOMEMLIMIT=170MiB GOGC=40 nohup setpriv --reuid=grafana --regid=grafana --init-groups grafana server --homepath=/usr/share/grafana --config="$grafana_runtime_root/grafana.ini" > grafana.log 2>&1 & echo "$!" > "$run_path/grafana.pid"
    fi
    wait_http_health "http://127.0.0.1:9093/api/health" 90 || return 1
    grafana_deadline=$((SECONDS + 45)); grafana_dashboard_ready=false
    while [ "$SECONDS" -lt "$grafana_deadline" ]; do
      if curl -fsS 'http://127.0.0.1:9093/api/search?query=GopherAI' 2>/dev/null | grep -q 'gopherai-closed-loop-v1'; then
        grafana_dashboard_ready=true; break
      fi
      sleep 1
    done
    [ "$grafana_dashboard_ready" = "true" ] || { echo "Grafana did not provision the GopherAI dashboard" >&2; tail -n 80 "$release_path/grafana.log" >&2; return 1; }
    grafana_pid="$(tr -cd '0-9' < "$run_path/grafana.pid")"
    grafana_startup_rss_kib="$(ps -o rss= -p "$grafana_pid" | tr -d ' ' || true)"
    [ -n "$grafana_startup_rss_kib" ] || { echo "Grafana process exited after readiness" >&2; return 1; }
    [ "$grafana_startup_rss_kib" -le 393216 ] || { echo "Grafana RSS exceeded 384 MiB startup guard: ${grafana_startup_rss_kib} KiB" >&2; return 1; }

    # Grafana briefly retains dashboard/plugin initialization memory after its
    # readiness endpoint turns green. Measure the settled process separately so
    # a harmless startup peak does not trigger rollback on the 1.6 GiB ECS.
    sleep 45
    grafana_stable_rss_kib="$(ps -o rss= -p "$grafana_pid" | tr -d ' ' || true)"
    [ -n "$grafana_stable_rss_kib" ] || { echo "Grafana process exited during RSS stabilization" >&2; return 1; }
    [ "$grafana_stable_rss_kib" -le 307200 ] || { echo "Grafana RSS exceeded 300 MiB stable-state guard: ${grafana_stable_rss_kib} KiB" >&2; return 1; }
    grafana_unexpected_plugins="$(pgrep -af '^/var/lib/grafana/plugins-bundled/.*/gpx_grafana_' 2>/dev/null | grep -v '/prometheus/' || true)"
    [ -z "$grafana_unexpected_plugins" ] || { echo "Grafana started a disabled bundled plugin" >&2; echo "$grafana_unexpected_plugins" >&2; return 1; }
    memory_available_kib="$(awk '/^MemAvailable:/ { print $2 }' /proc/meminfo)"
    [ "$memory_available_kib" -ge 262144 ] || { echo "system memory available fell below 256 MiB after Grafana start: ${memory_available_kib} KiB" >&2; return 1; }
    echo "Grafana dashboard ready on private container port: gopherai-closed-loop-v1 (startup ${grafana_startup_rss_kib} KiB; stable ${grafana_stable_rss_kib} KiB RSS; system available ${memory_available_kib} KiB)"
  else
    echo "Grafana config is absent in historical release; skipping dashboard server"
  fi

  cd "$release_path/common/mcp"; : > mcp.log; nohup ./gopherai-mcp -mode server > mcp.log 2>&1 & echo "$!" > "$run_path/mcp.pid"
  if [ "$skip_frontend" != "true" ]; then
    if [ -x "$release_path/GopherAI-frontend" ] && [ -f "$release_path/vue-frontend/dist/index.html" ]; then
      cd "$release_path"; : > vue-frontend/frontend.log
      nohup ./GopherAI-frontend -listen :8080 -backend "http://127.0.0.1:$port" -dist vue-frontend/dist > vue-frontend/frontend.log 2>&1 & echo "$!" > "$run_path/frontend.pid"
    else
      echo "frontend gateway is absent in historical release; using legacy Vue development server"
      [ -d "$release_path/vue-frontend/node_modules" ] || { echo "frontend node_modules is missing" >&2; return 1; }
      cd "$release_path/vue-frontend"; : > frontend.log; nohup npm run serve > frontend.log 2>&1 & echo "$!" > "$run_path/frontend.pid"
    fi
  fi

  wait_tcp 127.0.0.1 8081 60 || return 1
  if [ "$skip_frontend" != "true" ]; then
    wait_frontend_ready 8080 180 || return 1
  fi
}

prune_container_release_artifacts() {
  keep_previous="$backup_path"
  for candidate in "${project_path}.__previous_"* "${project_path}.__failed_"*; do
    [ -e "$candidate" ] || continue
    resolved="$(realpath -m -- "$candidate")"
    case "$resolved" in /root/GopherAI-.__previous_*|/root/GopherAI-.__failed_*) ;; *) echo "unsafe release artifact path: $resolved" >&2; return 1 ;; esac
    [ "$resolved" = "$keep_previous" ] && continue
    rm -rf -- "$resolved"
  done
  resolved_bundle_dir="$(realpath -m -- "$(dirname "$bundle_path")")"
  [ "$resolved_bundle_dir" = "/root/GopherAI_Deploy/bundles" ] || { echo "unsafe container bundle directory: $resolved_bundle_dir" >&2; return 1; }
  find "$resolved_bundle_dir" -maxdepth 1 -type f ! -name "$(basename "$bundle_path")" -delete
}

move_runtime_dir() {
  relative_path="$1"; source_path="$backup_path/$relative_path"; target_path="$project_path/$relative_path"
  if [ -e "$source_path" ]; then
    [ ! -e "$target_path" ] || { echo "runtime target exists: $target_path" >&2; return 1; }
    mkdir -p "$(dirname "$target_path")"; mv "$source_path" "$target_path"
  fi
}

restore_runtime_dir() {
  relative_path="$1"; source_path="$project_path/$relative_path"; target_path="$backup_path/$relative_path"
  if [ -e "$source_path" ] && [ ! -e "$target_path" ]; then mkdir -p "$(dirname "$target_path")"; mv "$source_path" "$target_path"; fi
}

rollback_release() {
  echo "[container] deployment failed; restoring previous release" >&2
  set +e; stop_application
  restore_runtime_dir uploads
  rm -rf -- "$failed_path"; mv "$project_path" "$failed_path"; mv "$backup_path" "$project_path"
  # A failed optional dashboard must never prevent the backend, worker, MCP and
  # frontend from being restored. The next successful deployment re-enables it.
  start_release "$project_path" true; return 1
}

echo "[container] stopping previous application processes"
stop_application
if [ -d "$project_path" ]; then [ ! -e "$backup_path" ] || { echo "backup exists: $backup_path" >&2; exit 1; }; mv "$project_path" "$backup_path"; fi
mv "$new_path" "$project_path"
if [ -d "$backup_path" ]; then move_runtime_dir uploads; fi

echo "[container] starting release"
if ! start_release "$project_path"; then rollback_release; exit 1; fi

echo "[container] sealing current-release cleanup evidence"
if ! (cd "$project_path" && ./GopherAI-cleanup-audit -release-id "$release_id" -git-sha "$git_sha" -root . -report "$runtime_path/evaluation/cleanup-audit-latest.json" -timeout 12s); then
  rollback_release
  exit 1
fi

echo "[container] release active: $release_id"
echo "[container] bundle sha256: $expected_sha"
sha256sum "$project_path/GopherAI" "$project_path/GopherAI-index-worker" "$project_path/common/mcp/gopherai-mcp" "$project_path/GopherAI-frontend" "$project_path/GopherAI-collaboration-eval" "$project_path/GopherAI-parent-context-eval" "$project_path/GopherAI-eval-runner" "$project_path/GopherAI-dashboard-validator" "$project_path/GopherAI-cleanup-audit" "$project_path/GopherAI-catalog-review-evidence" "$project_path/GopherAI-catalog-seal-verify" 2>/dev/null || true
pgrep -af '^\./GopherAI$|^\./GopherAI-index-worker$|^prometheus .*deploy/observability/prometheus.yml|[/]usr/share/grafana/bin/grafana server .*deploy/observability/grafana/grafana.ini|^\./gopherai-mcp -mode server$|^\./GopherAI-frontend |node .*vue-cli-service.*serve' || true
echo "[container] sanitized backend log tail"
tail -n 30 "$project_path/backend.log" 2>/dev/null | sed -E 's#(amqp://)[^@]+@#\1***:***@#g' || true
echo "[container] index worker log tail"
tail -n 30 "$project_path/index-worker.log" 2>/dev/null | sed -E 's#(amqp://)[^@]+@#\1***:***@#g' || true
echo "[container] Prometheus log tail"
tail -n 20 "$project_path/prometheus.log" 2>/dev/null || true
echo "[container] Grafana log tail"
tail -n 20 "$project_path/grafana.log" 2>/dev/null || true
echo "[container] MCP log tail"
tail -n 20 "$project_path/common/mcp/mcp.log" 2>/dev/null || true
if [ "$skip_frontend" != "true" ]; then echo "[container] frontend log tail"; tail -n 20 "$project_path/vue-frontend/frontend.log" 2>/dev/null || true; fi
echo "[container] pruning old rollback directories and uploaded bundles"
prune_container_release_artifacts
EOS

host_bundle_dir="$(realpath -m -- "$(dirname "$bundle_host_path")")"
[ "$host_bundle_dir" = "/root/GopherAI_Deploy/bundles" ] || { echo "unsafe host bundle directory: $host_bundle_dir" >&2; exit 1; }
find "$host_bundle_dir" -maxdepth 1 -type f ! -name "$bundle_name" ! -name "$bundle_name.sha256" ! -name "$bundle_name.manifest.json" -delete
'@

    $remoteScript = $remoteScriptTemplate.
        Replace("__CONTAINER__", (Quote-Bash $ContainerName)).
        Replace("__BUNDLE_HOST_PATH__", (Quote-Bash $remoteBundlePath)).
        Replace("__BUNDLE_NAME__", (Quote-Bash $bundleName)).
        Replace("__EXPECTED_SHA__", (Quote-Bash $bundleHash)).
        Replace("__CONTAINER_BUNDLE_DIR__", (Quote-Bash $RemoteContainerBundleDir)).
        Replace("__DEPENDENCY_CONTAINERS__", $dependencyList).
        Replace("__CONTAINER_BUNDLE_DIR_RAW__", $RemoteContainerBundleDir).
        Replace("__BUNDLE_NAME_RAW__", $bundleName).
        Replace("__PROJECT_PATH_RAW__", $ContainerProjectPath).
        Replace("__RUNTIME_PATH_RAW__", $ContainerRuntimePath).
        Replace("__RELEASE_ID_RAW__", $releaseId).
        Replace("__GIT_SHA_RAW__", $gitSha).
        Replace("__EXPECTED_SHA_RAW__", $bundleHash).
        Replace("__SKIP_FRONTEND_RAW__", $skipFrontendText).
        Replace("__DEPLOY_CONFIG_RAW__", $deployConfigText).
        Replace("__BUILD_IN_CONTAINER_RAW__", $buildInContainerText)
    Invoke-RemoteScript -Script $remoteScript
}
finally {
    foreach ($name in $environmentNames) {
        $originalValue = $originalEnvironment[$name]
        if ($null -eq $originalValue) { Remove-Item "Env:$name" -ErrorAction SilentlyContinue }
        else { [Environment]::SetEnvironmentVariable($name, $originalValue, "Process") }
    }
    if (Test-Path -LiteralPath $tempRoot) { Remove-Item -LiteralPath $tempRoot -Recurse -Force }
}
