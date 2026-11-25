using System;
using System.Diagnostics;
using System.Drawing;
using System.IO;
using System.IO.Compression;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Windows.Forms;

namespace XiaobaoDeskSetup
{
    static class NativeMethods
    {
        [DllImport("user32.dll")]
        public static extern bool SetProcessDPIAware();
    }

    static class Program
    {
        private const string ResourceName = "XiaobaoDeskSetup.payload.zip";

        [STAThread]
        static int Main(string[] args)
        {
            for (int i = 0; i < args.Length; i++)
            {
                if (args[i] == "--verify-payload")
                {
                    return VerifyPayload();
                }
            }

            NativeMethods.SetProcessDPIAware();
            Application.EnableVisualStyles();
            Application.SetCompatibleTextRenderingDefault(false);
            Application.Run(new SetupForm());
            return 0;
        }

        private static int VerifyPayload()
        {
            using (var stream = Assembly.GetExecutingAssembly().GetManifestResourceStream(ResourceName))
            {
                if (stream == null)
                {
                    Console.Error.WriteLine("Embedded payload missing: " + ResourceName);
                    return 1;
                }
                using (var archive = new ZipArchive(stream, ZipArchiveMode.Read))
                {
                    Console.WriteLine("Verified embedded payload entries: " + archive.Entries.Count);
                    foreach (var e in archive.Entries)
                    {
                        Console.WriteLine(" - " + e.FullName + " (" + e.Length + " bytes)");
                    }
                }
            }
            return 0;
        }

        public static void ExtractPayload(string installDir)
        {
            using (var stream = Assembly.GetExecutingAssembly().GetManifestResourceStream(ResourceName))
            {
                if (stream == null) throw new InvalidOperationException("安装包内嵌组件缺失。");
                using (var archive = new ZipArchive(stream, ZipArchiveMode.Read))
                {
                    foreach (var entry in archive.Entries)
                    {
                        string destPath = Path.GetFullPath(Path.Combine(installDir, entry.FullName));
                        if (string.IsNullOrEmpty(entry.Name))
                        {
                            Directory.CreateDirectory(destPath);
                            continue;
                        }
                        Directory.CreateDirectory(Path.GetDirectoryName(destPath));
                        entry.ExtractToFile(destPath, true);
                    }
                }
            }
        }
    }

    sealed class SetupForm : Form
    {
        private TextBox _tbDir;
        private CheckBox _cbVdd;
        private CheckBox _cbShortcut;
        private Button _btnInstall;
        private Label _lblStatus;

        public SetupForm()
        {
            Text = "XiaobaoDesk v1.6.0 一键安装程序";
            Size = new Size(520, 320);
            StartPosition = FormStartPosition.CenterScreen;
            FormBorderStyle = FormBorderStyle.FixedDialog;
            MaximizeBox = false;
            Font = new Font("Microsoft YaHei UI", 9.5f);

            Controls.Add(new Label
            {
                Text = "XiaobaoDesk 电脑端一键安装（含被控端、内置服务端与无线副屏驱动）",
                Font = new Font("Microsoft YaHei UI", 10.5f, FontStyle.Bold),
                Location = new Point(20, 18),
                Size = new Size(470, 26)
            });

            Controls.Add(new Label { Text = "安装目录：", Location = new Point(20, 60), Size = new Size(80, 24) });
            _tbDir = new TextBox
            {
                Text = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ProgramFiles), "XiaobaoDesk"),
                Location = new Point(102, 57),
                Size = new Size(380, 24)
            };
            Controls.Add(_tbDir);

            _cbVdd = new CheckBox
            {
                Text = "自动配置防火墙 (21116/21118)、开机自启并安装无线副屏虚拟显示器驱动",
                Location = new Point(20, 100),
                Size = new Size(465, 24),
                Checked = true
            };
            Controls.Add(_cbVdd);

            _cbShortcut = new CheckBox
            {
                Text = "创建桌面快捷方式（XiaobaoDesk 控制台）",
                Location = new Point(20, 132),
                Size = new Size(465, 24),
                Checked = true
            };
            Controls.Add(_cbShortcut);

            _lblStatus = new Label
            {
                Text = "准备就绪。点击下方按钮开始安装。",
                Location = new Point(20, 174),
                Size = new Size(465, 24),
                ForeColor = Color.FromArgb(71, 85, 105)
            };
            Controls.Add(_lblStatus);

            _btnInstall = new Button
            {
                Text = "立即安装并打开控制台",
                Location = new Point(20, 212),
                Size = new Size(462, 42),
                BackColor = Color.FromArgb(37, 99, 235),
                ForeColor = Color.White,
                FlatStyle = FlatStyle.Flat,
                Font = new Font("Microsoft YaHei UI", 10.5f, FontStyle.Bold)
            };
            _btnInstall.Click += (s, e) => DoInstall();
            Controls.Add(_btnInstall);
        }

        private void DoInstall()
        {
            string dir = _tbDir.Text.Trim();
            if (string.IsNullOrEmpty(dir)) return;

            _btnInstall.Enabled = false;
            try
            {
                _lblStatus.Text = "正在停止旧版驻留进程并释放程序文件...";
                Application.DoEvents();
                StopRunningInDir(dir);
                Directory.CreateDirectory(dir);
                Program.ExtractPayload(dir);

                string appExe = Path.Combine(dir, "XiaobaoDesk.exe");
                if (_cbVdd.Checked && File.Exists(appExe))
                {
                    _lblStatus.Text = "正在配置防火墙、开机自启与无线副屏驱动（约 5 秒）...";
                    Application.DoEvents();
                    var p = Process.Start(new ProcessStartInfo
                    {
                        FileName = appExe,
                        Arguments = "--repair-admin",
                        WorkingDirectory = dir,
                        UseShellExecute = false,
                        CreateNoWindow = true,
                        WindowStyle = ProcessWindowStyle.Hidden
                    });
                    if (p != null) p.WaitForExit(60000);
                }

                if (_cbShortcut.Checked && File.Exists(appExe))
                {
                    CreateDesktopShortcut(appExe, dir);
                }

                if (File.Exists(appExe))
                {
                    Process.Start(new ProcessStartInfo("explorer.exe", string.Format("\"{0}\"", appExe)));
                }

                MessageBox.Show("安装完成！XiaobaoDesk 控制台已打开。", "安装成功", MessageBoxButtons.OK, MessageBoxIcon.Information);
                Close();
            }
            catch (Exception ex)
            {
                MessageBox.Show("安装过程中出错：\n" + ex.Message, "安装失败", MessageBoxButtons.OK, MessageBoxIcon.Error);
                _btnInstall.Enabled = true;
            }
        }

        private void StopRunningInDir(string dir)
        {
            string[] names = new string[] { "XiaobaoDesk", "xb-host", "xb-hub", "xb-media-core" };
            foreach (string name in names)
            {
                try
                {
                    foreach (var p in Process.GetProcessesByName(name))
                    {
                        try
                        {
                            if (p.MainModule != null && p.MainModule.FileName.StartsWith(dir, StringComparison.OrdinalIgnoreCase))
                            {
                                p.Kill();
                                p.WaitForExit(2000);
                            }
                        }
                        catch { }
                    }
                }
                catch { }
            }
        }

        private void CreateDesktopShortcut(string targetExe, string workDir)
        {
            try
            {
                string lnk = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.DesktopDirectory), "XiaobaoDesk.lnk");
                Type t = Type.GetTypeFromProgID("WScript.Shell");
                dynamic shell = Activator.CreateInstance(t);
                dynamic shortcut = shell.CreateShortcut(lnk);
                shortcut.TargetPath = targetExe;
                shortcut.WorkingDirectory = workDir;
                shortcut.Description = "XiaobaoDesk 远程桌面与无线副屏控制台";
                shortcut.Save();
            }
            catch { }
        }
    }
}
