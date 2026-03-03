#Wy Activity Tracker - Backend

Dummy Go HTTP server that:

- Receives real-time events from Android app (app install, long-opened sessions, uninstalls)
- Saves events into **daily Excel files** (one file per day) sends daily  at **19:00**

## Features
- Daily Excel file: `data/bin_YYYY-MM-DD.xlsx`
- Columns: Timestamp · App Name · Package · Category · Duration · Event Type
- Automatic folder creation: `/data/`
- Categories for popular apps (Social, Game, Video, Browser, Shopping, ...)
- Gmail SMTP email report every day at 19:00 (Asia/Ho_Chi_Minh)
- Important: Use App Password 
## API Endpoints

| Method | Endpoint                        | Description                          | Expected JSON Body                          |
|--------|----------------------------------|--------------------------------------|---------------------------------------------|
| POST   | `/api/app-installed`            | New app installed                    | `{ "timestamp": "...", "appName": "...", "package": "..." }` |
| POST   | `/api/app-opened-long`          | App was opened for long time         | `{ "timestamp": "...", "appName": "...", "package": "...", "duration": 123456 }` (duration in ms) |
All endpoints return:
```json
{ "status": "success", ... }