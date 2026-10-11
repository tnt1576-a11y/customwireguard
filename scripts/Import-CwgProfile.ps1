# Data-only profile import. Never extracts or executes files supplied by a profile.
function Import-CwgProfile([string]$Archive,[string]$ApplicationRoot) {
    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archivePath=(Resolve-Path -LiteralPath $Archive).Path
    if((Get-Item -LiteralPath $archivePath).Length -gt 32768){throw 'This is not a small CWG configuration file. Import the .cwg file from your administrator.'}
    $root=[IO.Path]::GetFullPath($ApplicationRoot)
    $profiles=Join-Path $root 'profiles'
    if(Test-Path -LiteralPath $profiles){if((Get-Item -LiteralPath $profiles).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'The profiles folder cannot be a link.'}}
    else{New-Item -ItemType Directory -Path $profiles | Out-Null}
    $destination=Join-Path $profiles ([guid]::NewGuid().ToString('N'))
    $zip=[IO.Compression.ZipFile]::OpenRead($archivePath)
    $values=@{}
    try{
        $expected=@('profile.json','client.json','cwg-full.conf','cwg-test.conf')
        if($zip.Entries.Count -ne 4){throw 'The configuration must contain exactly four profile files.'}
        foreach($entry in $zip.Entries){
            if($entry.FullName -cnotin $expected -or $values.ContainsKey($entry.FullName) -or $entry.Length -gt 16384){throw 'The configuration contains an unexpected, duplicate or oversized file.'}
            $stream=$entry.Open();$reader=New-Object IO.StreamReader($stream,(New-Object Text.UTF8Encoding($false,$true)))
            try{
                $buffer=New-Object char[] 16385
                $count=$reader.ReadBlock($buffer,0,$buffer.Length)
                if($count -gt 16384 -or $reader.Peek() -ge 0){throw 'A configuration file is too large.'}
                $values[$entry.FullName]=New-Object string($buffer,0,$count)
            }finally{$reader.Dispose();$stream.Dispose()}
        }
    }finally{$zip.Dispose()}
    $metadata=$values['profile.json'] | ConvertFrom-Json
    if($metadata.schema -ne 1 -or $metadata.format -ne 'cwg-profile' -or $metadata.name -cnotmatch '^[a-z][a-z0-9_-]{0,31}$'){throw 'Unsupported CWG configuration format.'}
    $transport=$values['client.json'] | ConvertFrom-Json
    if($transport.mode -ne 'client' -or $transport.transport -ne 'exchange' -or $transport.exchange_shared -ne $true){throw 'Import a shared-mode CWG 0.6 configuration.'}
    foreach($filename in @('cwg-full.conf','cwg-test.conf')){
        foreach($line in $values[$filename].Split("`n")){
            $line=$line.Trim()
            if(-not $line -or $line.StartsWith('#') -or $line -in @('[Interface]','[Peer]')){continue}
            if($line -notmatch '^(PrivateKey|PublicKey|PresharedKey|Address|MTU|ListenPort|Endpoint|AllowedIPs|DNS|PersistentKeepalive)\s*='){throw 'The configuration contains unsupported directives.'}
        }
    }
    $settings=[pscustomobject]@{mode='All';applications=@()}
    $null=New-CwgAppConfig $values['cwg-full.conf'] $transport $settings 'Ethernet' @()
    New-Item -ItemType Directory -Path $destination | Out-Null
    $acl=New-Object Security.AccessControl.DirectorySecurity;$acl.SetAccessRuleProtection($true,$false)
    foreach($identity in @([Security.Principal.WindowsIdentity]::GetCurrent().User,(New-Object Security.Principal.SecurityIdentifier('S-1-5-18')),(New-Object Security.Principal.SecurityIdentifier('S-1-5-32-544')))){
        $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($identity,'FullControl','ContainerInherit,ObjectInherit','None','Allow')))
    }
    Set-Acl -LiteralPath $destination -AclObject $acl
    try{
        foreach($name in $values.Keys){[IO.File]::WriteAllText((Join-Path $destination $name),$values[$name],(New-Object Text.UTF8Encoding($false)))}
        $carrier=Join-Path $root 'bin\cwg-windows-amd64.exe'
        & $carrier check -config (Join-Path $destination 'client.json') *> $null
        if($LASTEXITCODE -ne 0){throw 'The transport rejected this configuration.'}
        Write-CwgJson (Join-Path $destination 'apps.json') $settings
        return [pscustomobject]@{Bundle=$destination;Name=$metadata.name;Endpoint=$transport.endpoint}
    }catch{
        # Remove only files owned by this import inside its newly created folder.
        foreach($name in @($values.Keys)+@('apps.json','apps.json.new')){$p=Join-Path $destination $name;if(Test-Path -LiteralPath $p){Remove-Item -LiteralPath $p}}
        [IO.Directory]::Delete($destination,$false)
        throw
    }
}
