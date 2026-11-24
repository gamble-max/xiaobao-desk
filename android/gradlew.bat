@echo off
setlocal
if not defined JAVA_HOME set "JAVA_HOME=C:\Program Files\Microsoft\jdk-17.0.12.7-hotspot"
if not defined GRADLE_USER_HOME set "GRADLE_USER_HOME=%~dp0..\.toolchains\.gradle-home"
set "GRADLE_BIN=%~dp0..\.toolchains\gradle-8.7\bin\gradle.bat"
if not exist "%GRADLE_BIN%" (
    echo Gradle 8.7 not found at "%GRADLE_BIN%"
    exit /b 1
)
call "%GRADLE_BIN%" %*
exit /b %ERRORLEVEL%
