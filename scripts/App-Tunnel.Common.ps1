# Configuration helpers; importing this file never changes networking.
Set-StrictMode -Version 2
$script:CwgEngineVersion = '1.14.3'
$script:CwgEngineArchiveSHA = 'd8b86ef58936dba9230ec6a57ffa4243933f3c14ef4809828b6c553440ed1518'
$script:CwgEngineSHA = '5c19bb3f8c12d4e85b3d47fe63123a7624b314b58dd7a8e32f51004f1c4d2faf'

function Read-CwgJson([string]$Path) { [IO.File]::ReadAllText($Path) | ConvertFrom-Json }
function Write-CwgJson([string]$Path,$Value) {
    $temporary=$Path+'.new'
    [IO.File]::WriteAllText($temporary,($Value | ConvertTo-Json -Depth 20),(New-Object Text.UTF8Encoding($false)))
    if([IO.File]::Exists($Path)){[IO.File]::Replace($temporary,$Path,[NullString]::Value)}else{[IO.File]::Move($temporary,$Path)}
}
function Get-CwgAppPatterns($Settings) {
    $patterns=@()
    foreach($entry in @($Settings.applications)){
        if(-not $entry -or $entry.kind -notin @('exe','folder')){throw 'Each application must be an exe or folder entry.'}
        $path=[string]$entry.path
        if(-not [IO.Path]::IsPathRooted($path) -or $path.StartsWith('\\') -or $path -match '["\r\n]' -or $path.Length -gt 240){throw 'Use a local absolute application path.'}
        $path=[IO.Path]::GetFullPath($path).TrimEnd('\')
        if($entry.kind -eq 'exe'){
            if([IO.Path]::GetExtension($path) -ne '.exe'){throw 'Application entries must end in .exe.'}
            $patterns+='(?i)^'+[regex]::Escape($path)+'$'
        }else{
            if($path.Length -lt 4 -or $path -eq $env:WINDIR -or $path -eq $env:ProgramFiles){throw 'Choose an application folder, not a drive or shared Windows/Program Files folder.'}
            $patterns+='(?i)^'+[regex]::Escape($path+'\')
        }
    }
    if($patterns.Count -gt 128){throw 'At most 128 application entries are supported.'}
    return ,$patterns
}
function New-CwgAppConfig([string]$NativeText,$Transport,$Settings,[string]$PhysicalInterface,[string[]]$BypassPrefixes) {
    if($Settings.mode -notin @('Include','Exclude','All')){throw 'Application mode must be Include, Exclude, or All.'}
    $patterns=Get-CwgAppPatterns $Settings
    if($Settings.mode -eq 'Include' -and $patterns.Count -eq 0){throw 'Add at least one application to tunnel.'}
    if(([regex]::Matches($NativeText,'(?mi)^\s*\[Peer\]\s*$')).Count -ne 1){throw 'Exactly one WireGuard peer is required.'}
    $fields=@{}
    foreach($key in @('PrivateKey','PublicKey','PresharedKey','Address','MTU','ListenPort','Endpoint')){
        $matches=[regex]::Matches($NativeText,'(?mi)^\s*'+$key+'\s*=\s*([^#\r\n]+)')
        if($matches.Count -ne 1){throw "Expected one $key in the native profile."}
        $fields[$key]=$matches[0].Groups[1].Value.Trim()
    }
    foreach($key in @('PrivateKey','PublicKey','PresharedKey')){if([Convert]::FromBase64String($fields[$key]).Length -ne 32){throw "Invalid $key."}}
    if($Transport.listen -notmatch '^127\.0\.0\.1:(\d+)$'){throw 'App mode requires an IPv4 loopback carrier listener.'}
    $carrierPort=[int]$Matches[1]
    if($Transport.wireguard -notmatch '^127\.0\.0\.1:(\d+)$'){throw 'App mode requires an IPv4 loopback WireGuard target.'}
    $nativePort=[int]$Matches[1]
    if($fields.Endpoint -ne $Transport.listen -or [int]$fields.ListenPort -ne $nativePort -or $carrierPort -eq $nativePort){throw 'Native profile and carrier loopback ports differ.'}
    $mtu=[int]$fields.MTU;if($mtu -lt 576 -or $mtu -gt 1280){throw 'Native MTU must be 576..1280.'}
    $address=@($fields.Address.Split(',') | ForEach-Object {$_.Trim()} | Where-Object {$_ -notmatch ':'})
    if($address.Count -ne 1 -or $address[0] -notmatch '^\d+\.\d+\.\d+\.\d+/(\d+)$'){throw 'One native IPv4 address is required.'}
    $null=[Net.IPAddress]::Parse($address[0].Split('/')[0])
    $rules=@(@{port=53;action='hijack-dns'})
    if($Settings.mode -eq 'Include'){
        $rules+=@{process_path_regex=$patterns;ip_version=6;action='reject'}
        $rules+=@{process_path_regex=$patterns;action='route';outbound='cwg'}
        $final='direct'
    }else{
        if($Settings.mode -eq 'Exclude' -and $patterns.Count){$rules+=@{process_path_regex=$patterns;action='route';outbound='direct'}}
        $rules+=@{ip_version=6;action='reject'}
        $final='cwg'
    }
    return [ordered]@{
        log=@{level='warn';timestamp=$true;disabled=$false}
        dns=@{servers=@(@{type='udp';tag='tunnel-dns';server='1.1.1.1';detour='cwg'});final='tunnel-dns';strategy='ipv4_only'}
        inbounds=@(@{type='tun';tag='applications';interface_name='CWG Applications';address=@('172.30.77.1/30','fd77:77:77::1/126');mtu=1280;auto_route=$true;strict_route=$true;route_exclude_address=$BypassPrefixes;stack='mixed'})
        endpoints=@(@{type='wireguard';tag='cwg';system=$false;mtu=$mtu;address=$address;private_key=$fields.PrivateKey;listen_port=$nativePort;peers=@(@{address='127.0.0.1';port=$carrierPort;public_key=$fields.PublicKey;pre_shared_key=$fields.PresharedKey;allowed_ips=@('0.0.0.0/0');persistent_keepalive_interval=25})})
        outbounds=@(@{type='direct';tag='direct';bind_interface=$PhysicalInterface})
        route=@{default_domain_resolver='tunnel-dns';rules=$rules;final=$final}
    }
}
function Get-CwgIPv4Prefix([string]$Address,[int]$Length){
    $bytes=[Net.IPAddress]::Parse($Address).GetAddressBytes()
    if($bytes.Length -ne 4 -or $Length -lt 0 -or $Length -gt 32){throw 'Invalid IPv4 prefix.'}
    for($i=0;$i -lt 4;$i++){$bits=[Math]::Min(8,[Math]::Max(0,$Length-8*$i));$bytes[$i]=$bytes[$i] -band (256-[Math]::Pow(2,8-$bits))}
    return ([Net.IPAddress]::new($bytes).ToString()+'/'+$Length)
}
