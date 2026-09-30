# Cathy Activity Tracker — Backend

Go HTTP server that:

- Receives real-time events from the Android app (app install, long-opened sessions, uninstalls)
- Saves events into **daily Excel files** (one file per day)
- Runs as a **Windows 10 service** so it starts with the PC and keeps listening in the background

## Features
- Daily Excel file: `data/bin_YYYY-MM-DD.xlsx`
- Columns: Timestamp · App Name · Package · Category · Duration · Event Type
- Automatic folder creation: `data/`
- Categories for popular apps (Social, Game, Video, Browser, Shopping, ...)
- Gmail SMTP email report (Asia/Ho_Chi_Minh)
- Important: use a Gmail **App Password**, not your normal password

## Windows 10 service

The process listens on port **8080**. Put `.env` next to `cathy.exe`. Logs go to `cathy.log` in that same folder.

### 1. Build the Windows binary

From this repo (Linux or Windows):

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o cathy.exe .
```

On a Windows machine with Go installed:

```powershell
go build -o cathy.exe .
```

### 2. Copy to the PC

Copy these files into one folder, for example `C:\Cathy\`:

- `cathy.exe`
- `.env` (see `.env`)
- `install-windows-service.ps1` (optional helper)

`.env` must contain:

```
APP_PASSWORD=your-gmail-app-password
TO_MAIL=someone@example.com
FROM_MAIL=yourgmail@gmail.com
```

### 3. Install (Administrator)

Open PowerShell **as Administrator**, `cd` to the folder, then either:

```powershell
.\install-windows-service.ps1
```

or:

```powershell
.\cathy.exe install
.\cathy.exe start
```

The helper script also opens Windows Firewall for inbound TCP 8080.

The service name is `Cathy`. It starts automatically at boot and restarts if it crashes.

### 4. Useful commands

```powershell
.\cathy.exe status
.\cathy.exe stop
.\cathy.exe start
.\cathy.exe restart
.\cathy.exe uninstall
```

Or: `services.msc` → **Cathy Activity Tracker**.

To test without installing a service, run `.\cathy.exe` in a console (Ctrl+C stops it).

The Android app must call this PC's **LAN IP** (not `localhost`), e.g. `http://192.168.1.20:8080`.

## API Endpoints

| Method | Endpoint                        | Description                          | Expected JSON Body                          |
|--------|----------------------------------|--------------------------------------|---------------------------------------------|
| POST   | `/api/app-installed`            | New app installed                    | `{ "timestamp": "...", "appName": "...", "package": "..." }` |
| POST   | `/api/app-opened-long`          | App was opened for long time         | `{ "timestamp": "...", "appName": "...", "package": "...", "duration": 123456 }` (duration in ms) |
All endpoints return:
```json
{ "status": "success", ... }
```