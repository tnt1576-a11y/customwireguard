# Isolated recovery guards. All process, task and filesystem operations below
# are mocked; this test never starts or stops a tunnel.
$ErrorActionPreference='Stop'
Set-StrictMode -Version 2
$source=Join-Path $PSScriptRoot 'App-Tunnel.ps1'
$errors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile($source,[ref]$null,[ref]$errors)
if($errors){throw 'App-Tunnel.ps1 has syntax errors.'}
$definition=$ast.FindAll({param($n) $n -is [Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -eq 'Recover-UntrustedRuntime'},$false)
Invoke-Expression $definition.Extent.Text
$runtime=Join-Path $env:ProgramData 'CWG-AppTunnel';$self=Join-Path $runtime 'App-Tunnel.ps1';$task='CWG-AppTunnel';$statePath=Join-Path $runtime 'state.json'
$ps=Join-Path $env:WINDIR 'System32\WindowsPowerShell\v1.0\powershell.exe'
$arguments='-NoProfile -NonInteractive -ExecutionPolicy Bypass -File "'+$self+'" -Action '
$script:events=New-Object 'Collections.Generic.List[string]'
$script:badTask=$false;$script:rootLink=$false;$script:childLink=$false
function Assert($value,$message){if(-not $value){throw $message}}
function Get-Item {param($LiteralPath) [pscustomobject]@{Attributes=if($script:rootLink){[IO.FileAttributes]::ReparsePoint}else{[IO.FileAttributes]::Directory}}}
function Get-ScheduledTask {
 param($TaskName,$ErrorAction)
 [pscustomobject]@{Actions=@([pscustomobject]@{Execute=$ps;Arguments=if($script:badTask){'-File C:\unrelated.ps1'}else{$arguments+'Worker'}})}
}
function Unregister-ScheduledTask {param([Parameter(ValueFromPipeline)]$InputObject,$Confirm) process{$script:events.Add('unregister')}}
function Get-CimInstance {
 param($ClassName,$Filter)
 @([pscustomobject]@{ExecutablePath=$ps;CommandLine='"'+$ps+'" '+$arguments+'Worker';ProcessId=31;CreationDate=[datetime]'2026-01-01'},
   [pscustomobject]@{ExecutablePath=$ps;CommandLine='"'+$ps+'" -File C:\unrelated.ps1';ProcessId=34;CreationDate=[datetime]'2026-01-01'})
}
function Get-Process {
 param($Name,$Id,$ErrorAction)
 if($Id){$p=[pscustomobject]@{Id=$Id;Path=$ps;StartTime=[datetime]'2026-01-01'};$p | Add-Member ScriptMethod WaitForExit {$true};return $p}
 @([pscustomobject]@{Id=32;Path=(Join-Path $runtime ($Name+'.exe'));StartTime=[datetime]'2026-01-01'},
   [pscustomobject]@{Id=34;Path=('C:\Other\'+$Name+'.exe');StartTime=[datetime]'2026-01-01'})
}
function Stop-Process {param($Id,[switch]$Force) $script:events.Add('stop:'+ $Id)}
function Stop-Owned {param($record) $script:events.Add('owned:'+ $record.Id)}
function Get-State {throw 'Recovery must not trust saved process IDs.'}
function Get-ChildItem {
 param($LiteralPath,[switch]$Force)
 if($script:childLink){[pscustomobject]@{PSIsContainer=$false;Attributes=[IO.FileAttributes]::ReparsePoint;FullName=$runtime+'\linked'}}
}
function Set-PrivateRuntimeAcl {param($Path,$Directory) $script:events.Add('acl')}
function Test-Path {param($LiteralPath) $false}
function Move-Item {param($LiteralPath,$Destination) Assert ($LiteralPath -eq $runtime -and (Split-Path $Destination -Parent) -eq (Split-Path $runtime -Parent)) 'Recovery escaped its directory.';$script:events.Add('retire')}
function Create-Runtime {$script:events.Add('create')}
function Write-CwgJson {param($Path,$Value) $script:saved=$Value;$script:events.Add('state')}

$script:badTask=$true
$rejected=$false;try{Recover-UntrustedRuntime}catch{$rejected=$true}
Assert ($rejected -and $script:events.Count -eq 0) 'Unexpected task must be refused before any mutation.'
$script:badTask=$false;$script:rootLink=$true
$rejected=$false;try{Recover-UntrustedRuntime}catch{$rejected=$true}
Assert ($rejected -and $script:events.Count -eq 0) 'Linked runtime must be refused before any mutation.'
$script:rootLink=$false
Recover-UntrustedRuntime | Out-Null
Assert ($script:events.Contains('stop:31') -and -not $script:events.Contains('stop:34') -and -not $script:events.Contains('owned:34')) 'Only identified CWG processes may stop.'
Assert ($script:events.Contains('retire') -and $script:events.Contains('state')) 'Recovery must retire old files and save stopped state.'
foreach($key in @('Supervisor','Engine','Carrier','StartedUTC','UpdatedUTC','Status','Mode','TestDeadlineUTC')){Assert ($script:saved.ContainsKey($key)) ('Recovered state is missing '+$key)}
Assert ($script:saved.Status -eq 'Stopped' -and $null -eq $script:saved.Supervisor) 'Recovered state must be stopped without process IDs.'
$script:events.Clear();$script:childLink=$true
$rejected=$false;try{Recover-UntrustedRuntime}catch{$rejected=$true}
Assert ($rejected -and $script:events.Contains('stop:31') -and -not $script:events.Contains('acl') -and -not $script:events.Contains('retire')) 'Child links must not be followed, changed or retired.'
Write-Output 'Recovery guard checks passed; no real processes, tasks, files or networking were changed.'
