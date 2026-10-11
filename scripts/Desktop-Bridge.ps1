#Requires -RunAsAdministrator
$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue'
[Console]::InputEncoding=New-Object Text.UTF8Encoding($false)
[Console]::OutputEncoding=New-Object Text.UTF8Encoding($false)
. (Join-Path $PSScriptRoot 'App-Tunnel.Common.ps1')
. (Join-Path $PSScriptRoot 'Import-CwgProfile.ps1')
try{
    $raw=[Console]::In.ReadToEnd()
    if($raw.Length -gt 65536){throw 'Application request is too large.'}
    $job=$raw | ConvertFrom-Json
    $root=Split-Path $PSScriptRoot -Parent
    switch([string]$job.Action){
        'Import' {
            $state=Join-Path $env:ProgramData 'CWG-AppTunnel\state.json'
            if((Test-Path $state) -and (Read-CwgJson $state).Status -in @('Starting','Running','Reconnecting')){throw 'Disconnect before importing another profile.'}
            $result=Import-CwgProfile ([string]$job.Archive) $root
        }
        'InstallEngine' {
            $offline=Join-Path $root 'engine\sing-box-1.14.3-windows-amd64.zip'
            if(Test-Path $offline){& (Join-Path $PSScriptRoot 'Install-AppEngine.ps1') -ArchivePath $offline *> $null}
            else{& (Join-Path $PSScriptRoot 'Install-AppEngine.ps1') *> $null}
            $result=@{Installed=$true}
        }
        'Start' {
            $bundle=(Resolve-Path -LiteralPath ([string]$job.Bundle)).Path
            if($job.Mode -notin @('All','Include','Exclude')){throw 'Choose Full PC or a split-tunnel mode.'}
            $settings=[pscustomobject]@{mode=[string]$job.Mode;applications=@($job.Applications)}
            $null=Get-CwgAppPatterns $settings
            $transport=Read-CwgJson (Join-Path $bundle 'client.json')
            $null=New-CwgAppConfig ([IO.File]::ReadAllText((Join-Path $bundle 'cwg-full.conf'))) $transport $settings 'Ethernet' @()
            Write-CwgJson (Join-Path $bundle 'apps.json') $settings
            $minutes=0
            if($job.PSObject.Properties.Name -contains 'TestMinutes'){$minutes=[int]$job.TestMinutes;if($minutes -lt 0 -or $minutes -gt 120){throw 'Invalid test deadline.'}}
            & (Join-Path $PSScriptRoot 'App-Tunnel.ps1') -Action Start -Bundle $bundle -TestMinutes $minutes *> $null
            $result=@{Started=$true}
        }
        'Stop' {& (Join-Path $PSScriptRoot 'App-Tunnel.ps1') -Action Stop *> $null;$result=@{Stopped=$true}}
        default {throw 'Unsupported application action.'}
    }
    [pscustomobject]@{Success=$true;Result=$result} | ConvertTo-Json -Depth 8 -Compress
}catch{
    $message=[string]$_.Exception.Message
    if($message.Length -gt 800){$message=$message.Substring(0,800)}
    [pscustomobject]@{Success=$false;Error=$message} | ConvertTo-Json -Compress
    exit 1
}
