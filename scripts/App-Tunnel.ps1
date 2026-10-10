#Requires -RunAsAdministrator
[CmdletBinding()]
param(
    [ValidateSet('Start','Stop','Status','Worker','Watchdog')][string]$Action='Status',
    [string]$Bundle,
    [string]$Settings,
    [ValidateRange(0,120)][int]$TestMinutes=0
)
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'App-Tunnel.Common.ps1')
$runtime=Join-Path $env:ProgramData 'CWG-AppTunnel'
$statePath=Join-Path $runtime 'state.json'
$stopPath=Join-Path $runtime 'stop.request'
$task='CWG-AppTunnel'
$self=Join-Path $runtime 'App-Tunnel.ps1'
trap {
    if($Action -in @('Worker','Watchdog') -and (Test-Path $runtime)){
        [IO.File]::WriteAllText((Join-Path $runtime 'last-error.txt'),([string]$_.Exception.Message+[Environment]::NewLine+$_.ScriptStackTrace))
    }
    throw $_
}
function Get-State { if(Test-Path -LiteralPath $statePath){return Read-CwgJson $statePath};return $null }
function Assert-Runtime {
    $item=Get-Item -LiteralPath $runtime
    if($item.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'The runtime directory cannot be a link.'}
    $acl=Get-Acl -LiteralPath $runtime
    if(-not $acl.AreAccessRulesProtected){throw 'The runtime directory has unexpected inherited permissions.'}
    foreach($rule in $acl.Access){if($rule.AccessControlType -eq 'Allow' -and $rule.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value -notin @('S-1-5-18','S-1-5-32-544')){throw 'The runtime directory has unexpected access permissions.'}}
}
function Create-Runtime {
    if(Test-Path $runtime){Assert-Runtime;return}
    New-Item -ItemType Directory -Path $runtime | Out-Null
    $acl=New-Object Security.AccessControl.DirectorySecurity
    $acl.SetAccessRuleProtection($true,$false)
    foreach($sid in @('S-1-5-18','S-1-5-32-544')){
        $identity=New-Object Security.Principal.SecurityIdentifier($sid)
        $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($identity,'FullControl','ContainerInherit,ObjectInherit','None','Allow')))
    }
    $acl.SetOwner((New-Object Security.Principal.SecurityIdentifier('S-1-5-32-544')))
    Set-Acl -LiteralPath $runtime -AclObject $acl;Assert-Runtime
}
function Owned-Process($record){
    if(-not $record){return $null}
    $process=Get-Process -Id $record.Id -ErrorAction SilentlyContinue
    if(-not $process){return $null}
    if($process.StartTime.ToUniversalTime().ToString('o') -ne $record.Start){return $null}
    if($process.Path -ne $record.Path){throw 'A recorded process has an unexpected executable path.'}
    return $process
}
function Stop-Owned($record){
    $p=Owned-Process $record;if(-not $p){return}
    if($record.Path -eq (Join-Path $runtime 'sing-box.exe')){
        $ps=Join-Path $env:WINDIR 'System32\WindowsPowerShell\v1.0\powershell.exe'
        $signal=Start-Process $ps -ArgumentList ('-NoProfile -NonInteractive -ExecutionPolicy Bypass -File "'+$runtime+'\App-EngineProcess.ps1" -Action Signal -Runtime "'+$runtime+'" -TargetId '+$record.Id+' -ExpectedStart "'+$record.Start+'"') -WindowStyle Hidden -PassThru
        if(-not $signal.WaitForExit(5000)){Stop-Process -Id $signal.Id -Force}
        if($p.WaitForExit(5000)){return}
    }
    Stop-Process -Id $p.Id -Force;if(-not $p.WaitForExit(10000)){throw 'A CWG child process could not be stopped.'}
}
function Spawn-Child([string]$file,[string]$arguments,[string]$log){
    if($file -eq 'cwg-app-carrier.exe'){
        return (& (Join-Path $runtime 'App-EngineProcess.ps1') -Action Carrier -Runtime $runtime -InterfaceIndex $s.InterfaceIndex)
    }
    $p=Start-Process -FilePath (Join-Path $runtime $file) -ArgumentList $arguments -WorkingDirectory $runtime -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $runtime ($log+'.out')) -RedirectStandardError (Join-Path $runtime ($log+'.err'))
    $null=$p.Handle
    return [pscustomobject]@{Id=$p.Id;Path=(Join-Path $runtime $file);Start=$p.StartTime.ToUniversalTime().ToString('o')}
}
function Finish-Session([string]$status,[string]$message){
    $s=Get-State;if(-not $s){return}
    foreach($record in @($s.Engine,$s.Carrier)){Stop-Owned $record}
    # Recover children if the supervisor was interrupted between creation and saving its PID.
    foreach($name in @('cwg-app-carrier','sing-box')){
        foreach($p in @(Get-Process $name -ErrorAction SilentlyContinue)){
            if($p.Path -eq (Join-Path $runtime ($name+'.exe')) -and $p.StartTime.ToUniversalTime() -ge [datetime]$s.StartedUTC){Stop-Owned ([pscustomobject]@{Id=$p.Id;Path=$p.Path;Start=$p.StartTime.ToUniversalTime().ToString('o')})}
        }
    }
    $s.Status=$status;$s.Message=$message;$s.UpdatedUTC=[DateTime]::UtcNow.ToString('o');$s.Engine=$null;$s.Carrier=$null
    Write-CwgJson $statePath $s
}
if($Action -eq 'Status'){
    $s=Get-State;if($s){$s | Select-Object Status,Message,Mode,StartedUTC,UpdatedUTC,Restarts,Interface,TestDeadlineUTC | ConvertTo-Json}else{[pscustomobject]@{Status='Stopped';Message='Application tunnel is stopped.'} | ConvertTo-Json};return
}
if($Action -eq 'Stop'){
    if(-not (Test-Path $runtime)){Write-Output 'Application tunnel is stopped.';return}
    Assert-Runtime
    [IO.File]::WriteAllText($stopPath,'stop')
    $s=Get-State
    if($s -and $s.Supervisor){Stop-Owned $s.Supervisor}
    Finish-Session 'Stopped' 'Normal networking restored.'
    Get-ScheduledTask -TaskName "$task-Worker","$task-Watchdog" -ErrorAction SilentlyContinue | Unregister-ScheduledTask -Confirm:$false
    Write-Output 'Application tunnel stopped; normal networking restored.';return
}
if($Action -in @('Worker','Watchdog')){
    if($PSScriptRoot -ne $runtime -or [Security.Principal.WindowsIdentity]::GetCurrent().User.Value -ne 'S-1-5-18'){throw 'Internal action must run as SYSTEM from the protected runtime.'}
    Assert-Runtime;$s=Get-State;if(-not $s){return}
    if($Action -eq 'Watchdog'){
        if($s.Status -in @('Stopped','Failed')){return}
        $expired=$s.TestDeadlineUTC -and [DateTime]::UtcNow -gt [datetime]$s.TestDeadlineUTC
        $missing=-not (Owned-Process $s.Supervisor)
        $stale=([DateTime]::UtcNow-[datetime]$s.UpdatedUTC).TotalSeconds -gt 30
        if($expired -or $missing -or $stale -or (Test-Path $stopPath)){
            Stop-Owned $s.Supervisor
            Finish-Session 'Stopped' 'Watchdog restored normal networking after the session stopped or expired.'
        }
        return
    }
    $mutex=New-Object Threading.Mutex($false,'Global\CWG-AppTunnel-Worker')
    if(-not $mutex.WaitOne(0)){throw 'Another application supervisor is active.'}
    try{
        $p=Get-Process -Id $PID
        $s.Supervisor=[pscustomobject]@{Id=$PID;Path=$p.Path;Start=$p.StartTime.ToUniversalTime().ToString('o')}
        $s.UpdatedUTC=[DateTime]::UtcNow.ToString('o');Write-CwgJson $statePath $s
        $s.Carrier=Spawn-Child 'cwg-app-carrier.exe' ('client -config "'+$runtime+'\client.json" -interface-index '+$s.InterfaceIndex) 'carrier';Write-CwgJson $statePath $s
        Start-Sleep -Milliseconds 750
        if(-not (Owned-Process $s.Carrier)){throw 'Carrier exited during startup; see carrier.err.'}
        $s.Engine=& (Join-Path $runtime 'App-EngineProcess.ps1') -Action Start -Runtime $runtime;Write-CwgJson $statePath $s
        $ready=$false
        for($attempt=0;$attempt -lt 50;$attempt++){
            if(-not (Owned-Process $s.Engine)){throw 'Application engine exited during startup; see engine.err.'}
            if(Test-Path $stopPath){break}
            $s.UpdatedUTC=[DateTime]::UtcNow.ToString('o');Write-CwgJson $statePath $s
            $device=Get-NetAdapter -Name $s.TunnelInterface -ErrorAction SilentlyContinue
            if($device -and $device.Status -eq 'Up' -and @(Get-NetIPAddress -InterfaceIndex $device.ifIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object IPAddress -eq '172.30.77.1').Count){$ready=$true;break}
            Start-Sleep -Milliseconds 500
        }
        if(-not $ready){throw 'Application adapter did not become ready; see engine.err.'}
        $lastRestart=[datetime]::MinValue
        while(-not (Test-Path $stopPath)){
            if($s.TestDeadlineUTC -and [DateTime]::UtcNow -gt [datetime]$s.TestDeadlineUTC){break}
            if(-not (Owned-Process $s.Engine)){throw 'Application engine stopped; normal networking has been restored.'}
            if(-not (Owned-Process $s.Carrier)){
                $s.Status='Reconnecting';$s.Message='Restarting the UDP transport. Selected connections remain in the tunnel.';$s.UpdatedUTC=[DateTime]::UtcNow.ToString('o');Write-CwgJson $statePath $s
                if(([DateTime]::UtcNow-$lastRestart).TotalSeconds -lt 5){Start-Sleep -Seconds 5}
                if(Test-Path $stopPath){break}
                $s.Carrier=Spawn-Child 'cwg-app-carrier.exe' ('client -config "'+$runtime+'\client.json" -interface-index '+$s.InterfaceIndex) 'carrier'
                $lastRestart=[DateTime]::UtcNow;$s.Restarts++;Write-CwgJson $statePath $s
            }
            $s.Status='Running';$s.Message='Application rules active. Connection availability depends on the server.';$s.UpdatedUTC=[DateTime]::UtcNow.ToString('o');Write-CwgJson $statePath $s
            Start-Sleep -Seconds 2
        }
        Finish-Session 'Stopped' 'Normal networking restored.'
    }catch{Finish-Session 'Failed' ([string]$_.Exception.Message)}finally{$mutex.ReleaseMutex();$mutex.Dispose()}
    return
}
# Start: validate everything before any route or adapter change.
if(-not $Bundle){throw 'Choose the private config folder with -Bundle.'}
$bundlePath=(Resolve-Path -LiteralPath $Bundle).Path
if(-not $Settings){$Settings=Join-Path $bundlePath 'apps.json'}
$preferences=Read-CwgJson (Resolve-Path -LiteralPath $Settings).Path
$cfg=Read-CwgJson (Join-Path $bundlePath 'client.json')
$native=[IO.File]::ReadAllText((Join-Path $bundlePath 'cwg-full.conf'))
$distribution=Split-Path $PSScriptRoot -Parent
$carrier=Join-Path $distribution 'bin\cwg-windows-amd64.exe'
$engine=Join-Path $distribution 'bin\app-engine\sing-box.exe'
if((Get-FileHash -LiteralPath $engine).Hash -ne $script:CwgEngineSHA){throw 'Install the pinned application engine using Install-AppEngine.ps1.'}
& $carrier check -config (Join-Path $bundlePath 'client.json');if($LASTEXITCODE -ne 0){throw 'Invalid carrier configuration.'}
if($cfg.endpoint -notmatch '^(\d+\.\d+\.\d+\.\d+):\d+$'){throw 'App mode currently requires a literal server IPv4 address.'}
$serverIP=$Matches[1]
$route=Find-NetRoute -RemoteIPAddress $serverIP | Where-Object {$_.CimClass.CimClassName -eq 'MSFT_NetRoute'} | Select-Object -First 1
if(-not $route){throw 'No route to the server.'}
$adapter=Get-NetAdapter | Where-Object ifIndex -eq $route.InterfaceIndex | Select-Object -First 1
if(-not $adapter -or -not $adapter.HardwareInterface){throw 'The server must use a physical adapter. Stop the other VPN before starting CWG app mode.'}
$bypass=@($serverIP+'/32')
foreach($ip in @(Get-NetIPAddress -InterfaceIndex $adapter.ifIndex -AddressFamily IPv4)){
    if($ip.PrefixLength -ge 8 -and $ip.PrefixLength -lt 32){$bypass+=Get-CwgIPv4Prefix $ip.IPAddress $ip.PrefixLength}
}
$engineConfig=New-CwgAppConfig $native $cfg $preferences $adapter.Name $bypass
$tunnelInterface='CWG Apps '+[guid]::NewGuid().ToString('N').Substring(0,8)
$engineConfig.inbounds[0].interface_name=$tunnelInterface
$ports=@([int]$cfg.listen.Split(':')[-1],[int]$cfg.wireguard.Split(':')[-1])
if(@(Get-NetUDPEndpoint -LocalPort $ports -ErrorAction SilentlyContinue).Count){throw 'The CWG loopback ports are already in use. Stop the previous client.'}
if(@(Get-Service 'WireGuardTunnel$cwg-*' -ErrorAction SilentlyContinue | Where-Object Status -eq Running).Count){throw 'Stop the existing native CWG tunnel first.'}
$startMutex=New-Object Threading.Mutex($false,'Global\CWG-AppTunnel-Start')
if(-not $startMutex.WaitOne(0)){throw 'Another application start is in progress.'}
try{
    Create-Runtime
    $previous=Get-State
    if($previous -and $previous.Status -notin @('Stopped','Failed')){throw 'An application session already exists. Stop it before starting again.'}
    foreach($name in @('App-Tunnel.ps1','App-Tunnel.Common.ps1','App-EngineProcess.ps1')){Copy-Item -LiteralPath (Join-Path $PSScriptRoot $name) -Destination (Join-Path $runtime $name) -Force}
    Copy-Item -LiteralPath $engine -Destination (Join-Path $runtime 'sing-box.exe') -Force
    Copy-Item -LiteralPath $carrier -Destination (Join-Path $runtime 'cwg-app-carrier.exe') -Force
    Copy-Item -LiteralPath (Join-Path $bundlePath 'client.json') -Destination (Join-Path $runtime 'client.json') -Force
    Write-CwgJson (Join-Path $runtime 'engine.json') $engineConfig
    & $engine check -c (Join-Path $runtime 'engine.json');if($LASTEXITCODE -ne 0){throw 'Application routing configuration failed validation.'}
    if(Test-Path $stopPath){Remove-Item -LiteralPath $stopPath}
    $s=[ordered]@{Version=1;Status='Starting';Message='Starting application routing.';Mode=[string]$preferences.mode;StartedUTC=[DateTime]::UtcNow.ToString('o');UpdatedUTC=[DateTime]::UtcNow.ToString('o');Restarts=0;Interface=[string]$adapter.Name;InterfaceIndex=[int]$adapter.ifIndex;TunnelInterface=$tunnelInterface;Supervisor=$null;Carrier=$null;Engine=$null;TestDeadlineUTC=if($TestMinutes){[DateTime]::UtcNow.AddMinutes($TestMinutes).ToString('o')}else{''}}
    Write-CwgJson $statePath $s
    $ps=Join-Path $env:WINDIR 'System32\WindowsPowerShell\v1.0\powershell.exe'
    $principal=New-ScheduledTaskPrincipal -UserId SYSTEM -LogonType ServiceAccount -RunLevel Highest
    $watchSettings=New-ScheduledTaskSettingsSet -StartWhenAvailable -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit (New-TimeSpan -Minutes 1)
    $watchTriggers=@((New-ScheduledTaskTrigger -Once -At (Get-Date).AddMinutes(1) -RepetitionInterval (New-TimeSpan -Minutes 1)),(New-ScheduledTaskTrigger -AtStartup))
    Register-ScheduledTask "$task-Watchdog" -Force -Action (New-ScheduledTaskAction -Execute $ps -Argument ('-NoProfile -NonInteractive -ExecutionPolicy Bypass -File "'+$self+'" -Action Watchdog')) -Principal $principal -Settings $watchSettings -Trigger $watchTriggers | Out-Null
    $workerSettings=New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero)
    Register-ScheduledTask "$task-Worker" -Force -Action (New-ScheduledTaskAction -Execute $ps -Argument ('-NoProfile -NonInteractive -ExecutionPolicy Bypass -File "'+$self+'" -Action Worker')) -Principal $principal -Settings $workerSettings | Out-Null
    Start-ScheduledTask "$task-Worker"
    Write-Output 'Application tunnel is starting. All Windows DNS uses the tunnel while active. Restart apps to move existing connections.'
}catch{
    if(Test-Path $statePath){$failed=Get-State;if($failed.Status -eq 'Starting'){Finish-Session 'Failed' ([string]$_.Exception.Message)}}
    throw
}finally{$startMutex.ReleaseMutex();$startMutex.Dispose()}
