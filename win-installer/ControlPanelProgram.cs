using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.IO;
using System.Net;
using System.Net.NetworkInformation;
using System.Net.Sockets;
using System.Runtime.InteropServices;
using System.Security.AccessControl;
using System.Security.Cryptography.X509Certificates;
using System.Security.Principal;
using System.Text;
using System.Text.RegularExpressions;
using System.Threading;
using System.Web.Script.Serialization;
using System.Windows.Forms;
using Microsoft.Win32;

namespace XiaobaoDeskApp
{
    static class NativeMethods
    {
        public const uint DISPLAY_DEVICE_ATTACHED_TO_DESKTOP = 0x00000001;
        public const uint DISPLAY_DEVICE_PRIMARY_DEVICE = 0x00000004;
        public const uint DM_POSITION = 0x00000020;
        public const uint DM_PELSWIDTH = 0x00080000;
        public const uint DM_PELSHEIGHT = 0x00100000;
        public const uint CDS_UPDATEREGISTRY = 0x00000001;
        public const uint CDS_NORESET = 0x10000000;
        public const uint SDC_TOPOLOGY_INTERNAL = 0x00000001;
        public const uint SDC_APPLY = 0x00000080;
        public const uint SWP_NOSIZE = 0x0001;
        public const uint SWP_NOZORDER = 0x0004;
        public const uint SWP_NOACTIVATE = 0x0010;

        [StructLayout(LayoutKind.Sequential)]
        public struct RECT
        {
            public int Left;
            public int Top;
            public int Right;
            public int Bottom;
        }

        [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
        public struct DISPLAY_DEVICE
        {
            public int cb;
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 32)]
            public string DeviceName;
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)]
            public string DeviceString;
            public uint StateFlags;
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)]
            public string DeviceID;
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)]
            public string DeviceKey;
        }

        [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
        public struct DEVMODEW
        {
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 32)]
            public string dmDeviceName;
            public ushort dmSpecVersion;
            public ushort dmDriverVersion;
            public ushort dmSize;
            public ushort dmDriverExtra;
            public uint dmFields;
            public int dmPositionX;
            public int dmPositionY;
            public uint dmDisplayOrientation;
            public uint dmDisplayFixedOutput;
            public short dmColor;
            public short dmDuplex;
            public short dmYResolution;
            public short dmTTOption;
            public short dmCollate;
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 32)]
            public string dmFormName;
            public ushort dmLogPixels;
            public uint dmBitsPerPel;
            public uint dmPelsWidth;
            public uint dmPelsHeight;
            public uint dmDisplayFlags;
            public uint dmDisplayFrequency;
            public uint dmICMMethod;
            public uint dmICMIntent;
            public uint dmMediaType;
            public uint dmDitherType;
            public uint dmReserved1;
            public uint dmReserved2;
            public uint dmPanningWidth;
            public uint dmPanningHeight;
        }

        public delegate bool EnumWindowsProc(IntPtr hWnd, IntPtr lParam);

        [DllImport("user32.dll")]
        public static extern bool SetProcessDPIAware();

        [DllImport("user32.dll", CharSet = CharSet.Unicode)]
        public static extern bool EnumDisplayDevicesW(string lpDevice, uint iDevNum, ref DISPLAY_DEVICE lpDisplayDevice, uint dwFlags);

        [DllImport("user32.dll", CharSet = CharSet.Unicode)]
        public static extern int ChangeDisplaySettingsExW(string lpszDeviceName, ref DEVMODEW lpDevMode, IntPtr hwnd, uint dwflags, IntPtr lParam);

        [DllImport("user32.dll", CharSet = CharSet.Unicode)]
        public static extern int ChangeDisplaySettingsExW(string lpszDeviceName, IntPtr lpDevMode, IntPtr hwnd, uint dwflags, IntPtr lParam);

        [DllImport("user32.dll")]
        public static extern int SetDisplayConfig(uint numPathArrayElements, IntPtr pathArray, uint numModeInfoArrayElements, IntPtr modeInfoArray, uint flags);

        [DllImport("user32.dll")]
        public static extern bool EnumWindows(EnumWindowsProc lpEnumFunc, IntPtr lParam);

        [DllImport("user32.dll")]
        public static extern bool IsWindowVisible(IntPtr hWnd);

        [DllImport("user32.dll")]
        public static extern bool IsIconic(IntPtr hWnd);

        [DllImport("user32.dll")]
        public static extern int GetWindowTextLengthW(IntPtr hWnd);

        [DllImport("user32.dll")]
        public static extern bool GetWindowRect(IntPtr hWnd, out RECT lpRect);

        [DllImport("user32.dll")]
        public static extern bool SetWindowPos(IntPtr hWnd, IntPtr hWndInsertAfter, int X, int Y, int cx, int cy, uint uFlags);
    }

    static class DisplayHelper
    {
        public static bool IsVirtualDisplayDevice(NativeMethods.DISPLAY_DEVICE dd)
        {
            string desc = dd.DeviceString ?? "";
            string id = dd.DeviceID ?? "";
            return desc.IndexOf("Virtual Display Driver", StringComparison.OrdinalIgnoreCase) >= 0
                || desc.IndexOf("MttVDD", StringComparison.OrdinalIgnoreCase) >= 0
                || id.IndexOf("MttVDD", StringComparison.OrdinalIgnoreCase) >= 0;
        }

        public static bool DetachVirtualDisplays(bool forceInternalTopology)
        {
            bool detachedAny = false;
            try
            {
                for (uint i = 0; i < 32; i++)
                {
                    var dd = new NativeMethods.DISPLAY_DEVICE();
                    dd.cb = Marshal.SizeOf(typeof(NativeMethods.DISPLAY_DEVICE));
                    if (!NativeMethods.EnumDisplayDevicesW(null, i, ref dd, 0))
                    {
                        break;
                    }
                    bool attached = (dd.StateFlags & NativeMethods.DISPLAY_DEVICE_ATTACHED_TO_DESKTOP) != 0;
                    bool primary = (dd.StateFlags & NativeMethods.DISPLAY_DEVICE_PRIMARY_DEVICE) != 0;
                    if (attached && !primary && IsVirtualDisplayDevice(dd))
                    {
                        var dm = new NativeMethods.DEVMODEW();
                        dm.dmSize = (ushort)Marshal.SizeOf(typeof(NativeMethods.DEVMODEW));
                        dm.dmFields = NativeMethods.DM_POSITION | NativeMethods.DM_PELSWIDTH | NativeMethods.DM_PELSHEIGHT;
                        dm.dmPelsWidth = 0;
                        dm.dmPelsHeight = 0;
                        NativeMethods.ChangeDisplaySettingsExW(
                            dd.DeviceName,
                            ref dm,
                            IntPtr.Zero,
                            NativeMethods.CDS_UPDATEREGISTRY | NativeMethods.CDS_NORESET,
                            IntPtr.Zero);
                        detachedAny = true;
                    }
                }
                if (detachedAny)
                {
                    NativeMethods.ChangeDisplaySettingsExW(null, IntPtr.Zero, IntPtr.Zero, 0, IntPtr.Zero);
                }
                if (detachedAny || forceInternalTopology)
                {
                    NativeMethods.SetDisplayConfig(
                        0,
                        IntPtr.Zero,
                        0,
                        IntPtr.Zero,
                        NativeMethods.SDC_TOPOLOGY_INTERNAL | NativeMethods.SDC_APPLY);
                }
            }
            catch { }
            return detachedAny;
        }

        public static void RescueOffScreenWindows()
        {
            try
            {
                Rectangle primary = Screen.PrimaryScreen.WorkingArea;
                NativeMethods.EnumWindows(delegate(IntPtr hWnd, IntPtr lParam)
                {
                    if (!NativeMethods.IsWindowVisible(hWnd) || NativeMethods.IsIconic(hWnd))
                    {
                        return true;
                    }
                    if (NativeMethods.GetWindowTextLengthW(hWnd) <= 0)
                    {
                        return true;
                    }
                    NativeMethods.RECT rc;
                    if (!NativeMethods.GetWindowRect(hWnd, out rc))
                    {
                        return true;
                    }
                    int w = rc.Right - rc.Left;
                    int h = rc.Bottom - rc.Top;
                    if (w <= 0 || h <= 0)
                    {
                        return true;
                    }
                    var winRect = new Rectangle(rc.Left, rc.Top, w, h);
                    if (!primary.IntersectsWith(winRect))
                    {
                        int newX = primary.Left + 40;
                        int newY = primary.Top + 40;
                        NativeMethods.SetWindowPos(
                            hWnd,
                            IntPtr.Zero,
                            newX,
                            newY,
                            0,
                            0,
                            NativeMethods.SWP_NOSIZE | NativeMethods.SWP_NOZORDER | NativeMethods.SWP_NOACTIVATE);
                    }
                    return true;
                }, IntPtr.Zero);
            }
            catch { }
        }
    }

    static class Program
    {
        [STAThread]
        static int Main(string[] args)
        {
            bool repairAdmin = false;
            bool startMinimized = false;
            for (int i = 0; i < args.Length; i++)
            {
                if (args[i] == "--repair-admin") repairAdmin = true;
                if (args[i] == "--minimized") startMinimized = true;
            }

            if (repairAdmin)
            {
                return AdminRepair.Run(AppDomain.CurrentDomain.BaseDirectory);
            }

            NativeMethods.SetProcessDPIAware();
            bool createdNew;
            using (var mutex = new Mutex(true, "Local\\XiaobaoDeskControlPanelSingleton", out createdNew))
            {
                if (!createdNew)
                {
                    MessageBox.Show("XiaobaoDesk 已在系统托盘中运行，请双击右下角托盘图标打开控制台。", "XiaobaoDesk", MessageBoxButtons.OK, MessageBoxIcon.Information);
                    return 0;
                }
                Application.EnableVisualStyles();
                Application.SetCompatibleTextRenderingDefault(false);
                Application.Run(new ControlPanelForm(startMinimized));
            }
            return 0;
        }
    }

    static class AdminRepair
    {
        public static int Run(string appDir)
        {
            try
            {
                string hubExe = Path.Combine(appDir, "xb-hub.exe");
                string hostExe = Path.Combine(appDir, "xb-host.exe");
                string appExe = Path.Combine(appDir, "XiaobaoDesk.exe");

                RunHidden("netsh", "advfirewall firewall delete rule name=\"XiaobaoDesk-Hub-TCP-21116\"");
                RunHidden("netsh", "advfirewall firewall delete rule name=\"XiaobaoDesk-Hub-UDP-21116\"");
                RunHidden("netsh", "advfirewall firewall delete rule name=\"XiaobaoDesk-Host-TCP-21118\"");
                RunHidden("netsh", "advfirewall firewall delete rule name=\"XiaobaoDesk-Host-UDP-21118\"");

                if (File.Exists(hubExe))
                {
                    RunHidden("netsh", string.Format("advfirewall firewall add rule name=\"XiaobaoDesk-Hub-TCP-21116\" dir=in action=allow protocol=TCP localport=21116 program=\"{0}\" profile=any", hubExe));
                    RunHidden("netsh", string.Format("advfirewall firewall add rule name=\"XiaobaoDesk-Hub-UDP-21116\" dir=in action=allow protocol=UDP localport=21116 program=\"{0}\" profile=any", hubExe));
                }
                if (File.Exists(hostExe))
                {
                    RunHidden("netsh", string.Format("advfirewall firewall add rule name=\"XiaobaoDesk-Host-TCP-21118\" dir=in action=allow protocol=TCP localport=21118 program=\"{0}\" profile=any", hostExe));
                    RunHidden("netsh", string.Format("advfirewall firewall add rule name=\"XiaobaoDesk-Host-UDP-21118\" dir=in action=allow protocol=UDP localport=21118 program=\"{0}\" profile=any", hostExe));
                }

                InstallVirtualDisplayDriver(appDir);

                if (File.Exists(appExe))
                {
                    RunHidden("schtasks", string.Format("/Create /TN \"XiaobaoDesk-App\" /TR \"\\\"{0}\\\" --minimized\" /SC ONLOGON /RL HIGHEST /F", appExe));
                }
                return 0;
            }
            catch (Exception ex)
            {
                File.WriteAllText(Path.Combine(Path.GetTempPath(), "xiaobaodesk-repair-err.txt"), ex.ToString());
                return 1;
            }
        }

        private static void InstallVirtualDisplayDriver(string appDir)
        {
            string vddSrc = Path.Combine(appDir, "vdd");
            if (!Directory.Exists(vddSrc)) return;

            string targetDir = @"C:\VirtualDisplayDriver";
            string stageDir = Path.Combine(targetDir, "driver");
            Directory.CreateDirectory(targetDir);
            Directory.CreateDirectory(stageDir);

            string settingsSrc = Path.Combine(vddSrc, "vdd_settings.xml");
            string settingsDst = Path.Combine(targetDir, "vdd_settings.xml");
            if (File.Exists(settingsSrc))
            {
                string xml = File.ReadAllText(settingsSrc, Encoding.UTF8);
                xml = Regex.Replace(xml, @"<count>\s*\d+\s*</count>", "<count>0</count>");
                File.WriteAllText(settingsDst, xml, new UTF8Encoding(false));
            }
            else if (File.Exists(settingsDst))
            {
                string xml = File.ReadAllText(settingsDst, Encoding.UTF8);
                xml = Regex.Replace(xml, @"<count>\s*\d+\s*</count>", "<count>0</count>");
                File.WriteAllText(settingsDst, xml, new UTF8Encoding(false));
            }

            using (var key = Registry.LocalMachine.CreateSubKey(@"SOFTWARE\MikeTheTech\VirtualDisplayDriver"))
            {
                if (key != null) key.SetValue("VDDPATH", targetDir, RegistryValueKind.String);
            }

            foreach (string f in Directory.GetFiles(vddSrc))
            {
                File.Copy(f, Path.Combine(stageDir, Path.GetFileName(f)), true);
            }

            string catFile = Path.Combine(stageDir, "mttvdd.cat");
            if (File.Exists(catFile))
            {
                try
                {
                    var cert = X509Certificate.CreateFromSignedFile(catFile);
                    var cert2 = new X509Certificate2(cert);
                    var store = new X509Store(StoreName.TrustedPublisher, StoreLocation.LocalMachine);
                    store.Open(OpenFlags.ReadWrite);
                    if (!store.Certificates.Contains(cert2))
                    {
                        store.Add(cert2);
                    }
                    store.Close();
                }
                catch { }
            }

            string infFile = Path.Combine(stageDir, "MttVDD.inf");
            string nefconc = Path.Combine(stageDir, "nefconc.exe");
            if (File.Exists(infFile) && File.Exists(nefconc))
            {
                int rc = RunHidden(nefconc, string.Format("install \"{0}\" \"Root\\MttVDD\" --no-duplicates --remove-duplicates", infFile));
                if (rc != 0 && rc != 3010)
                {
                    RunHidden("pnputil.exe", string.Format("/add-driver \"{0}\" /install", infFile));
                }
            }
            DisplayHelper.DetachVirtualDisplays(true);
        }

        private static int RunHidden(string fileName, string args)
        {
            var psi = new ProcessStartInfo
            {
                FileName = fileName,
                Arguments = args,
                UseShellExecute = false,
                CreateNoWindow = true,
                WindowStyle = ProcessWindowStyle.Hidden
            };
            using (var p = Process.Start(psi))
            {
                p.WaitForExit(30000);
                return p.ExitCode;
            }
        }
    }

    sealed class ControlPanelForm : Form
    {
        private readonly string _appDir;
        private readonly string _cfgDir;
        private readonly string _hostCfgPath;
        private readonly string _hubCfgPath;
        private readonly string _hubFpPath;
        private readonly string _uiCfgPath;
        private readonly string _hostLogPath;
        private readonly string _hubLogPath;
        private readonly string _hostExe;
        private readonly string _hubExe;

        private readonly JavaScriptSerializer _json = new JavaScriptSerializer();
        private readonly object _procLock = new object();
        private Process _hubProc;
        private Process _hostProc;
        private volatile bool _exiting;

        private readonly NotifyIcon _trayIcon;
        private readonly System.Windows.Forms.Timer _watchdogTimer;

        private RadioButton _rbLocalHub;
        private RadioButton _rbRemoteHub;
        private ComboBox _cbLocalAddrs;
        private TextBox _tbRemoteAddr;
        private TextBox _tbRemoteFp;
        private TextBox _tbRemoteSecret;
        private Button _btnPasteHubStr;

        private TextBox _tbMobileAddr;
        private TextBox _tbMobileFp;
        private TextBox _tbPassword;
        private Label _lblStatus;

        private string _mode = "local_hub";
        private string _localHubFp = "";
        private string _hostPassword = "";
        private string _remoteHubAddr = "";
        private string _remoteHubFp = "";
        private string _remoteHubSecret = "";
        private string _selectedLocalAddr = "";

        public ControlPanelForm(bool startMinimized)
        {
            _appDir = AppDomain.CurrentDomain.BaseDirectory;
            _hostExe = Path.Combine(_appDir, "xb-host.exe");
            _hubExe = Path.Combine(_appDir, "xb-hub.exe");

            _cfgDir = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), ".xiaobao-desk");
            Directory.CreateDirectory(_cfgDir);
            LockConfigDirectoryAcl(_cfgDir);
            _hostCfgPath = Path.Combine(_cfgDir, "host.json");
            _hubCfgPath = Path.Combine(_cfgDir, "hub.json");
            _hubFpPath = Path.Combine(_cfgDir, "hub-fingerprint.txt");
            _uiCfgPath = Path.Combine(_cfgDir, "ui-mode.json");
            _hostLogPath = Path.Combine(_cfgDir, "host.log");
            _hubLogPath = Path.Combine(_cfgDir, "hub.log");

            Text = "XiaobaoDesk 电脑端控制台 v1.6.0";
            Size = new Size(700, 668);
            StartPosition = FormStartPosition.CenterScreen;
            FormBorderStyle = FormBorderStyle.FixedDialog;
            MaximizeBox = false;
            Font = new Font("Microsoft YaHei UI", 9.5f);
            BackColor = Color.FromArgb(248, 250, 252);

            ForceSinglePrimaryScreenIfVirtual();
            BuildUi();
            EnsureConfigsInitialized();
            PopulateLocalAddresses();
            SyncUiFromState();
            RestartServices();

            var menu = new ContextMenuStrip();
            menu.Items.Add("打开控制台", null, (s, e) => ShowMainWindow());
            menu.Items.Add("复制手机一键配对码", null, (s, e) => CopyMobilePairingCode());
            menu.Items.Add("恢复单主屏", null, (s, e) => ForceSinglePrimaryScreenIfVirtual(true));
            menu.Items.Add("重启后台服务", null, (s, e) => RestartServices());
            menu.Items.Add(new ToolStripSeparator());
            menu.Items.Add("彻底退出 XiaobaoDesk", null, (s, e) => ExitCompletely());

            _trayIcon = new NotifyIcon
            {
                Icon = SystemIcons.Shield,
                Text = "XiaobaoDesk 运行中",
                ContextMenuStrip = menu,
                Visible = true
            };
            _trayIcon.DoubleClick += (s, e) => ShowMainWindow();

            _watchdogTimer = new System.Windows.Forms.Timer { Interval = 2500 };
            _watchdogTimer.Tick += (s, e) => EnsureServicesRunning();
            _watchdogTimer.Start();

            if (startMinimized)
            {
                WindowState = FormWindowState.Minimized;
                ShowInTaskbar = false;
            }
        }

        private void BuildUi()
        {
            var main = new Panel { Dock = DockStyle.Fill, Padding = new Padding(16) };
            Controls.Add(main);

            int y = 8;
            var lblHeader = new Label
            {
                Text = "XiaobaoDesk 远程桌面与无线副屏控制台 (默认裸 IP 直连，无需域名)",
                Font = new Font("Microsoft YaHei UI", 11.5f, FontStyle.Bold),
                ForeColor = Color.FromArgb(15, 23, 42),
                Location = new Point(16, y),
                Size = new Size(650, 28)
            };
            main.Controls.Add(lblHeader);
            y += 34;

            // Mode Group
            var grpMode = new GroupBox
            {
                Text = "第一步：选择电脑端部署模式",
                Location = new Point(16, y),
                Size = new Size(650, 210),
                ForeColor = Color.FromArgb(30, 41, 59)
            };
            main.Controls.Add(grpMode);

            _rbLocalHub = new RadioButton
            {
                Text = "无服务器模式（推荐：本机同时运行 被控端 + 内置服务端，不依赖任何云服务器）",
                Location = new Point(14, 24),
                Size = new Size(620, 24),
                Checked = true
            };
            _rbLocalHub.CheckedChanged += (s, e) => { if (_rbLocalHub.Checked) OnModeToggle(true); };
            grpMode.Controls.Add(_rbLocalHub);

            var lblLocalIp = new Label
            {
                Text = "对外连接裸 IP (公网IPv6可手机流量直连，局域网IP仅限同Wi-Fi):",
                Location = new Point(34, 52),
                Size = new Size(430, 20),
                ForeColor = Color.FromArgb(71, 85, 105)
            };
            grpMode.Controls.Add(lblLocalIp);

            _cbLocalAddrs = new ComboBox
            {
                Location = new Point(34, 74),
                Size = new Size(595, 28),
                DropDownStyle = ComboBoxStyle.DropDown
            };
            _cbLocalAddrs.TextChanged += (s, e) => { if (_rbLocalHub != null && _rbLocalHub.Checked) UpdateMobilePreview(); };
            grpMode.Controls.Add(_cbLocalAddrs);

            _rbRemoteHub = new RadioButton
            {
                Text = "独立服务器模式（接入你在 Linux 服务器上用一行 bash 命令安装的服务端）",
                Location = new Point(14, 112),
                Size = new Size(440, 24)
            };
            _rbRemoteHub.CheckedChanged += (s, e) => { if (_rbRemoteHub.Checked) OnModeToggle(false); };
            grpMode.Controls.Add(_rbRemoteHub);

            _btnPasteHubStr = new Button
            {
                Text = "粘贴 xbhub:// 接入串",
                Location = new Point(465, 109),
                Size = new Size(165, 28)
            };
            _btnPasteHubStr.Click += (s, e) => PasteHubConnectionString();
            grpMode.Controls.Add(_btnPasteHubStr);

            grpMode.Controls.Add(new Label { Text = "服务器裸 IP:21116", Location = new Point(34, 144), Size = new Size(125, 22) });
            _tbRemoteAddr = new TextBox { Location = new Point(162, 141), Size = new Size(210, 24) };
            _tbRemoteAddr.TextChanged += (s, e) => { if (_rbRemoteHub != null && _rbRemoteHub.Checked) UpdateMobilePreview(); };
            grpMode.Controls.Add(_tbRemoteAddr);

            grpMode.Controls.Add(new Label { Text = "服务器密钥", Location = new Point(382, 144), Size = new Size(76, 22) });
            _tbRemoteSecret = new TextBox { Location = new Point(462, 141), Size = new Size(167, 24), UseSystemPasswordChar = true };
            grpMode.Controls.Add(_tbRemoteSecret);

            grpMode.Controls.Add(new Label { Text = "服务器证书指纹", Location = new Point(34, 174), Size = new Size(125, 22) });
            _tbRemoteFp = new TextBox { Location = new Point(162, 171), Size = new Size(467, 24), Font = new Font("Consolas", 8.5f) };
            _tbRemoteFp.TextChanged += (s, e) => { if (_rbRemoteHub != null && _rbRemoteHub.Checked) UpdateMobilePreview(); };
            grpMode.Controls.Add(_tbRemoteFp);

            y += 220;

            // Mobile Pairing Group
            var grpMobile = new GroupBox
            {
                Text = "第二步：手机端连接信息（两种部署模式下手机操作完全一致）",
                Location = new Point(16, y),
                Size = new Size(650, 200),
                ForeColor = Color.FromArgb(30, 41, 59)
            };
            main.Controls.Add(grpMobile);

            grpMobile.Controls.Add(new Label { Text = "服务端地址 (裸 IP)", Location = new Point(14, 28), Size = new Size(135, 22) });
            _tbMobileAddr = new TextBox { Location = new Point(155, 25), Size = new Size(475, 24), ReadOnly = true, BackColor = Color.White };
            grpMobile.Controls.Add(_tbMobileAddr);

            grpMobile.Controls.Add(new Label { Text = "证书 SHA-256 指纹", Location = new Point(14, 58), Size = new Size(135, 22) });
            _tbMobileFp = new TextBox { Location = new Point(155, 55), Size = new Size(475, 24), ReadOnly = true, BackColor = Color.White, Font = new Font("Consolas", 8.5f) };
            grpMobile.Controls.Add(_tbMobileFp);

            grpMobile.Controls.Add(new Label { Text = "连接密码 (可修改)", Location = new Point(14, 88), Size = new Size(135, 22) });
            _tbPassword = new TextBox { Location = new Point(155, 85), Size = new Size(340, 24) };
            grpMobile.Controls.Add(_tbPassword);

            var btnSaveApply = new Button
            {
                Text = "保存并应用配置",
                Location = new Point(505, 83),
                Size = new Size(125, 28)
            };
            btnSaveApply.Click += (s, e) => { SaveAndApplyConfig(); MessageBox.Show("配置已保存并重启生效。", "XiaobaoDesk", MessageBoxButtons.OK, MessageBoxIcon.Information); };
            grpMobile.Controls.Add(btnSaveApply);

            var btnCopyPair = new Button
            {
                Text = "复制手机一键配对码（发到手机后打开 App 点「粘贴配对码」即连）",
                Location = new Point(14, 122),
                Size = new Size(616, 42),
                BackColor = Color.FromArgb(37, 99, 235),
                ForeColor = Color.White,
                FlatStyle = FlatStyle.Flat,
                Font = new Font("Microsoft YaHei UI", 10f, FontStyle.Bold)
            };
            btnCopyPair.Click += (s, e) => CopyMobilePairingCode();
            grpMobile.Controls.Add(btnCopyPair);

            var lblTip = new Label
            {
                Text = "提示：手机端 Host ID 保持默认 default-host；如使用独立公网 IPv4 路由器，请确认映射端口 TCP/UDP 21116 与 21118。",
                Location = new Point(14, 170),
                Size = new Size(620, 22),
                ForeColor = Color.FromArgb(100, 116, 139),
                Font = new Font("Microsoft YaHei UI", 8.5f)
            };
            grpMobile.Controls.Add(lblTip);

            y += 210;

            // Status & Actions
            _lblStatus = new Label
            {
                Text = "状态检测中...",
                Location = new Point(16, y),
                Size = new Size(650, 24),
                ForeColor = Color.FromArgb(22, 163, 74),
                Font = new Font("Microsoft YaHei UI", 9.5f, FontStyle.Bold)
            };
            main.Controls.Add(_lblStatus);
            y += 30;

            var btnRepair = new Button { Text = "弹窗授权修复防火墙与副屏驱动", Location = new Point(16, y), Size = new Size(215, 34) };
            btnRepair.Click += (s, e) => RunAdminRepairWithUac();
            main.Controls.Add(btnRepair);

            var btnRestart = new Button { Text = "重启后台服务", Location = new Point(242, y), Size = new Size(120, 34) };
            btnRestart.Click += (s, e) => RestartServices();
            main.Controls.Add(btnRestart);

            var btnLogs = new Button { Text = "查看运行日志", Location = new Point(372, y), Size = new Size(120, 34) };
            btnLogs.Click += (s, e) => OpenLogs();
            main.Controls.Add(btnLogs);

            var btnExit = new Button { Text = "彻底退出", Location = new Point(546, y), Size = new Size(120, 34) };
            btnExit.Click += (s, e) => ExitCompletely();
            main.Controls.Add(btnExit);

            y += 42;
            var btnRestorePrimary = new Button
            {
                Text = "一键恢复单主屏（解除虚拟副屏）",
                Location = new Point(16, y),
                Size = new Size(650, 36),
                BackColor = Color.FromArgb(245, 158, 11),
                ForeColor = Color.FromArgb(15, 23, 42),
                FlatStyle = FlatStyle.Flat,
                Font = new Font("Microsoft YaHei UI", 9.5f, FontStyle.Bold)
            };
            btnRestorePrimary.Click += (s, e) => ForceSinglePrimaryScreenIfVirtual(true);
            main.Controls.Add(btnRestorePrimary);
        }

        private void EnsureConfigsInitialized()
        {
            if (!File.Exists(_hubCfgPath) && File.Exists(_hubExe))
            {
                RunProcessWait(_hubExe, string.Format("--init-config --config \"{0}\"", _hubCfgPath));
            }
            if (!File.Exists(_hostCfgPath) && File.Exists(_hostExe))
            {
                RunProcessWait(_hostExe, string.Format("--init-config --config \"{0}\"", _hostCfgPath));
            }
            if (File.Exists(_hubFpPath))
            {
                _localHubFp = File.ReadAllText(_hubFpPath, Encoding.UTF8).Trim();
            }

            if (File.Exists(_uiCfgPath))
            {
                try
                {
                    var ui = _json.Deserialize<Dictionary<string, object>>(File.ReadAllText(_uiCfgPath, Encoding.UTF8));
                    if (ui != null)
                    {
                        if (ui.ContainsKey("mode")) _mode = Convert.ToString(ui["mode"]);
                        if (ui.ContainsKey("selected_local_addr")) _selectedLocalAddr = Convert.ToString(ui["selected_local_addr"]);
                        if (ui.ContainsKey("remote_hub_addr")) _remoteHubAddr = Convert.ToString(ui["remote_hub_addr"]);
                        if (ui.ContainsKey("remote_hub_fp")) _remoteHubFp = Convert.ToString(ui["remote_hub_fp"]);
                        if (ui.ContainsKey("remote_hub_secret")) _remoteHubSecret = Convert.ToString(ui["remote_hub_secret"]);
                    }
                }
                catch { }
            }

            if (File.Exists(_hostCfgPath))
            {
                try
                {
                    var host = _json.Deserialize<Dictionary<string, object>>(File.ReadAllText(_hostCfgPath, Encoding.UTF8));
                    if (host != null && host.ContainsKey("host_password"))
                    {
                        _hostPassword = Convert.ToString(host["host_password"]);
                    }
                }
                catch { }
            }

            ApplyConfigFiles();
        }

        private void ApplyConfigFiles()
        {
            if (!File.Exists(_hostCfgPath)) return;
            var host = _json.Deserialize<Dictionary<string, object>>(File.ReadAllText(_hostCfgPath, Encoding.UTF8));
            if (host == null) host = new Dictionary<string, object>();

            host["host_id"] = "default-host";
            host["require_totp"] = false;
            host["media_core_path"] = Path.Combine(_appDir, "xb-media-core.exe");
            if (!string.IsNullOrEmpty(_hostPassword))
            {
                host["host_password"] = _hostPassword;
            }

            if (_mode == "remote_hub" && !string.IsNullOrEmpty(_remoteHubAddr))
            {
                host["hub_addr"] = _remoteHubAddr;
                host["hub_cert_sha256"] = _remoteHubFp;
                host["hub_auth_secret"] = _remoteHubSecret;
            }
            else
            {
                host["hub_addr"] = "127.0.0.1:21116";
                host["hub_cert_sha256"] = _localHubFp;
                host["hub_auth_secret"] = _hostPassword;

                if (File.Exists(_hubCfgPath))
                {
                    var hub = _json.Deserialize<Dictionary<string, object>>(File.ReadAllText(_hubCfgPath, Encoding.UTF8));
                    if (hub == null) hub = new Dictionary<string, object>();
                    hub["listen_addr"] = ":21116";
                    hub["auth_secret"] = _hostPassword;
                    File.WriteAllText(_hubCfgPath, _json.Serialize(hub), Encoding.UTF8);
                }
            }

            File.WriteAllText(_hostCfgPath, _json.Serialize(host), Encoding.UTF8);

            var ui = new Dictionary<string, object>();
            ui["mode"] = _mode;
            ui["selected_local_addr"] = _selectedLocalAddr;
            ui["remote_hub_addr"] = _remoteHubAddr;
            ui["remote_hub_fp"] = _remoteHubFp;
            ui["remote_hub_secret"] = _remoteHubSecret;
            File.WriteAllText(_uiCfgPath, _json.Serialize(ui), Encoding.UTF8);
            LockConfigDirectoryAcl(_cfgDir);
        }

        private static void LockConfigDirectoryAcl(string dirPath)
        {
            try
            {
                if (!Directory.Exists(dirPath)) return;
                var dirInfo = new DirectoryInfo(dirPath);
                var sec = dirInfo.GetAccessControl();
                sec.SetAccessRuleProtection(true, false);
                var existing = sec.GetAccessRules(true, true, typeof(SecurityIdentifier));
                foreach (FileSystemAccessRule rule in existing)
                {
                    sec.RemoveAccessRule(rule);
                }
                var currentUser = WindowsIdentity.GetCurrent().User;
                var systemSid = new SecurityIdentifier(WellKnownSidType.LocalSystemSid, null);
                var flags = InheritanceFlags.ContainerInherit | InheritanceFlags.ObjectInherit;
                if (currentUser != null)
                {
                    sec.AddAccessRule(new FileSystemAccessRule(currentUser, FileSystemRights.FullControl, flags, PropagationFlags.None, AccessControlType.Allow));
                }
                sec.AddAccessRule(new FileSystemAccessRule(systemSid, FileSystemRights.FullControl, flags, PropagationFlags.None, AccessControlType.Allow));
                dirInfo.SetAccessControl(sec);
            }
            catch { }
        }

        private void ForceSinglePrimaryScreenIfVirtual()
        {
            ForceSinglePrimaryScreenIfVirtual(false);
        }

        private void ForceSinglePrimaryScreenIfVirtual(bool showFeedback)
        {
            bool detached = DisplayHelper.DetachVirtualDisplays(showFeedback);
            DisplayHelper.RescueOffScreenWindows();
            if (Visible)
            {
                Rectangle primary = Screen.PrimaryScreen.WorkingArea;
                if (!primary.IntersectsWith(Bounds))
                {
                    Location = new Point(primary.Left + 40, primary.Top + 40);
                }
            }
            if (showFeedback)
            {
                string msg = detached
                    ? "已解除虚拟副屏并恢复单主屏显示，同时将副屏窗口拉回主屏。"
                    : "未检测到活跃的虚拟副屏，已执行单主屏拓扑确认并将屏外窗口拉回主屏。";
                MessageBox.Show(msg, "XiaobaoDesk", MessageBoxButtons.OK, MessageBoxIcon.Information);
            }
        }

        private void PopulateLocalAddresses()
        {
            _cbLocalAddrs.Items.Clear();
            var ipv6Public = new List<string>();
            var ipv4Other = new List<string>();
            var ipv4Lan = new List<string>();

            try
            {
                foreach (var ni in NetworkInterface.GetAllNetworkInterfaces())
                {
                    if (ni.OperationalStatus != OperationalStatus.Up || ni.NetworkInterfaceType == NetworkInterfaceType.Loopback)
                        continue;
                    foreach (var ua in ni.GetIPProperties().UnicastAddresses)
                    {
                        var ip = ua.Address;
                        if (IPAddress.IsLoopback(ip) || ip.IsIPv6LinkLocal || ip.IsIPv6SiteLocal) continue;
                        if (ip.AddressFamily == AddressFamily.InterNetworkV6)
                        {
                            string s = ip.ToString();
                            int pct = s.IndexOf('%');
                            if (pct > 0) s = s.Substring(0, pct);
                            if (s.StartsWith("fe80", StringComparison.OrdinalIgnoreCase) || s.StartsWith("fd", StringComparison.OrdinalIgnoreCase) || s.StartsWith("fc", StringComparison.OrdinalIgnoreCase))
                                continue;
                            ipv6Public.Add(string.Format("[{0}]:21116", s));
                        }
                        else if (ip.AddressFamily == AddressFamily.InterNetwork)
                        {
                            string s = ip.ToString();
                            if (s.StartsWith("169.254.")) continue;
                            if (s.StartsWith("192.168.") || s.StartsWith("10.") || s.StartsWith("172."))
                                ipv4Lan.Add(string.Format("{0}:21116", s));
                            else
                                ipv4Other.Add(string.Format("{0}:21116", s));
                        }
                    }
                }
            }
            catch { }

            foreach (var x in ipv6Public) _cbLocalAddrs.Items.Add(x);
            foreach (var x in ipv4Other) _cbLocalAddrs.Items.Add(x);
            foreach (var x in ipv4Lan) _cbLocalAddrs.Items.Add(x);

            if (!string.IsNullOrEmpty(_selectedLocalAddr))
            {
                _cbLocalAddrs.Text = _selectedLocalAddr;
            }
            else if (_cbLocalAddrs.Items.Count > 0)
            {
                _cbLocalAddrs.SelectedIndex = 0;
            }
        }

        private void SyncUiFromState()
        {
            _rbLocalHub.Checked = (_mode != "remote_hub");
            _rbRemoteHub.Checked = (_mode == "remote_hub");
            _tbRemoteAddr.Text = _remoteHubAddr;
            _tbRemoteFp.Text = _remoteHubFp;
            _tbRemoteSecret.Text = _remoteHubSecret;
            _tbPassword.Text = _hostPassword;
            OnModeToggle(_mode != "remote_hub");
        }

        private void OnModeToggle(bool isLocal)
        {
            _cbLocalAddrs.Enabled = isLocal;
            _tbRemoteAddr.Enabled = !isLocal;
            _tbRemoteFp.Enabled = !isLocal;
            _tbRemoteSecret.Enabled = !isLocal;
            _btnPasteHubStr.Enabled = !isLocal;
            UpdateMobilePreview();
        }

        private string ExtractRawAddress(string text)
        {
            if (text == null) return "";
            return text.Trim();
        }

        private void UpdateMobilePreview()
        {
            if (_rbLocalHub.Checked)
            {
                _tbMobileAddr.Text = ExtractRawAddress(_cbLocalAddrs.Text);
                _tbMobileFp.Text = _localHubFp;
            }
            else
            {
                _tbMobileAddr.Text = _tbRemoteAddr.Text.Trim();
                _tbMobileFp.Text = _tbRemoteFp.Text.Trim();
            }
        }

        private void PasteHubConnectionString()
        {
            string raw = "";
            try { raw = Clipboard.GetText().Trim(); } catch { }
            if (string.IsNullOrEmpty(raw))
            {
                MessageBox.Show("剪贴板为空，请先复制 Linux 服务器安装完成后输出的 xbhub://... 接入串。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Warning);
                return;
            }
            try
            {
                int idx = raw.IndexOf("xbhub://", StringComparison.OrdinalIgnoreCase);
                string token = (idx >= 0) ? raw.Substring(idx + 8).Trim() : raw;
                int ws = token.IndexOfAny(new char[] { ' ', '\r', '\n', '\t' });
                if (ws > 0) token = token.Substring(0, ws);
                string b64 = token.Replace('-', '+').Replace('_', '/');
                while (b64.Length % 4 != 0) b64 += "=";
                string jsonStr = Encoding.UTF8.GetString(Convert.FromBase64String(b64));
                var obj = _json.Deserialize<Dictionary<string, object>>(jsonStr);
                _tbRemoteAddr.Text = Convert.ToString(obj["addr"]);
                _tbRemoteFp.Text = Convert.ToString(obj["fp"]);
                _tbRemoteSecret.Text = Convert.ToString(obj["secret"]);
                _rbRemoteHub.Checked = true;
                SaveAndApplyConfig();
                MessageBox.Show("已成功解析服务器接入串并切换到独立服务器模式！", "成功", MessageBoxButtons.OK, MessageBoxIcon.Information);
            }
            catch
            {
                MessageBox.Show("未能识别有效的 xbhub:// 接入串，请完整复制服务端输出的那一行。", "解析失败", MessageBoxButtons.OK, MessageBoxIcon.Error);
            }
        }

        private void SaveAndApplyConfig()
        {
            _mode = _rbRemoteHub.Checked ? "remote_hub" : "local_hub";
            _selectedLocalAddr = ExtractRawAddress(_cbLocalAddrs.Text);
            _remoteHubAddr = _tbRemoteAddr.Text.Trim();
            _remoteHubFp = _tbRemoteFp.Text.Trim();
            _remoteHubSecret = _tbRemoteSecret.Text.Trim();
            if (!string.IsNullOrWhiteSpace(_tbPassword.Text))
            {
                _hostPassword = _tbPassword.Text.Trim();
            }
            ApplyConfigFiles();
            UpdateMobilePreview();
            RestartServices();
        }

        private void CopyMobilePairingCode()
        {
            SaveAndApplyConfig();
            string addr = _tbMobileAddr.Text.Trim();
            string fp = _tbMobileFp.Text.Trim();
            if (string.IsNullOrEmpty(addr) || string.IsNullOrEmpty(fp))
            {
                MessageBox.Show("服务端裸 IP 或证书指纹为空，请先检查网络地址或粘贴服务器接入串。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Warning);
                return;
            }
            var profile = new Dictionary<string, object>();
            profile["name"] = Environment.MachineName;
            profile["hubAddress"] = addr;
            profile["hostId"] = "default-host";
            profile["certFingerprint"] = fp;
            profile["password"] = _hostPassword;
            profile["hubSecret"] = (_mode == "remote_hub") ? _remoteHubSecret : _hostPassword;
            profile["superScreen"] = false;
            profile["fps"] = 120;
            profile["codec"] = "hevc";
            profile["bitrateMbps"] = 16;

            string jsonStr = _json.Serialize(profile);
            string b64 = Convert.ToBase64String(Encoding.UTF8.GetBytes(jsonStr)).Replace('+', '-').Replace('/', '_').TrimEnd('=');
            string code = "xiaobaodesk://import?data=" + b64;
            Clipboard.SetText(code);
            MessageBox.Show("已复制手机端一键配对码到剪贴板！\n\n将它发送到手机，打开 XiaobaoDesk App 点击右上角「粘贴配对码」即可一键导入并连接。", "配对码已复制", MessageBoxButtons.OK, MessageBoxIcon.Information);
        }

        private void EnsureServicesRunning()
        {
            if (_exiting) return;
            lock (_procLock)
            {
                if (_mode != "remote_hub")
                {
                    if ((_hubProc == null || _hubProc.HasExited) && File.Exists(_hubExe))
                    {
                        _hubProc = StartHiddenProcess(_hubExe, string.Format("--config \"{0}\"", _hubCfgPath));
                    }
                }
                else
                {
                    StopProcess(ref _hubProc);
                }

                if ((_hostProc == null || _hostProc.HasExited) && File.Exists(_hostExe))
                {
                    _hostProc = StartHiddenProcess(_hostExe, string.Format("--config \"{0}\" --log \"{1}\"", _hostCfgPath, _hostLogPath));
                }

                string hubState = (_mode == "remote_hub") ? "已切换至独立服务器" : ((_hubProc != null && !_hubProc.HasExited) ? "内置服务端: 运行中" : "内置服务端: 未运行");
                string hostState = (_hostProc != null && !_hostProc.HasExited) ? string.Format("被控端: 运行中 (PID {0})", _hostProc.Id) : "被控端: 未运行";
                _lblStatus.Text = string.Format("运行状态：{0}  |  {1}", hubState, hostState);
            }
        }

        private void RestartServices()
        {
            lock (_procLock)
            {
                StopProcess(ref _hostProc);
                StopProcess(ref _hubProc);
                KillMediaCoreFromAppDir();
            }
            EnsureServicesRunning();
        }

        private void KillMediaCoreFromAppDir()
        {
            try
            {
                foreach (var p in Process.GetProcessesByName("xb-media-core"))
                {
                    try
                    {
                        if (p.MainModule != null && p.MainModule.FileName.StartsWith(_appDir, StringComparison.OrdinalIgnoreCase))
                        {
                            p.Kill();
                        }
                    }
                    catch { }
                }
            }
            catch { }
        }

        private Process StartHiddenProcess(string exe, string args)
        {
            try
            {
                return Process.Start(new ProcessStartInfo
                {
                    FileName = exe,
                    Arguments = args,
                    WorkingDirectory = _appDir,
                    UseShellExecute = false,
                    CreateNoWindow = true,
                    WindowStyle = ProcessWindowStyle.Hidden
                });
            }
            catch { return null; }
        }

        private void StopProcess(ref Process p)
        {
            try
            {
                if (p != null && !p.HasExited)
                {
                    p.Kill();
                    p.WaitForExit(1500);
                }
            }
            catch { }
            p = null;
        }

        private int RunProcessWait(string exe, string args)
        {
            try
            {
                using (var p = Process.Start(new ProcessStartInfo
                {
                    FileName = exe,
                    Arguments = args,
                    WorkingDirectory = _appDir,
                    UseShellExecute = false,
                    CreateNoWindow = true,
                    WindowStyle = ProcessWindowStyle.Hidden
                }))
                {
                    p.WaitForExit(15000);
                    return p.ExitCode;
                }
            }
            catch { return -1; }
        }

        private void RunAdminRepairWithUac()
        {
            try
            {
                var p = Process.Start(new ProcessStartInfo
                {
                    FileName = Path.Combine(_appDir, "XiaobaoDesk.exe"),
                    Arguments = "--repair-admin",
                    Verb = "runas",
                    UseShellExecute = true
                });
                if (p != null) p.WaitForExit();
                MessageBox.Show("防火墙规则、开机自启与虚拟副屏驱动修复完成！", "完成", MessageBoxButtons.OK, MessageBoxIcon.Information);
            }
            catch (Exception ex)
            {
                MessageBox.Show("未获得管理员授权或执行失败：" + ex.Message, "提示", MessageBoxButtons.OK, MessageBoxIcon.Warning);
            }
        }

        private void OpenLogs()
        {
            if (File.Exists(_hostLogPath))
            {
                Process.Start(new ProcessStartInfo(_hostLogPath) { UseShellExecute = true });
            }
            else
            {
                Process.Start(new ProcessStartInfo(_cfgDir) { UseShellExecute = true });
            }
        }

        private void ShowMainWindow()
        {
            ShowInTaskbar = true;
            Show();
            WindowState = FormWindowState.Normal;
            Activate();
        }

        protected override void OnFormClosing(FormClosingEventArgs e)
        {
            if (!_exiting && e.CloseReason == CloseReason.UserClosing)
            {
                e.Cancel = true;
                Hide();
                ShowInTaskbar = false;
                _trayIcon.ShowBalloonTip(2000, "XiaobaoDesk", "已最小化到右下角托盘继续运行，双击图标可重新打开。", ToolTipIcon.Info);
                return;
            }
            base.OnFormClosing(e);
        }

        private void ExitCompletely()
        {
            _exiting = true;
            _watchdogTimer.Stop();
            lock (_procLock)
            {
                StopProcess(ref _hostProc);
                StopProcess(ref _hubProc);
                KillMediaCoreFromAppDir();
            }
            ForceSinglePrimaryScreenIfVirtual();
            _trayIcon.Visible = false;
            _trayIcon.Dispose();
            Application.Exit();
        }
    }
}
