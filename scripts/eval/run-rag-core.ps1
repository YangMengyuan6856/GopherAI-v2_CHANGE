[CmdletBinding()]
param(
    [string]$SshHost = "gopherai-aliyun",
    [string]$Container = "gopherai2",
    [string]$Candidate = ""
)

$ErrorActionPreference = "Stop"
$repo = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
if ([string]::IsNullOrWhiteSpace($Candidate)) {
    $Candidate = (git -C $repo rev-parse HEAD).Trim()
}
if ($Candidate -notmatch '^[a-zA-Z0-9._-]+$') {
    throw "candidate contains unsupported characters"
}
if ($Container -notmatch '^[a-zA-Z0-9._-]+$') {
    throw "container contains unsupported characters"
}

$tempRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("gopherai-rag-eval-" + [guid]::NewGuid().ToString("N"))
$remoteRoot = "/root/GopherAI_Eval"
$localReports = Join-Path $repo "evals\reports"
$sshOptions = @("-o", "BatchMode=yes", "-o", "ConnectTimeout=30", "-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=12")
$scpOptions = @("-o", "BatchMode=yes", "-o", "ConnectTimeout=30", "-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=12")
New-Item -ItemType Directory -Path $tempRoot -Force | Out-Null
New-Item -ItemType Directory -Path $localReports -Force | Out-Null

$previousGoos = $env:GOOS
$previousGoarch = $env:GOARCH
$previousCgo = $env:CGO_ENABLED
$previousGoroot = $env:GOROOT
try {
    $env:GOOS = "linux"
    $env:GOARCH = "amd64"
    $env:CGO_ENABLED = "0"
    $env:GOROOT = "C:\Program Files\Go"
    $binary = Join-Path $tempRoot "GopherAI-rag-eval"
    Write-Host "[eval] building Linux evaluator locally"
    & "C:\Program Files\Go\bin\go.exe" -C $repo build -p 1 -o $binary ./cmd/rag-eval
    if ($LASTEXITCODE -ne 0) { throw "evaluator build failed" }

    Copy-Item (Join-Path $repo "evals\devsupport-rag-core-v2.jsonl") (Join-Path $tempRoot "devsupport-rag-core-v2.jsonl")
    Copy-Item (Join-Path $repo "evals\fixtures\kb-fixture-v2.json") (Join-Path $tempRoot "kb-fixture-v2.json")

    Write-Host "[eval] uploading isolated dataset and prebuilt evaluator"
    & ssh @sshOptions $SshHost "mkdir -p '$remoteRoot'"
    if ($LASTEXITCODE -ne 0) { throw "remote eval directory creation failed" }
    & scp @scpOptions (Join-Path $tempRoot "GopherAI-rag-eval") (Join-Path $tempRoot "devsupport-rag-core-v2.jsonl") (Join-Path $tempRoot "kb-fixture-v2.json") "${SshHost}:${remoteRoot}/"
    if ($LASTEXITCODE -ne 0) { throw "eval upload failed" }
    & ssh @sshOptions $SshHost "docker exec '$Container' mkdir -p '$remoteRoot' && docker cp '$remoteRoot/GopherAI-rag-eval' '${Container}:$remoteRoot/GopherAI-rag-eval' && docker cp '$remoteRoot/devsupport-rag-core-v2.jsonl' '${Container}:$remoteRoot/devsupport-rag-core-v2.jsonl' && docker cp '$remoteRoot/kb-fixture-v2.json' '${Container}:$remoteRoot/kb-fixture-v2.json' && docker exec '$Container' chmod 0755 '$remoteRoot/GopherAI-rag-eval'"
    if ($LASTEXITCODE -ne 0) { throw "copy evaluator into container failed" }

    Write-Host "[eval] running 60-case RAG slice against an isolated Redis index"
    & ssh @sshOptions $SshHost "docker exec -e GOMAXPROCS=1 -w /root/GopherAI- '$Container' nice -n 10 '$remoteRoot/GopherAI-rag-eval' -dataset '$remoteRoot/devsupport-rag-core-v2.jsonl' -fixture '$remoteRoot/kb-fixture-v2.json' -out-json '$remoteRoot/devsupport-rag-core-latest.json' -out-md '$remoteRoot/devsupport-rag-core-latest.md' -candidate '$Candidate'"
    $evalExit = $LASTEXITCODE

    & ssh @sshOptions $SshHost "docker cp '${Container}:$remoteRoot/devsupport-rag-core-latest.json' '$remoteRoot/devsupport-rag-core-latest.json' && docker cp '${Container}:$remoteRoot/devsupport-rag-core-latest.md' '$remoteRoot/devsupport-rag-core-latest.md'"
    if ($LASTEXITCODE -ne 0) { throw "copy reports out of container failed" }
    & scp @scpOptions "${SshHost}:${remoteRoot}/devsupport-rag-core-latest.json" "${SshHost}:${remoteRoot}/devsupport-rag-core-latest.md" "$localReports/"
    if ($LASTEXITCODE -ne 0) { throw "report download failed" }

    Write-Host "[eval] reports: $localReports"
    if ($evalExit -ne 0) { throw "RAG evaluation failed its release gate; inspect the downloaded report" }
}
finally {
    $env:GOOS = $previousGoos
    $env:GOARCH = $previousGoarch
    $env:CGO_ENABLED = $previousCgo
    $env:GOROOT = $previousGoroot
    if (Test-Path -LiteralPath $tempRoot) {
        Remove-Item -LiteralPath $tempRoot -Recurse -Force
    }
}
