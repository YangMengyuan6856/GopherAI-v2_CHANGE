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

function New-RemoteBashCommand {
    param([string]$Script)
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($Script)
    $encoded = [Convert]::ToBase64String($bytes)
    return "printf %s '$encoded' | base64 -d | bash"
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
        "-C", $RepoRoot, "build", "-p", "1",
        "-o", (Join-Path $ArtifactDirectory "GopherAI"),
        "main.go", "pprof_server.go"
    )
    Write-Host "[build] mcp linux/amd64 CGO_ENABLED=0"
    Invoke-Checked -FilePath $GoExecutable -Arguments @(
        "-C", (Join-Path $RepoRoot "common\mcp"), "build", "-p", "1",
        "-o", (Join-Path $ArtifactDirectory "gopherai-mcp"), "."
    )
    Write-Host "[build] index worker linux/amd64 CGO_ENABLED=0"
    Invoke-Checked -FilePath $GoExecutable -Arguments @(
        "-C", $RepoRoot, "build", "-p", "1",
        "-o", (Join-Path $ArtifactDirectory "GopherAI-index-worker"),
        "./cmd/index-worker"
    )
    Write-Host "[build] static frontend gateway linux/amd64 CGO_ENABLED=0"
    Invoke-Checked -FilePath $GoExecutable -Arguments @(
        "-C", $RepoRoot, "build", "-p", "1",
        "-o", (Join-Path $ArtifactDirectory "GopherAI-frontend"),
        "./cmd/frontend-gateway"
    )
    Write-Host "[build] collaboration evaluation runner linux/amd64 CGO_ENABLED=0"
    Invoke-Checked -FilePath $GoExecutable -Arguments @(
        "-C", $RepoRoot, "build", "-p", "1",
        "-o", (Join-Path $ArtifactDirectory "GopherAI-collaboration-eval"),
        "./cmd/collaboration-eval"
    )
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
$artifactDirectory = Join-Path $payloadRoot ".deploy-bin"
$manifestPath = Join-Path $payloadRoot "release-manifest.json"
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
    New-Item -ItemType Directory -Force -Path $tempRoot, $payloadRoot | Out-Null
    Write-Host "[deploy] repo: $repoRoot"
    Write-Host "[deploy] branch: $branch"
    Write-Host "[deploy] git sha: $gitSha"
    Write-Host "[deploy] release: $releaseId"
    Write-Host "[deploy] host: $HostAlias"
    Write-Host "[deploy] source dirty: $dirty"

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

    $manifest = [ordered]@{
        release_id = $releaseId
        branch = $branch
        git_sha = $gitSha
        source_dirty = $dirty
        built_at = (Get-Date).ToUniversalTime().ToString("o")
        build_strategy = $buildStrategy
        target = "linux/amd64"
        go_version = $goVersion
        included_components = @("backend", "index-worker", "mcp", "frontend-static-gateway", "frontend-dist", "collaboration-eval")
        config_included = [bool]$DeployConfig
        migrations = @()
        rollback = "previous-directory"
    }
    $manifestJson = $manifest | ConvertTo-Json -Depth 5
    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($manifestPath, $manifestJson + [Environment]::NewLine, $utf8NoBom)

    $tarArgs = @(
        "-czf", $bundlePath,
		"--exclude=.git", "--exclude=.claude", "--exclude=.codex-tmp", "--exclude=uploads",
        "--exclude=vue-frontend/node_modules",
        "--exclude=backend.log", "--exclude=index-worker.log", "--exclude=mcp.log", "--exclude=frontend.log"
    )
    if (-not $DeployConfig) { $tarArgs += "--exclude=config/config.toml" }
    $tarArgs += @("-C", $repoRoot, ".", "-C", $payloadRoot, "release-manifest.json")
    if (-not $BuildInContainer -and -not $DryRun) { $tarArgs += ".deploy-bin" }
    Invoke-Checked -FilePath "tar" -Arguments $tarArgs -WorkingDirectory $repoRoot

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
  (cd "$new_path" && go build -p 1 -o GopherAI main.go pprof_server.go)
  (cd "$new_path" && go build -p 1 -o GopherAI-index-worker ./cmd/index-worker)
  (cd "$new_path/common/mcp" && go build -p 1 -o gopherai-mcp .)
  (cd "$new_path" && go build -p 1 -o GopherAI-frontend ./cmd/frontend-gateway)
  (cd "$new_path" && go build -p 1 -o GopherAI-collaboration-eval ./cmd/collaboration-eval)
else
  echo "[container] installing locally built Linux binaries"
  test -f "$new_path/.deploy-bin/GopherAI"
  test -f "$new_path/.deploy-bin/GopherAI-index-worker"
  test -f "$new_path/.deploy-bin/gopherai-mcp"
  test -f "$new_path/.deploy-bin/GopherAI-frontend"
  test -f "$new_path/.deploy-bin/GopherAI-collaboration-eval"
  cp "$new_path/.deploy-bin/GopherAI" "$new_path/GopherAI"
  cp "$new_path/.deploy-bin/GopherAI-index-worker" "$new_path/GopherAI-index-worker"
  cp "$new_path/.deploy-bin/gopherai-mcp" "$new_path/common/mcp/gopherai-mcp"
  cp "$new_path/.deploy-bin/GopherAI-frontend" "$new_path/GopherAI-frontend"
  cp "$new_path/.deploy-bin/GopherAI-collaboration-eval" "$new_path/GopherAI-collaboration-eval"
  chmod 0755 "$new_path/GopherAI" "$new_path/GopherAI-index-worker" "$new_path/common/mcp/gopherai-mcp" "$new_path/GopherAI-frontend" "$new_path/GopherAI-collaboration-eval"
  rm -rf -- "$new_path/.deploy-bin"
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

stop_application() {
  stop_pid_file frontend; stop_pid_file mcp; stop_pid_file index-worker; stop_pid_file backend
  stop_legacy_matches '^\./GopherAI$'
  stop_legacy_matches '^\./GopherAI-index-worker$'
  stop_legacy_matches '^\./gopherai-mcp -mode server$'
  stop_legacy_matches '^\./GopherAI-frontend '
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
  service mysql start >/dev/null 2>&1 || true
  for attempt in $(seq 1 30); do mysqladmin ping --silent >/dev/null 2>&1 && break; sleep 1; done
  mysqladmin ping --silent >/dev/null 2>&1 || return 1
  wait_tcp rabbitmq 5672 60 || return 1
  wait_tcp redis-vector 6379 60 || return 1

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
  start_release "$project_path"; return 1
}

echo "[container] stopping previous application processes"
stop_application
if [ -d "$project_path" ]; then [ ! -e "$backup_path" ] || { echo "backup exists: $backup_path" >&2; exit 1; }; mv "$project_path" "$backup_path"; fi
mv "$new_path" "$project_path"
if [ -d "$backup_path" ]; then move_runtime_dir uploads; fi

echo "[container] starting release"
if ! start_release "$project_path"; then rollback_release; exit 1; fi

echo "[container] release active: $release_id"
echo "[container] bundle sha256: $expected_sha"
sha256sum "$project_path/GopherAI" "$project_path/GopherAI-index-worker" "$project_path/common/mcp/gopherai-mcp" "$project_path/GopherAI-frontend" "$project_path/GopherAI-collaboration-eval" 2>/dev/null || true
pgrep -af '^\./GopherAI$|^\./GopherAI-index-worker$|^\./gopherai-mcp -mode server$|^\./GopherAI-frontend |node .*vue-cli-service.*serve' || true
echo "[container] sanitized backend log tail"
tail -n 30 "$project_path/backend.log" 2>/dev/null | sed -E 's#(amqp://)[^@]+@#\1***:***@#g' || true
echo "[container] index worker log tail"
tail -n 30 "$project_path/index-worker.log" 2>/dev/null | sed -E 's#(amqp://)[^@]+@#\1***:***@#g' || true
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
        Replace("__EXPECTED_SHA_RAW__", $bundleHash).
        Replace("__SKIP_FRONTEND_RAW__", $skipFrontendText).
        Replace("__DEPLOY_CONFIG_RAW__", $deployConfigText).
        Replace("__BUILD_IN_CONTAINER_RAW__", $buildInContainerText)
    Invoke-Remote -Command (New-RemoteBashCommand -Script $remoteScript)
}
finally {
    foreach ($name in $environmentNames) {
        $originalValue = $originalEnvironment[$name]
        if ($null -eq $originalValue) { Remove-Item "Env:$name" -ErrorAction SilentlyContinue }
        else { [Environment]::SetEnvironmentVariable($name, $originalValue, "Process") }
    }
    if (Test-Path -LiteralPath $tempRoot) { Remove-Item -LiteralPath $tempRoot -Recurse -Force }
}
