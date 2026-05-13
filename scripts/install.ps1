param(
  [string]$Version = "latest"
)

$ErrorActionPreference = "Stop"

$Repo = "andre-carbajal/GoEverything"
$Project = "goeverything"
$Binary = "ge.exe"

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { "amd64" }
  "ARM64" { "arm64" }
  default { throw "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}

if ($Version -eq "latest") {
  $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest"
  $tag = $release.tag_name
} else {
  $tag = if ($Version.StartsWith("v")) { $Version } else { "v$Version" }
}

$versionNoV = $tag.TrimStart("v")
$asset = "${Project}_${versionNoV}_windows_${arch}.zip"
$url = "https://github.com/$Repo/releases/download/$tag/$asset"

$tempDir = Join-Path $env:TEMP "goeverything-install-$(Get-Random)"
New-Item -ItemType Directory -Path $tempDir | Out-Null

try {
  $zipPath = Join-Path $tempDir $asset
  Invoke-WebRequest -Uri $url -OutFile $zipPath
  Expand-Archive -Path $zipPath -DestinationPath $tempDir -Force

  $sourceBinary = Join-Path $tempDir $Binary
  if (-not (Test-Path $sourceBinary)) {
    throw "Binary $Binary not found in archive"
  }

  $targetDir = Join-Path $env:USERPROFILE "scoop\shims"
  if (-not (Test-Path $targetDir)) {
    $targetDir = Join-Path $env:USERPROFILE "AppData\Local\Microsoft\WindowsApps"
    if (-not (Test-Path $targetDir)) {
      $targetDir = Join-Path $env:USERPROFILE "bin"
      New-Item -ItemType Directory -Path $targetDir -Force | Out-Null
    }
  }

  Copy-Item -Path $sourceBinary -Destination (Join-Path $targetDir $Binary) -Force
  Write-Host "Installed ge to $targetDir\\$Binary"
  Write-Host "If ge is not found, add $targetDir to your PATH."
}
finally {
  Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
}
