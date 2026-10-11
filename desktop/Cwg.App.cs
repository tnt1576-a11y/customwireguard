using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Reflection;
using System.Security.Cryptography;
using System.Security.Principal;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using System.Web.Script.Serialization;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Interop;
using System.Windows.Markup;
using System.Windows.Media;
using System.Windows.Media.Imaging;
using System.Windows.Threading;
using Forms = System.Windows.Forms;

[assembly: AssemblyTitle("CWG Desktop")]
[assembly: AssemblyProduct("CWG")]
[assembly: AssemblyVersion("0.6.3.0")]
[assembly: AssemblyFileVersion("0.6.3.0")]

namespace Cwg.Desktop {
 internal sealed class AppEntry {
  public string kind { get; set; }
  public string path { get; set; }
 }
 internal sealed class Client {
  readonly string root = AppDomain.CurrentDomain.BaseDirectory.TrimEnd(Path.DirectorySeparatorChar);
  readonly JavaScriptSerializer json = new JavaScriptSerializer { MaxJsonLength = 65536 };
  Window window;
  Forms.NotifyIcon tray;
  DispatcherTimer timer;
  readonly List<AppEntry> entries = new List<AppEntry>();
  string bundle, account = "No profile yet", endpoint = "", state = "Stopped", lastMessage = "";
  bool busy, quitting, loading, preview;
  DateTime? started;
  T Get<T>(string name) where T : class { return window.FindName(name) as T; }
  TextBlock Text(string name) { return Get<TextBlock>(name); }
  Button Button(string name) { return Get<Button>(name); }
  RadioButton Radio(string name) { return Get<RadioButton>(name); }
  bool Active { get { return state == "Starting" || state == "Running" || state == "Reconnecting"; } }
  string Mode { get { return Radio("FullMode").IsChecked == true ? "All" : Radio("ExcludeMode").IsChecked == true ? "Exclude" : "Include"; } }
  string RuntimeState { get { return Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData), "CWG-AppTunnel", "state.json"); } }

  [STAThread]
  public static int Main(string[] args) {
   try {
    bool isPreview = args.Length == 2 && args[0] == "--preview";
    if (!isPreview && args.Length != 0) throw new InvalidOperationException("Start CWG without command-line arguments.");
    if (!isPreview && !new WindowsPrincipal(WindowsIdentity.GetCurrent()).IsInRole(WindowsBuiltInRole.Administrator)) {
     try { Process.Start(new ProcessStartInfo(Assembly.GetExecutingAssembly().Location) { UseShellExecute = true, Verb = "runas", WorkingDirectory = AppDomain.CurrentDomain.BaseDirectory }); }
     catch (System.ComponentModel.Win32Exception) { MessageBox.Show("CWG needs administrator permission to manage its tunnel. Open the app again when you are ready.", "CWG"); }
     return 0;
    }
    bool created;
    using (var mutex = new Mutex(true, isPreview ? "Local\\CWG-Desktop-Preview" : "Global\\CWG-Desktop", out created)) {
     if (!created) { MessageBox.Show("CWG is already open. Use its icon in the notification area.", "CWG"); return 0; }
     var app = new Application { ShutdownMode = ShutdownMode.OnMainWindowClose };
     var client = new Client(); client.Load(isPreview);
     if (isPreview) { client.Render(args[1]); return 0; }
     app.Run(client.window); return 0;
    }
   } catch (Exception ex) { MessageBox.Show(ex.Message, "CWG could not start", MessageBoxButton.OK, MessageBoxImage.Error); return 1; }
  }

  void Load(bool render) {
   preview = render;
   using (Stream stream = Assembly.GetExecutingAssembly().GetManifestResourceStream("Cwg.MainWindow.xaml")) window = (Window)XamlReader.Load(stream);
   window.SourceInitialized += delegate { try { int dark = 1; DwmSetWindowAttribute(new WindowInteropHelper(window).Handle, 20, ref dark, 4); } catch { } };
   if (!preview) {
    string defaultPath = Path.Combine(root, "config");
    string selection = Path.Combine(root, "selected-profile.json");
    try {
     if (File.Exists(selection)) {
      var value = Read(selection);
      string candidate = Convert.ToString(value["Bundle"]);
      if (Path.GetFullPath(candidate).StartsWith(root + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase) && Directory.Exists(candidate)) defaultPath = candidate;
     }
     if (File.Exists(Path.Combine(defaultPath, "client.json"))) LoadProfile(defaultPath);
    } catch (Exception ex) { lastMessage = "Profile could not be loaded: " + ex.Message; }
   }
   Button("ImportButton").Click += async delegate { await Import(); };
   Button("ConnectButton").Click += async delegate { if (Active) await Disconnect(); else await Connect(); };
   Button("AddApp").Click += delegate { AddPrograms(); };
   Button("AddFolder").Click += delegate { AddFolder(); };
   Button("RemoveApp").Click += delegate {
    foreach (ListBoxItem row in Get<ListBox>("AppList").SelectedItems.Cast<ListBoxItem>().ToArray()) entries.Remove((AppEntry)row.Tag);
    RenderEntries(); SavePreferences();
   };
   foreach (string name in new[] { "FullMode", "SplitMode", "IncludeMode", "ExcludeMode" }) Radio(name).Checked += delegate { RenderMode(); SavePreferences(); };
   window.Closing += delegate(object sender, System.ComponentModel.CancelEventArgs e) {
    if (busy) { e.Cancel = true; return; }
    if (Active && !quitting) { e.Cancel = true; window.Hide(); tray.Visible = true; tray.ShowBalloonTip(2000, "CWG is still connected", "Use this icon to open CWG or disconnect.", Forms.ToolTipIcon.Info); }
   };
   window.Closed += delegate { if (timer != null) timer.Stop(); if (tray != null) { tray.Visible = false; tray.Dispose(); } };
   window.StateChanged += delegate { if (!preview && window.WindowState == WindowState.Minimized) { tray.Visible = true; window.Hide(); } };
   if (!preview) {
    tray = new Forms.NotifyIcon { Text = "CWG", Icon = System.Drawing.SystemIcons.Shield, Visible = false };
    tray.DoubleClick += delegate { ShowWindow(); };
    var menu = new Forms.ContextMenuStrip();
    menu.Items.Add("Open CWG", null, delegate { ShowWindow(); });
    menu.Items.Add("Disconnect", null, async delegate { await Disconnect(); });
    menu.Items.Add("Disconnect and quit", null, async delegate { await Disconnect(); if (!Active && !busy) { quitting = true; window.Close(); } });
    tray.ContextMenuStrip = menu;
    timer = new DispatcherTimer { Interval = TimeSpan.FromSeconds(1) };
    timer.Tick += delegate { PollState(); }; timer.Start(); PollState();
   }
   RenderEntries(); RenderMode(); Refresh();
  }

  [System.Runtime.InteropServices.DllImport("dwmapi.dll")]
  static extern int DwmSetWindowAttribute(IntPtr handle, int attribute, ref int value, int length);
  void ShowWindow() { window.Show(); window.WindowState = WindowState.Normal; window.Activate(); tray.Visible = false; }
  Dictionary<string, object> Read(string file) {
   var info = new FileInfo(file); if (info.Length > 65536) throw new InvalidOperationException("This settings file is too large.");
   return json.Deserialize<Dictionary<string, object>>(File.ReadAllText(file));
  }
  void Write(string file, object value) {
   string stage = file + "." + Guid.NewGuid().ToString("N") + ".tmp";
   try { File.WriteAllText(stage, json.Serialize(value), new UTF8Encoding(false)); if (File.Exists(file)) File.Replace(stage, file, null); else File.Move(stage, file); }
   finally { if (File.Exists(stage)) File.Delete(stage); }
  }
  void LoadProfile(string path) {
   var cfg = Read(Path.Combine(path, "client.json"));
   endpoint = Convert.ToString(cfg["endpoint"]);
   if (endpoint.Length > 100) throw new InvalidOperationException("Invalid server address.");
   bundle = Path.GetFullPath(path); account = "Owner";
   string metadata = Path.Combine(bundle, "profile.json");
   if (File.Exists(metadata)) account = Convert.ToString(Read(metadata)["name"]);
   entries.Clear(); loading = true;
   try {
    string settings = Path.Combine(bundle, "apps.json");
    string mode = "All";
    if (File.Exists(settings)) {
     var preferences = Read(settings); mode = Convert.ToString(preferences["mode"]);
     var apps = json.ConvertToType<AppEntry[]>(preferences["applications"]);
     if (apps != null && apps.Length <= 128) entries.AddRange(apps);
    }
    Radio("FullMode").IsChecked = mode == "All";
    Radio("SplitMode").IsChecked = mode != "All";
    Radio("IncludeMode").IsChecked = mode != "Exclude";
    Radio("ExcludeMode").IsChecked = mode == "Exclude";
   } finally { loading = false; }
   lastMessage = "Ready to connect."; RenderEntries(); RenderMode(); Refresh();
  }
  void SavePreferences() {
   if (preview || loading || bundle == null || Active || busy) return;
   try { Write(Path.Combine(bundle, "apps.json"), new { mode = Mode, applications = entries.ToArray() }); }
   catch (Exception ex) { lastMessage = "Could not save app choices: " + ex.Message; Refresh(); }
  }
  void RenderMode() {
   bool split = Radio("SplitMode").IsChecked == true;
   Get<Grid>("SplitPanel").Visibility = split ? Visibility.Visible : Visibility.Collapsed;
   Get<System.Windows.Controls.Border>("FullPanel").Visibility = split ? Visibility.Collapsed : Visibility.Visible;
  }
  void RenderEntries() {
   var list = Get<ListBox>("AppList"); list.Items.Clear();
   foreach (var entry in entries) {
    var panel = new StackPanel();
    panel.Children.Add(new TextBlock { Text = Path.GetFileName(entry.path.TrimEnd('\\')) + (entry.kind == "folder" ? "  ·  folder and helpers" : ""), FontWeight = FontWeights.SemiBold });
    panel.Children.Add(new TextBlock { Text = entry.path, FontSize = 11, Foreground = new SolidColorBrush(Color.FromRgb(144, 164, 193)), Margin = new Thickness(0, 4, 0, 0), TextTrimming = TextTrimming.CharacterEllipsis });
    list.Items.Add(new ListBoxItem { Content = panel, Tag = entry });
   }
   Text("EmptyHint").Visibility = entries.Count == 0 ? Visibility.Visible : Visibility.Collapsed;
  }
  void Add(string kind, string path) {
   if (entries.Count >= 128) throw new InvalidOperationException("A profile supports up to 128 app rules.");
   if (entries.Any(e => e.kind == kind && String.Equals(e.path, path, StringComparison.OrdinalIgnoreCase))) return;
   entries.Add(new AppEntry { kind = kind, path = path }); RenderEntries(); SavePreferences();
  }
  void AddPrograms() {
   try { var dialog = new Microsoft.Win32.OpenFileDialog { Title = "Choose applications", Filter = "Windows applications (*.exe)|*.exe", Multiselect = true }; if (dialog.ShowDialog(window) == true) foreach (string name in dialog.FileNames) Add("exe", name); }
   catch (Exception ex) { Error(ex.Message); }
  }
  void AddFolder() {
   try { using (var dialog = new Forms.FolderBrowserDialog { Description = "Choose an app's installation folder. Helpers inside this folder are included." }) if (dialog.ShowDialog() == Forms.DialogResult.OK) Add("folder", dialog.SelectedPath); }
   catch (Exception ex) { Error(ex.Message); }
  }
  void PollState() {
   if (busy || preview) return;
   try {
    if (File.Exists(RuntimeState)) {
     var value = Read(RuntimeState); state = Convert.ToString(value["Status"]);
     if (value.ContainsKey("StartedUTC")) { DateTime parsed; if (DateTime.TryParse(Convert.ToString(value["StartedUTC"]), out parsed)) started = parsed.ToUniversalTime(); }
     if (state == "Failed") lastMessage = Convert.ToString(value["Message"]);
    } else state = "Stopped";
    Refresh();
   } catch (IOException) { } catch (Exception) { lastMessage = "Could not read tunnel status. Reopen CWG before starting another connection."; Refresh(); }
  }
  void Refresh() {
   if (window == null) return;
   Text("ProfileName").Text = account;
   Text("Endpoint").Text = String.IsNullOrEmpty(endpoint) ? "Import the .cwg file from your administrator." : "Server  " + endpoint;
   Text("StatusBadge").Text = busy ? "Working…" : state == "Running" ? "Connected" : state == "Reconnecting" ? "Reconnecting" : state == "Starting" ? "Connecting" : "Disconnected";
   Text("StatusBadge").Foreground = new SolidColorBrush(Active ? Color.FromRgb(104, 229, 191) : Color.FromRgb(184, 198, 218));
   Button("ConnectButton").Content = busy ? "Please wait…" : Active ? "Disconnect" : "Connect";
   Button("ConnectButton").IsEnabled = !busy && (bundle != null || Active);
   foreach (string name in new[] { "ImportButton", "AddApp", "AddFolder", "RemoveApp" }) Button(name).IsEnabled = !busy && !Active;
   foreach (string name in new[] { "FullMode", "SplitMode", "IncludeMode", "ExcludeMode" }) Radio(name).IsEnabled = !busy && !Active;
   Get<ListBox>("AppList").IsEnabled = !busy && !Active;
   string elapsed = started.HasValue ? (DateTime.UtcNow - started.Value).ToString(@"hh\:mm\:ss") : "";
   Text("Message").Text = busy ? lastMessage : state == "Running" ? "Connected  ·  " + elapsed + "  ·  Local network stays direct" : state == "Reconnecting" ? "Reconnecting the transport…" : String.IsNullOrEmpty(lastMessage) ? "Import a profile to get started." : lastMessage;
   if (tray != null) tray.Text = "CWG · " + (Active ? "Connected" : "Disconnected");
  }
  void Error(string message) { busy = false; lastMessage = message; Refresh(); MessageBox.Show(window, message, "CWG", MessageBoxButton.OK, MessageBoxImage.Warning); }
  void VerifyTools() {
   using (var stream = Assembly.GetExecutingAssembly().GetManifestResourceStream("Cwg.ToolHashes"))
   using (var reader = new StreamReader(stream)) {
    string line;
    while ((line = reader.ReadLine()) != null) {
     int split = line.IndexOf("  ", StringComparison.Ordinal); if (split < 0) continue;
     string expected = line.Substring(0, split), relative = line.Substring(split + 2), path = Path.Combine(root, relative.Replace('/', '\\'));
     if (!File.Exists(path)) throw new InvalidOperationException("CWG is missing " + relative + ". Extract the complete app folder.");
     using (var hash = SHA256.Create()) using (var input = File.OpenRead(path)) {
      string actual = BitConverter.ToString(hash.ComputeHash(input)).Replace("-", "").ToLowerInvariant();
      if (actual != expected) throw new InvalidOperationException("A CWG program file has changed: " + relative + ". Use a complete matching app release.");
     }
    }
   }
  }
  async Task<Dictionary<string, object>> Execute(object job, int timeout) {
   VerifyTools(); string request = json.Serialize(job);
   return await Task.Run(async delegate {
    var info = new ProcessStartInfo(Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.Windows), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")) {
     Arguments = "-NoProfile -NonInteractive -ExecutionPolicy Bypass -File \"" + Path.Combine(root, "scripts", "Desktop-Bridge.ps1") + "\"",
     WorkingDirectory = root, UseShellExecute = false, CreateNoWindow = true, WindowStyle = ProcessWindowStyle.Hidden,
     RedirectStandardInput = true, RedirectStandardOutput = true, RedirectStandardError = true, StandardOutputEncoding = Encoding.UTF8
    };
    using (var process = Process.Start(info)) {
     var output = process.StandardOutput.ReadToEndAsync(); var error = process.StandardError.ReadToEndAsync();
     process.StandardInput.Write(request); process.StandardInput.Close();
     if (!process.WaitForExit(timeout)) { process.Kill(); throw new InvalidOperationException("This operation timed out. Use Disconnect to clear any session before retrying."); }
     string data = await output; await error;
     Dictionary<string, object> result;
     try { result = json.Deserialize<Dictionary<string, object>>(data); }
     catch { throw new InvalidOperationException("CWG could not complete this operation. Extract the complete app folder and try again."); }
     if (result == null || !result.ContainsKey("Success") || !(bool)result["Success"]) throw new InvalidOperationException(result != null && result.ContainsKey("Error") ? Convert.ToString(result["Error"]) : "CWG operation failed.");
     return result.ContainsKey("Result") ? (Dictionary<string, object>)result["Result"] : new Dictionary<string, object>();
    }
   });
  }
  async Task Import() {
   var dialog = new Microsoft.Win32.OpenFileDialog { Title = "Import a CWG configuration", Filter = "CWG configuration (*.cwg)|*.cwg" };
   if (dialog.ShowDialog(window) != true) return;
   busy = true; lastMessage = "Checking and importing your private profile…"; Refresh();
   try { var result = await Execute(new { Action = "Import", Archive = dialog.FileName }, 30000); LoadProfile(Convert.ToString(result["Bundle"])); Write(Path.Combine(root, "selected-profile.json"), new { Bundle = bundle }); }
   catch (Exception ex) { Error(ex.Message); }
   finally { busy = false; Refresh(); }
  }
  async Task Connect() {
   if (busy || bundle == null) return;
   if (Mode == "Include" && entries.Count == 0) { Error("Add at least one app, or choose Full PC."); return; }
   SavePreferences(); busy = true; lastMessage = "Preparing your connection…"; Refresh();
   try {
    if (!File.Exists(Path.Combine(root, "bin", "app-engine", "sing-box.exe"))) { lastMessage = "Installing the verified routing engine for first use…"; Refresh(); await Execute(new { Action = "InstallEngine" }, 240000); }
    lastMessage = "Connecting…"; Refresh();
    await Execute(new { Action = "Start", Bundle = bundle, Mode = Mode, Applications = entries.ToArray() }, 90000);
    lastMessage = "Tunnel started.";
   } catch (Exception ex) { Error(ex.Message); }
   finally { busy = false; PollState(); }
  }
  async Task Disconnect() {
   if (busy) return;
   busy = true; lastMessage = "Disconnecting and restoring normal networking…"; Refresh();
   try { await Execute(new { Action = "Stop" }, 45000); state = "Stopped"; lastMessage = "Disconnected. Normal networking restored."; }
   catch (Exception ex) { Error(ex.Message); }
   finally { busy = false; PollState(); }
  }
  void Render(string destination) {
   account = "Personal profile"; endpoint = "203.0.113.10:443";
   Radio("SplitMode").IsChecked = true;
   entries.Add(new AppEntry { kind = "exe", path = @"C:\Program Files\Mozilla Firefox\firefox.exe" });
   entries.Add(new AppEntry { kind = "folder", path = @"C:\Games\My Game" });
   lastMessage = "Ready to connect."; bundle = Path.Combine(root, "config"); RenderEntries(); RenderMode(); Refresh();
   window.ShowInTaskbar = false; window.WindowStartupLocation = WindowStartupLocation.Manual; window.Left = -20000; window.Top = -20000;
   window.Show(); window.UpdateLayout();
   var content = (FrameworkElement)window.Content;
   var bounds = new Rect(0, 0, content.ActualWidth, content.ActualHeight);
   var visual = new DrawingVisual();
   using (var drawing = visual.RenderOpen()) {
    drawing.DrawRectangle(window.Background, null, bounds);
    drawing.DrawRectangle(new VisualBrush(content) { Stretch = Stretch.Fill }, null, bounds);
   }
   var image = new RenderTargetBitmap((int)Math.Ceiling(bounds.Width), (int)Math.Ceiling(bounds.Height), 96, 96, PixelFormats.Pbgra32); image.Render(visual);
   var encoder = new PngBitmapEncoder(); encoder.Frames.Add(BitmapFrame.Create(image));
   using (var output = File.Create(Path.GetFullPath(destination))) encoder.Save(output);
   quitting = true; window.Close();
  }
 }
}
