$ErrorActionPreference = 'Stop'
$packageArgs = @{
  packageName    = 'magnetar'
  fileType       = 'exe'
  url64bit       = 'https://github.com/nikolap994/magnetar/releases/download/v0.3.1/magnetar_0.3.1_windows_amd64_setup.exe'
  checksum64     = 'b0a2bc4932c676c8edce89cf061d77246ddcb8fe75cadaf2cbb424816b3358d4'
  checksumType64 = 'sha256'
  silentArgs     = '/S'
  validExitCodes = @(0)
}
Install-ChocolateyPackage @packageArgs
