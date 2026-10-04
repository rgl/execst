#!/bin/bash
set -euxo pipefail

# test.
make release-snapshot
export EXECST_USERNAME="tester"
export EXECST_PASSWORD="$(pwsh -c - <<'EOF'
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$u = -join ((65..90)  | Get-Random -Count 4 | ForEach-Object { [char]$_ })
$l = -join ((97..122) | Get-Random -Count 4 | ForEach-Object { [char]$_ })
$d = -join ((48..57)  | Get-Random -Count 4 | ForEach-Object { [char]$_ })
-join (($u + $l + $d).ToCharArray() | Sort-Object { Get-Random })
EOF
)"
pwsh -c - <<'EOF'
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
Write-Host "Installing the Carbon.Security PowerShell module..."
Install-Module -Name Carbon.Security -RequiredVersion 1.0.5 -Force
Import-Module Carbon.Security
Write-Host "Creating the $env:EXECST_USERNAME user..."
New-LocalUser -Name $env:EXECST_USERNAME -Password (ConvertTo-SecureString $env:EXECST_PASSWORD -AsPlainText -Force) -PasswordNeverExpires | Out-Null
Write-Host "Granting the SeBatchLogonRight user right to the $env:EXECST_USERNAME user..."
Grant-CPrivilege -Identity $env:EXECST_USERNAME -Privilege SeBatchLogonRight
EOF
MSYS2_ARG_CONV_EXCL='*' ./dist/execst_windows_amd64_v3/execst.exe \
  -- \
  whoami -all

# release.
if [[ $GITHUB_REF == refs/tags/v* ]]; then
  make release
fi
