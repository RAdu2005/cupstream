<#
Builds the trimmed GStreamer runtime for the Windows installer: the plugins
in gstreamer-plugins.txt, and every DLL from the GStreamer installation that
they or the app load, found by following their imports (dumpbin).

  gstreamer-bundle.ps1 -GstRoot C:\gstreamer -Exe target\debug\cupstream-cli.exe -Out app\src-tauri\gstreamer

The result: <Out>\bin\*.dll (installed next to cupstream-app.exe) and
<Out>\gstreamer-1.0\*.dll (the plugins). The app finds them at startup
(cupstream_media::use_bundled_gstreamer).
#>
param(
  [Parameter(Mandatory)] [string]$GstRoot,
  [Parameter(Mandatory)] [string]$Exe,
  [Parameter(Mandatory)] [string]$Out
)
$ErrorActionPreference = 'Stop'

$vswhere = "${env:ProgramFiles(x86)}\Microsoft Visual Studio\Installer\vswhere.exe"
$dumpbin = & $vswhere -latest -products * -find 'VC\Tools\MSVC\**\bin\Hostx64\x64\dumpbin.exe' | Select-Object -First 1
if (-not $dumpbin) { throw 'dumpbin.exe not found: install the MSVC build tools' }

$pluginDir = Join-Path $GstRoot 'lib\gstreamer-1.0'
$binDir = Join-Path $GstRoot 'bin'
$plugins = Get-Content (Join-Path $PSScriptRoot 'gstreamer-plugins.txt') |
  ForEach-Object { $_.Trim() } | Where-Object { $_ -and -not $_.StartsWith('#') }

Remove-Item $Out -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force "$Out\bin", "$Out\gstreamer-1.0" | Out-Null

# Plugins, and the files whose imports must be satisfied.
$queue = [System.Collections.Generic.Queue[string]]::new()
$queue.Enqueue((Resolve-Path $Exe).Path)
foreach ($p in $plugins) {
  $file = Join-Path $pluginDir "gst$p.dll"
  if (-not (Test-Path $file)) { throw "plugin $p is not in $pluginDir" }
  Copy-Item $file "$Out\gstreamer-1.0\"
  $queue.Enqueue($file)
}

# Follow imports; only DLLs that GStreamer ships are copied, the rest are
# Windows' own.
$seen = @{}
while ($queue.Count -gt 0) {
  $file = $queue.Dequeue()
  $deps = & $dumpbin /nologo /dependents $file | ForEach-Object { $_.Trim() } | Where-Object { $_ -match '\.dll$' }
  foreach ($dll in $deps) {
    $key = $dll.ToLowerInvariant()
    if ($seen.ContainsKey($key)) { continue }
    $seen[$key] = $true
    $src = Join-Path $binDir $dll
    if (Test-Path $src) {
      Copy-Item $src "$Out\bin\"
      $queue.Enqueue($src)
    }
  }
}

$size = { param($d) (Get-ChildItem $d -File | Measure-Object Length -Sum).Sum / 1MB }
"{0} plugins ({1:N1} MB), {2} DLLs ({3:N1} MB)" -f $plugins.Count, (& $size "$Out\gstreamer-1.0"), (Get-ChildItem "$Out\bin").Count, (& $size "$Out\bin")
