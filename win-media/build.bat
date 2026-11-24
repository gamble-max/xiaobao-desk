@echo off
setlocal
call "C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools\VC\Auxiliary\Build\vcvars64.bat" 10.0.22000.0
if errorlevel 1 exit /b 1

if not exist "%~dp0build" mkdir "%~dp0build"
if not exist "%~dp0..\dist" mkdir "%~dp0..\dist"

cl.exe /nologo /utf-8 /std:c++17 /O2 /EHsc /W4 /DUNICODE /D_UNICODE /DWIN32_LEAN_AND_MEAN /DNOMINMAX ^
  /Fo"%~dp0build\\" ^
  "%~dp0src\main.cpp" ^
  "%~dp0src\ipc_transport.cpp" ^
  "%~dp0src\capture_dxgi.cpp" ^
  "%~dp0src\encoder_mf.cpp" ^
  "%~dp0src\audio_wasapi.cpp" ^
  "%~dp0src\display_ctrl.cpp" ^
  "%~dp0src\input_ctrl.cpp" ^
  "%~dp0src\privacy_screen.cpp" ^
  /Fe:"%~dp0..\dist\xb-media-core.exe" ^
  /link ^
  d3d11.lib dxgi.lib mfplat.lib mfuuid.lib mf.lib evr.lib strmiids.lib ^
  ws2_32.lib winmm.lib user32.lib gdi32.lib dwmapi.lib advapi32.lib ole32.lib oleaut32.lib

if errorlevel 1 exit /b 1
echo Build succeeded: %~dp0..\dist\xb-media-core.exe
