using System;
using System.Diagnostics;
using System.Drawing;
using System.IO;
using System.Threading;
using System.Windows.Forms;

namespace XiaobaoDeskTray
{
    static class Program
    {
        [STAThread]
        static void Main()
        {
            bool createdNew;
            using (var mutex = new Mutex(true, "Local\\XiaobaoDeskTraySingleton", out createdNew))
            {
                if (!createdNew)
                {
                    return;
                }
                Application.EnableVisualStyles();
                Application.SetCompatibleTextRenderingDefault(false);
                Application.Run(new TrayAppContext());
            }
        }
    }

    sealed class TrayAppContext : ApplicationContext
    {
        private readonly NotifyIcon _trayIcon;
        private readonly ToolStripMenuItem _statusItem;
        private readonly string _hostExe;
        private readonly string _configPath;
        private readonly string _logPath;
        private readonly string _workDir;
        private readonly object _procLock = new object();

        private Process _hostProc;
        private volatile bool _exiting;
        private readonly System.Windows.Forms.Timer _watchdogTimer;

        public TrayAppContext()
        {
            _workDir = AppDomain.CurrentDomain.BaseDirectory;
            _hostExe = Path.Combine(_workDir, "xb-host.exe");
            string userProfile = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
            string cfgDir = Path.Combine(userProfile, ".xiaobao-desk");
            Directory.CreateDirectory(cfgDir);
            _configPath = Path.Combine(cfgDir, "host.json");
            _logPath = Path.Combine(cfgDir, "host.log");

            _statusItem = new ToolStripMenuItem("状态: 正在启动...") { Enabled = false };
            var restartItem = new ToolStripMenuItem("重启被控服务", null, (s, e) => RestartHost());
            var openLogItem = new ToolStripMenuItem("查看运行日志", null, (s, e) => OpenLog());
            var displayItem = new ToolStripMenuItem("打开系统显示设置 (扩展屏)", null, (s, e) => OpenDisplaySettings());
            var exitItem = new ToolStripMenuItem("退出托盘并停止服务", null, (s, e) => ExitApp());

            var menu = new ContextMenuStrip();
            menu.Items.Add(_statusItem);
            menu.Items.Add(new ToolStripSeparator());
            menu.Items.Add(restartItem);
            menu.Items.Add(openLogItem);
            menu.Items.Add(displayItem);
            menu.Items.Add(new ToolStripSeparator());
            menu.Items.Add(exitItem);

            _trayIcon = new NotifyIcon
            {
                Icon = SystemIcons.Shield,
                Text = "XiaobaoDesk 被控端守护中",
                ContextMenuStrip = menu,
                Visible = true
            };
            _trayIcon.DoubleClick += (s, e) => OpenLog();

            EnsureHostRunning();

            _watchdogTimer = new System.Windows.Forms.Timer { Interval = 2500 };
            _watchdogTimer.Tick += (s, e) => EnsureHostRunning();
            _watchdogTimer.Start();
        }

        private void EnsureHostRunning()
        {
            if (_exiting) return;
            lock (_procLock)
            {
                if (_hostProc != null && !_hostProc.HasExited)
                {
                    _statusItem.Text = string.Format("状态: 运行中 (PID {0})", _hostProc.Id);
                    _trayIcon.Text = string.Format("XiaobaoDesk 运行中 (PID {0})", _hostProc.Id);
                    return;
                }

                if (!File.Exists(_hostExe))
                {
                    _statusItem.Text = "状态: 未找到 xb-host.exe";
                    return;
                }

                try
                {
                    var psi = new ProcessStartInfo
                    {
                        FileName = _hostExe,
                        Arguments = string.Format("--config \"{0}\" --log \"{1}\"", _configPath, _logPath),
                        WorkingDirectory = _workDir,
                        UseShellExecute = false,
                        CreateNoWindow = true,
                        WindowStyle = ProcessWindowStyle.Hidden
                    };
                    _hostProc = Process.Start(psi);
                    if (_hostProc != null)
                    {
                        _statusItem.Text = string.Format("状态: 运行中 (PID {0})", _hostProc.Id);
                        _trayIcon.Text = string.Format("XiaobaoDesk 运行中 (PID {0})", _hostProc.Id);
                    }
                }
                catch (Exception ex)
                {
                    _statusItem.Text = "启动失败: " + ex.Message;
                }
            }
        }

        private void RestartHost()
        {
            StopChildHost();
            EnsureHostRunning();
        }

        private void StopChildHost()
        {
            lock (_procLock)
            {
                try
                {
                    if (_hostProc != null && !_hostProc.HasExited)
                    {
                        _hostProc.Kill();
                        _hostProc.WaitForExit(2000);
                    }
                }
                catch { }
                _hostProc = null;
            }
            try
            {
                foreach (var p in Process.GetProcessesByName("xb-media-core"))
                {
                    try { p.Kill(); } catch { }
                }
            }
            catch { }
        }

        private void OpenLog()
        {
            try
            {
                if (File.Exists(_logPath))
                {
                    Process.Start(new ProcessStartInfo(_logPath) { UseShellExecute = true });
                }
            }
            catch { }
        }

        private void OpenDisplaySettings()
        {
            try
            {
                Process.Start(new ProcessStartInfo("ms-settings:display") { UseShellExecute = true });
            }
            catch { }
        }

        private void ExitApp()
        {
            _exiting = true;
            _watchdogTimer.Stop();
            StopChildHost();
            _trayIcon.Visible = false;
            _trayIcon.Dispose();
            Application.Exit();
        }
    }
}
