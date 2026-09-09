[CmdletBinding()]
param(
    [string]$HostAlias = "gopherai-aliyun",
    [string]$SshConfigPath = "$env:USERPROFILE\.ssh\config",
    [string]$ContainerName = "gopherai2"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

if (-not (Get-Command ssh -ErrorAction SilentlyContinue)) {
    throw "ssh was not found in PATH"
}
if ($ContainerName -notmatch '^[a-zA-Z0-9_.-]+$') {
    throw "invalid container name"
}

$containerScript = @'
set -Eeuo pipefail
if command -v prometheus >/dev/null 2>&1 && command -v promtool >/dev/null 2>&1; then
  prometheus --version | head -1
  promtool --version | head -1
  exit 0
fi

export DEBIAN_FRONTEND=noninteractive
policy_file=/usr/sbin/policy-rc.d
policy_backup=/tmp/gopherai-policy-rc.d.backup
had_policy=false
if [ -e "$policy_file" ]; then
  cp -a "$policy_file" "$policy_backup"
  had_policy=true
fi
restore_policy() {
  if [ "$had_policy" = "true" ]; then
    mv -f "$policy_backup" "$policy_file"
  else
    rm -f -- "$policy_file"
  fi
}
trap restore_policy EXIT
printf '#!/bin/sh\nexit 101\n' > "$policy_file"
chmod 0755 "$policy_file"
apt-get update -qq
apt-get install -y --no-install-recommends prometheus
command -v prometheus >/dev/null 2>&1
command -v promtool >/dev/null 2>&1
prometheus --version | head -1
promtool --version | head -1
'@
$containerEncoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($containerScript))
$remoteCommand = "docker exec $ContainerName bash -lc `"printf %s '$containerEncoded' | base64 -d | bash`""

& ssh -F $SshConfigPath -o BatchMode=yes -o ConnectTimeout=30 -o ServerAliveInterval=10 -o ServerAliveCountMax=6 $HostAlias $remoteCommand
if ($LASTEXITCODE -ne 0) {
    throw "Prometheus bootstrap failed with exit code $LASTEXITCODE"
}
