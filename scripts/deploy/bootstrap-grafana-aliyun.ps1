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
expected_version=13.2.1
package_url=https://dl.grafana.com/grafana/release/13.2.1/grafana_13.2.1_33191028959_linux_amd64.deb
expected_sha256=b4f088f661c5103746f23cb5cbbe2b2bb2e18ba9870ad68a3c2a90451f511ded

if command -v grafana >/dev/null 2>&1 && grafana --version 2>&1 | grep -q "version ${expected_version}"; then
  grafana --version
  exit 0
fi

export DEBIAN_FRONTEND=noninteractive
policy_file=/usr/sbin/policy-rc.d
policy_backup=/tmp/gopherai-grafana-policy-rc.d.backup
had_policy=false
if [ -e "$policy_file" ]; then
  cp -a "$policy_file" "$policy_backup"
  had_policy=true
fi
work_dir="$(mktemp -d /tmp/gopherai-grafana-bootstrap.XXXXXX)"
restore_bootstrap() {
  if [ "$had_policy" = "true" ]; then
    mv -f "$policy_backup" "$policy_file"
  else
    rm -f -- "$policy_file"
  fi
  case "$work_dir" in /tmp/gopherai-grafana-bootstrap.*) rm -rf -- "$work_dir" ;; esac
}
trap restore_bootstrap EXIT
printf '#!/bin/sh\nexit 101\n' > "$policy_file"
chmod 0755 "$policy_file"

apt-get update -qq
apt-get install -y --no-install-recommends adduser ca-certificates curl libfontconfig1 musl
package_path="$work_dir/grafana.deb"
curl --fail --location --retry 3 --connect-timeout 20 --output "$package_path" "$package_url"
actual_sha256="$(sha256sum "$package_path" | awk '{print $1}')"
[ "$actual_sha256" = "$expected_sha256" ] || { echo "Grafana package checksum mismatch" >&2; exit 1; }
dpkg -i "$package_path"
command -v grafana >/dev/null 2>&1
grafana --version 2>&1 | grep -q "version ${expected_version}"
grafana --version
'@
$containerEncoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($containerScript))
$remoteCommand = "docker exec $ContainerName bash -lc `"printf %s '$containerEncoded' | base64 -d | bash`""

& ssh -F $SshConfigPath -o BatchMode=yes -o ConnectTimeout=30 -o ServerAliveInterval=10 -o ServerAliveCountMax=6 $HostAlias $remoteCommand
if ($LASTEXITCODE -ne 0) {
    throw "Grafana bootstrap failed with exit code $LASTEXITCODE"
}
