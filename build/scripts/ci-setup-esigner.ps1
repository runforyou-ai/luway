$ErrorActionPreference = 'Stop'

# GitHub 托管 Windows 使用临时用户，签名前安装 CKA 并保存安装器生成的空白配置。
$installDir = Join-Path $env:LOCALAPPDATA 'eSignerCKA'
$archive = Join-Path $env:RUNNER_TEMP 'esigner-cka-1.0.8.zip'
$extract = Join-Path $env:RUNNER_TEMP 'esigner-cka-1.0.8'
Invoke-WebRequest -Uri 'https://github.com/SSLcom/eSignerCKA/releases/download/v1.0.8/SSL.COM-eSigner-CKA_1.0.8.zip' -OutFile $archive
if ((Get-FileHash $archive -Algorithm SHA256).Hash -ne 'c45bcd520a2a77acda150c12a1c233ca2408b3c10c2016b8d0dc8b9aefc0cadb') {
  throw 'eSigner CKA archive checksum mismatch'
}
Expand-Archive $archive -DestinationPath $extract -Force
$installer = Join-Path $extract 'SSL.COM eSigner CKA_1.0.8_build_20250228.exe'

foreach ($arch in 'x86', 'x64') {
  $runtime = Join-Path $env:RUNNER_TEMP "vc_redist.$arch.exe"
  Invoke-WebRequest -Uri "https://aka.ms/vs/17/release/vc_redist.$arch.exe" -OutFile $runtime
  $process = Start-Process $runtime -ArgumentList '/install', '/quiet', '/norestart' -Wait -PassThru
  if ($process.ExitCode -notin 0, 1638, 3010) { throw "VC runtime $arch installation failed: $($process.ExitCode)" }
}

$process = Start-Process $installer -ArgumentList '/CURRENTUSER', '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', "/DIR=`"$installDir`"" -Wait -PassThru
if ($process.ExitCode -notin 0, 3010) { throw "CKA installation failed: $($process.ExitCode)" }
foreach ($program in 'RegisterKSP.exe', 'eSignerCSP.Config.exe') {
  & (Join-Path $installDir $program) | Out-Host
  if ($LASTEXITCODE -ne 0) { throw "$program failed: $LASTEXITCODE" }
}
$config = Join-Path $env:APPDATA 'eSignerCKA\Config\esignerapp-pro.data'
if (-not (Test-Path $config)) { throw 'CKA installer did not create its default configuration' }
Copy-Item $config (Join-Path $installDir 'ci-default-config.data') -Force
if (-not (Test-Path (Join-Path $installDir 'eSignerCKATool.exe'))) { throw 'CKA command-line tool is missing' }
