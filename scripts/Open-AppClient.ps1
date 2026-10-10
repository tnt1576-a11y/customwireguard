#Requires -RunAsAdministrator
[CmdletBinding()]
param([string]$Bundle,[string]$RenderPreview)
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'App-Tunnel.Common.ps1')
if(-not $Bundle){$Bundle=Join-Path (Split-Path $PSScriptRoot -Parent) 'config'}
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
[Windows.Forms.Application]::EnableVisualStyles()
$script:bundlePath=[IO.Path]::GetFullPath($Bundle)
$script:active=$false;$script:busy=$false
$form=New-Object Windows.Forms.Form
$form.Text='CWG Application Tunnel';$form.ClientSize=New-Object Drawing.Size(800,600);$form.MinimumSize=New-Object Drawing.Size(816,639)
$form.StartPosition='CenterScreen';$form.Font=New-Object Drawing.Font('Segoe UI',10);$form.BackColor=[Drawing.Color]::FromArgb(246,248,251)
$form.AutoScaleMode='Dpi'
function Label([string]$text,[int]$x,[int]$y,[int]$width,[int]$height){$c=New-Object Windows.Forms.Label;$c.Text=$text;$c.SetBounds($x,$y,$width,$height);$form.Controls.Add($c);return $c}
$title=Label 'Choose which apps use CWG' 24 20 740 36;$title.Font=New-Object Drawing.Font('Segoe UI',20,[Drawing.FontStyle]::Bold)
$sub=Label 'Keep your normal connection for the other apps.' 26 62 740 26;$sub.ForeColor=[Drawing.Color]::DimGray
$bundleLabel=Label 'Config folder' 26 101 110 26
$bundleBox=New-Object Windows.Forms.TextBox;$bundleBox.SetBounds(140,98,526,29);$bundleBox.ReadOnly=$true;$bundleBox.Text=$script:bundlePath;$bundleBox.Anchor='Top,Left,Right';$form.Controls.Add($bundleBox)
function Button([string]$text,[int]$x,[int]$y,[int]$width){$b=New-Object Windows.Forms.Button;$b.Text=$text;$b.SetBounds($x,$y,$width,34);$b.FlatStyle='System';$form.Controls.Add($b);return $b}
$browse=Button 'Browse...' 678 96 96;$browse.Anchor='Top,Right'
$mode=New-Object Windows.Forms.ComboBox;$mode.DropDownStyle='DropDownList';$mode.SetBounds(26,147,748,32);$mode.Anchor='Top,Left,Right';$mode.Items.AddRange(@('Only these apps use CWG','These apps bypass CWG','All apps use CWG'));$mode.SelectedIndex=0;$form.Controls.Add($mode)
$list=New-Object Windows.Forms.ListView;$list.SetBounds(26,194,748,217);$list.Anchor='Top,Bottom,Left,Right';$list.View='Details';$list.FullRowSelect=$true;$list.MultiSelect=$true;$list.HideSelection=$false;$list.BorderStyle='FixedSingle';$null=$list.Columns.Add('Type',85);$null=$list.Columns.Add('Application or folder',635);$form.Controls.Add($list)
$add=Button 'Add program...' 26 421 142;$add.Anchor='Bottom,Left'
$folder=Button 'Add folder...' 178 421 132;$folder.Anchor='Bottom,Left'
$remove=Button 'Remove' 320 421 108;$remove.Anchor='Bottom,Left'
$install=Button 'Install app engine' 610 421 164;$install.Anchor='Bottom,Right'
$hint=Label 'Folder rules also include helper programs inside that folder.' 26 462 748 24;$hint.Anchor='Bottom,Left,Right';$hint.ForeColor=[Drawing.Color]::DimGray
$status=Label 'Stopped' 26 496 575 25;$status.Anchor='Bottom,Left,Right';$status.Font=New-Object Drawing.Font('Segoe UI',11,[Drawing.FontStyle]::Bold)
$connect=Button 'Connect' 608 492 166;$connect.Anchor='Bottom,Right'
$notice=Label "All Windows DNS uses CWG while connected. Tunneled apps use IPv4; their public IPv6 is blocked.`nRestart apps to move existing connections. Stopping restores normal networking; there is no kill switch after stop." 26 536 748 52;$notice.Anchor='Bottom,Left,Right';$notice.Font=New-Object Drawing.Font('Segoe UI',9);$notice.ForeColor=[Drawing.Color]::DimGray
$tray=New-Object Windows.Forms.NotifyIcon;$tray.Icon=[Drawing.SystemIcons]::Application;$tray.Text='CWG Application Tunnel';$tray.Visible=$false
$menu=New-Object Windows.Forms.ContextMenuStrip;$showItem=$menu.Items.Add('Open CWG');$stopItem=$menu.Items.Add('Disconnect');$quitItem=$menu.Items.Add('Disconnect and quit');$tray.ContextMenuStrip=$menu
function Show-Error($message){[Windows.Forms.MessageBox]::Show($form,[string]$message,'CWG','OK','Error') | Out-Null}
function Add-Entry([string]$kind,[string]$path){$item=New-Object Windows.Forms.ListViewItem($(if($kind -eq 'folder'){'Folder'}else{'Program'}));$null=$item.SubItems.Add($path);$item.Tag=[pscustomobject]@{kind=$kind;path=$path};$null=$list.Items.Add($item)}
function Load-Settings {
    $list.Items.Clear();$path=Join-Path $script:bundlePath 'apps.json'
    if(Test-Path $path){$s=Read-CwgJson $path;$null=Get-CwgAppPatterns $s;$mode.SelectedIndex=[Array]::IndexOf(@('Include','Exclude','All'),[string]$s.mode);if($mode.SelectedIndex -lt 0){throw 'Invalid app mode.'};foreach($entry in $s.applications){Add-Entry $entry.kind $entry.path}}
}
function Save-Settings {
    $entries=@(foreach($item in $list.Items){$item.Tag})
    $s=[pscustomobject]@{mode=@('Include','Exclude','All')[$mode.SelectedIndex];applications=$entries}
    $null=Get-CwgAppPatterns $s
    Write-CwgJson (Join-Path $script:bundlePath 'apps.json') $s
}
function Update-Status {
    if($script:busy -or $RenderPreview){return}
    $path=Join-Path $env:ProgramData 'CWG-AppTunnel\state.json'
    try{
        if(Test-Path $path){$s=Read-CwgJson $path;$state=[string]$s.Status;$message=[string]$s.Message}else{$state='Stopped';$message=''}
        $script:active=$state -in @('Starting','Running','Reconnecting')
        $status.Text=if($state -eq 'Running'){'Connected - application rules active'}elseif($state -eq 'Failed'){'Stopped - '+$message}else{$state}
        $status.ForeColor=if($state -eq 'Running'){[Drawing.Color]::FromArgb(20,110,70)}elseif($state -eq 'Failed'){[Drawing.Color]::Firebrick}else{[Drawing.Color]::FromArgb(45,55,70)}
        $connect.Text=if($script:active){'Disconnect'}else{'Connect'}
        foreach($c in @($browse,$mode,$add,$folder,$remove,$list,$install)){$c.Enabled=-not $script:active}
        $list.Enabled=$list.Enabled -and $mode.SelectedIndex -ne 2
        $tray.Text='CWG - '+$state
    }catch{$status.Text='Could not read CWG status';$status.ForeColor=[Drawing.Color]::Firebrick}
}
function Disconnect-App {
    $script:busy=$true
    try{& (Join-Path $PSScriptRoot 'App-Tunnel.ps1') -Action Stop | Out-Null}catch{Show-Error $_.Exception.Message}finally{$script:busy=$false;Update-Status}
}
$browse.Add_Click({$d=New-Object Windows.Forms.FolderBrowserDialog;$d.Description='Select the private config folder containing client.json and cwg-full.conf.';$d.SelectedPath=$script:bundlePath;if($d.ShowDialog($form) -eq 'OK'){try{$script:bundlePath=$d.SelectedPath;$bundleBox.Text=$script:bundlePath;Load-Settings}catch{Show-Error $_.Exception.Message}};$d.Dispose()})
$add.Add_Click({$d=New-Object Windows.Forms.OpenFileDialog;$d.Filter='Programs (*.exe)|*.exe';$d.Multiselect=$true;if($d.ShowDialog($form) -eq 'OK'){foreach($path in $d.FileNames){Add-Entry 'exe' $path}};$d.Dispose()})
$folder.Add_Click({$d=New-Object Windows.Forms.FolderBrowserDialog;$d.Description='Select one application folder, including its helper executables.';if($d.ShowDialog($form) -eq 'OK'){Add-Entry 'folder' $d.SelectedPath};$d.Dispose()})
$remove.Add_Click({foreach($item in @($list.SelectedItems)){$list.Items.Remove($item)}})
$install.Add_Click({
    $script:busy=$true;$install.Enabled=$false;$status.Text='Downloading and verifying the app engine...';$form.Refresh()
    try{& (Join-Path $PSScriptRoot 'Install-AppEngine.ps1') | Out-Null;$status.Text='Application engine installed.'}catch{Show-Error $_.Exception.Message}finally{$script:busy=$false;$install.Enabled=$true}
})
$mode.Add_SelectedIndexChanged({$list.Enabled=$mode.SelectedIndex -ne 2})
$connect.Add_Click({
    if($script:active){Disconnect-App;return}
    $script:busy=$true;$connect.Enabled=$false;$status.Text='Starting...';$form.Refresh()
    try{
        Save-Settings
        & (Join-Path $PSScriptRoot 'App-Tunnel.ps1') -Action Start -Bundle $script:bundlePath | Out-Null
    }catch{Show-Error $_.Exception.Message}finally{$script:busy=$false;$connect.Enabled=$true;Update-Status}
})
$showAction={$form.Show();$form.WindowState='Normal';$form.Activate();$tray.Visible=$false}
$tray.Add_DoubleClick($showAction);$showItem.Add_Click($showAction);$stopItem.Add_Click({Disconnect-App})
$quitItem.Add_Click({Disconnect-App;if(-not $script:active){$form.Close()}})
$form.Add_FormClosing({param($sender,$event) if($script:active){$event.Cancel=$true;$tray.Visible=$true;$form.Hide();$tray.ShowBalloonTip(2500,'CWG is still running','Use the tray icon to open the controls or disconnect.','Info')}})
$form.Add_Resize({if($form.WindowState -eq 'Minimized'){$tray.Visible=$true;$form.Hide()}})
$timer=New-Object Windows.Forms.Timer;$timer.Interval=1000;$timer.Add_Tick({Update-Status})
try{
    if($RenderPreview){
        Add-Entry 'exe' 'C:\Program Files\Mozilla Firefox\firefox.exe';Add-Entry 'folder' 'C:\Games\Example Game'
        $form.ShowInTaskbar=$false;$form.StartPosition='Manual';$form.Location=New-Object Drawing.Point(-32000,-32000)
        $form.CreateControl();$form.Show();$form.Refresh();[Windows.Forms.Application]::DoEvents()
        $bitmap=New-Object Drawing.Bitmap($form.Width,$form.Height);$form.DrawToBitmap($bitmap,(New-Object Drawing.Rectangle(0,0,$form.Width,$form.Height)));$bitmap.Save([IO.Path]::GetFullPath($RenderPreview));$bitmap.Dispose()
    }else{
        try{Load-Settings}catch{Show-Error $_.Exception.Message}
        Update-Status;$timer.Start();[Windows.Forms.Application]::Run($form)
    }
}finally{$timer.Stop();$timer.Dispose();$tray.Visible=$false;$tray.Dispose();$form.Dispose()}
