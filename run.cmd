@echo off
chcp 65001 >nul
REM Go-Emby 复刻版 一键启动（Windows）
setlocal
if "%HTTP_PORT%"=="" set HTTP_PORT=8097
if "%MEDIA_ROOTS%"=="" set MEDIA_ROOTS=%~dp0media
if "%APP_DATA_PATH%"=="" set APP_DATA_PATH=%~dp0data
if "%ADMIN_PASSWORD%"=="" set ADMIN_PASSWORD=admin
echo 媒体目录: %MEDIA_ROOTS%
echo 数据目录: %APP_DATA_PATH%
echo 访问: http://127.0.0.1:%HTTP_PORT%/
"%~dp0go-emby.exe"
endlocal
