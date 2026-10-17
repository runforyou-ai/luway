param(
  [Parameter(Mandatory)][string]$Executable,
  [Parameter(Mandatory)][string]$Installer,
  [Parameter(Mandatory)][scriptblock]$Package,
  [string]$Timestamp = 'http://ts.ssl.com'
)
$ErrorActionPreference = 'Stop'
$tool = Join-Path $env:LOCALAPPDATA 'eSignerCKA\eSignerCKATool.exe'
if (-not (Test-Path $tool)) { throw 'The runner requires eSigner CKA and its x64/x86 VC runtimes' }
foreach ($name in 'ES_USERNAME','ES_PASSWORD','ES_TOTP_SECRET') {
  if (-not [Environment]::GetEnvironmentVariable($name)) { throw "Missing $name" }
}
# CKA 使用当前用户的共享证书配置；跨仓库、跨执行器串行完成配置、签名和卸载。
$mutex = [Threading.Mutex]::new($false, 'Global\CI-eSigner-CKA')
$locked = $false
$configDirectory = Join-Path $env:APPDATA 'eSignerCKA\Config'
$template = Join-Path (Split-Path $tool) 'ci-default-config.data'
if (-not (Test-Path $template)) { throw 'Provision the CKA default configuration template before signing' }
$key = Join-Path $env:RUNNER_TEMP ("esigning-" + [Guid]::NewGuid().ToString('N') + '.key')
try {
  try { $locked = $mutex.WaitOne([TimeSpan]::FromMinutes(20)) }
  catch [Threading.AbandonedMutexException] { $locked = $true }
  if (-not $locked) { throw 'Timed out waiting for the signing slot' }
  # CKA 的命令行配置依赖安装器生成的空白配置，不能直接删除该文件。
  New-Item -ItemType Directory -Force $configDirectory | Out-Null
  & $tool unload | Out-Host
  Copy-Item $template (Join-Path $configDirectory 'esignerapp-pro.data') -Force
  & $tool config -mode product -user $env:ES_USERNAME -pass $env:ES_PASSWORD -totp $env:ES_TOTP_SECRET -key $key -r | Out-Host
  if ($LASTEXITCODE -ne 0) { throw "CKA config failed: $LASTEXITCODE" }
  & $tool load | Out-Host
  if ($LASTEXITCODE -ne 0) { throw "CKA load failed: $LASTEXITCODE" }
  $certs = @(Get-ChildItem Cert:\CurrentUser\My -CodeSigningCert | Where-Object { $_.NotAfter -gt (Get-Date) })
  if ($certs.Count -ne 1) { throw "Expected one loaded code-signing certificate, found $($certs.Count)" }
  $thumbprint = $certs[0].Thumbprint
  & wails3 tool sign --input $Executable --thumbprint $thumbprint --timestamp $Timestamp | Out-Host
  if ($LASTEXITCODE -ne 0) { throw 'Executable signing failed' }
  & $Package
  & wails3 tool sign --input $Installer --thumbprint $thumbprint --timestamp $Timestamp | Out-Host
  if ($LASTEXITCODE -ne 0) { throw 'Installer signing failed' }
  foreach ($file in $Executable,$Installer) {
    $signature = Get-AuthenticodeSignature $file
    if ($signature.Status -ne 'Valid' -or -not $signature.TimeStamperCertificate -or $signature.SignerCertificate.Thumbprint -ne $thumbprint) {
      throw "Signature verification failed: $file ($($signature.Status))"
    }
    Write-Host "Verified signed and timestamped: $file"
  }
} finally {
  if ($locked) {
    try { & $tool unload | Out-Host }
    finally {
      try { Copy-Item $template (Join-Path $configDirectory 'esignerapp-pro.data') -Force }
      finally {
        Remove-Item $key -Force -ErrorAction SilentlyContinue
        $mutex.ReleaseMutex()
      }
    }
  }
  $mutex.Dispose()
}
