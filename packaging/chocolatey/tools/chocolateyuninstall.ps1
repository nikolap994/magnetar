$ErrorActionPreference = 'Stop'
$exe = Join-Path $env:LOCALAPPDATA 'Programs\Magnetar\uninstall.exe'
if (Test-Path $exe) { Start-Process -FilePath $exe -ArgumentList '/uninstall', '/S' -Wait }
