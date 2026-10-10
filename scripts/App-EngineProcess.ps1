#Requires -RunAsAdministrator
# Give the routing engine its own hidden console, allowing a real SIGINT cleanup.
param([ValidateSet('Start','Signal','Carrier')][string]$Action,[string]$Runtime,[int]$TargetId,[string]$ExpectedStart,[int]$InterfaceIndex)
$ErrorActionPreference='Stop'
if(-not ('CwgEngineProcess' -as [type])){
Add-Type -TypeDefinition @'
using System;
using System.IO;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Text;
using Microsoft.Win32.SafeHandles;
public static class CwgEngineProcess {
 static readonly object logLock=new object();
 static void Log(string path,string line){if(line==null)return;try{lock(logLock){if(File.Exists(path)&&new FileInfo(path).Length>8*1024*1024){if(File.Exists(path+".previous"))File.Delete(path+".previous");File.Move(path,path+".previous");}File.AppendAllText(path,line+Environment.NewLine);}}catch(IOException){}}
 public static int Carrier(string root,int index){
  var p=new System.Diagnostics.Process();p.StartInfo=new System.Diagnostics.ProcessStartInfo(Path.Combine(root,"cwg-app-carrier.exe"),"client -config \""+Path.Combine(root,"client.json")+"\" -interface-index "+index);
  p.StartInfo.WorkingDirectory=root;p.StartInfo.UseShellExecute=false;p.StartInfo.CreateNoWindow=true;p.StartInfo.RedirectStandardOutput=true;p.StartInfo.RedirectStandardError=true;
  p.OutputDataReceived+=(s,e)=>Log(Path.Combine(root,"carrier.out"),e.Data);p.ErrorDataReceived+=(s,e)=>Log(Path.Combine(root,"carrier.err"),e.Data);
  p.Start();p.BeginOutputReadLine();p.BeginErrorReadLine();return p.Id;
 }
 [StructLayout(LayoutKind.Sequential,CharSet=CharSet.Unicode)] struct STARTUPINFO {
  public int cb; public string reserved,desktop,title; public uint x,y,xSize,ySize,xCountChars,yCountChars,fill,flags;
  public ushort show,cbReserved2; public IntPtr reserved2,input,output,error;
 }
 [StructLayout(LayoutKind.Sequential)] struct PROCESSINFO {public IntPtr process,thread; public int id,threadId;}
 [DllImport("kernel32.dll",CharSet=CharSet.Unicode,SetLastError=true)] static extern bool CreateProcessW(string app,StringBuilder command,IntPtr processAttributes,IntPtr threadAttributes,bool inherit,uint flags,IntPtr environment,string directory,ref STARTUPINFO startup,out PROCESSINFO process);
 [DllImport("kernel32.dll",SetLastError=true)] static extern bool SetHandleInformation(IntPtr handle,uint mask,uint flags);
 [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr handle);
 [DllImport("kernel32.dll",CharSet=CharSet.Unicode,SetLastError=true)] static extern SafeFileHandle CreateFileW(string name,uint access,uint share,IntPtr security,uint creation,uint attributes,IntPtr template);
 [DllImport("kernel32.dll",SetLastError=true)] static extern bool FreeConsole();
 [DllImport("kernel32.dll",SetLastError=true)] static extern bool AttachConsole(uint pid);
 [DllImport("kernel32.dll",SetLastError=true)] static extern bool SetConsoleCtrlHandler(IntPtr handler,bool add);
 [DllImport("kernel32.dll",SetLastError=true)] static extern bool GenerateConsoleCtrlEvent(uint control,uint group);
 public static int Start(string root) {
  string exe=Path.Combine(root,"sing-box.exe"), config=Path.Combine(root,"engine.json");
  using(var input=CreateFileW("NUL",0x80000000,3,IntPtr.Zero,3,0,IntPtr.Zero))
  using(var output=new FileStream(Path.Combine(root,"engine.out"),FileMode.Create,FileAccess.Write,FileShare.ReadWrite))
  using(var error=new FileStream(Path.Combine(root,"engine.err"),FileMode.Create,FileAccess.Write,FileShare.ReadWrite)) {
   var si=new STARTUPINFO();si.cb=Marshal.SizeOf(si);si.flags=0x101;si.show=0;
   if(input.IsInvalid)throw new Win32Exception();
   si.input=input.DangerousGetHandle();si.output=output.SafeFileHandle.DangerousGetHandle();si.error=error.SafeFileHandle.DangerousGetHandle();
   foreach(var handle in new[]{si.input,si.output,si.error})if(!SetHandleInformation(handle,1,1))throw new Win32Exception();
   PROCESSINFO pi;
   if(!CreateProcessW(exe,new StringBuilder("\""+exe+"\" run --disable-color -c \""+config+"\""),IntPtr.Zero,IntPtr.Zero,true,0x10,IntPtr.Zero,root,ref si,out pi))throw new Win32Exception();
   CloseHandle(pi.thread);CloseHandle(pi.process);return pi.id;
  }
 }
 public static void Signal(uint pid) {
  FreeConsole();if(!AttachConsole(pid))throw new Win32Exception();
  try{if(!SetConsoleCtrlHandler(IntPtr.Zero,true))throw new Win32Exception();if(!GenerateConsoleCtrlEvent(0,0))throw new Win32Exception();System.Threading.Thread.Sleep(100);}
  finally{FreeConsole();}
 }
}
'@
}
if($Action -in @('Start','Carrier')){
    if($Action -eq 'Start'){$id=[CwgEngineProcess]::Start($Runtime);$exe='sing-box.exe'}else{$id=[CwgEngineProcess]::Carrier($Runtime,$InterfaceIndex);$exe='cwg-app-carrier.exe'}
    $p=Get-Process -Id $id
    [pscustomobject]@{Id=$id;Path=(Join-Path $Runtime $exe);Start=$p.StartTime.ToUniversalTime().ToString('o')}
}else{
    $p=Get-Process -Id $TargetId -ErrorAction SilentlyContinue
    if(-not $p){return}
    if($p.Path -ne (Join-Path $Runtime 'sing-box.exe') -or $p.StartTime.ToUniversalTime().ToString('o') -ne $ExpectedStart){throw 'Engine identity changed; signal refused.'}
    [CwgEngineProcess]::Signal([uint32]$TargetId)
}
