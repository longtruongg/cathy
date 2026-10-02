package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/hashicorp/mdns"
	"github.com/joho/godotenv"
	"github.com/kardianos/service"
	"github.com/robfig/cron/v3"
	"github.com/xuri/excelize/v2"
	"gopkg.in/mail.v2"
)

type AppOpenedLong struct {
	Timestamp    string `json:"timestamp"`
	AppName      string `json:"appName"`
	Package      string `json:"package"`
	Duration     int64  `json:"duration"`
	SessionStart int64  `json:"sessionStart"`
	DeviceID     string `json:"deviceId"`
}

type PackageChange struct {
	Timestamp string `json:"timestamp"`
	AppName   string `json:"appName"`
	Package   string `json:"package"`
	DeviceID  string `json:"deviceId"`
}

type UninstallEvent struct {
	Reason    string `json:"reason"`
	Timestamp string `json:"timestamp"`
}
type GameSession struct {
	AppName      string `json:"appName"`
	Package      string `json:"package"`
	SessionStart int64  `json:"sessionStart"`
	SessionEnd   int64  `json:"sessionEnd"`
	Duration     int64  `json:"duration"`
	DeviceID     string `json:"deviceId"`
}

var (
	subject       = "Bin Daily activities"
	excelMutex    sync.Mutex
	categoryCache = map[string]string{}
	cacheMutex    sync.RWMutex
	// cron: minute hour day month weekday → every day at 19:45
	sendTime   = "40 20 * * *" // 20:40pm
	activeFile string
	sheetName  = "Events"
	dataDir    string
	listenAddr = ":8080"
)

type Config struct {
	AppPassword, ToMail, FromMail string
}

func loadConfig() (*Config, error) {
	_ = godotenv.Load()
	cfg := Config{
		AppPassword: os.Getenv("APP_PASSWORD"),
		ToMail:      os.Getenv("TO_MAIL"),
		FromMail:    os.Getenv("FROM_MAIL"),
	}
	if cfg.AppPassword == "" || cfg.ToMail == "" || cfg.FromMail == "" {
		return nil, fmt.Errorf("APP_PASSWORD, TO_MAIL, and FROM_MAIL must be set in .env (next to the executable) or the process environment")
	}
	return &cfg, nil
}

const (
	serviceName        = "Cathy"
	serviceDisplayName = "Cathy Activity Tracker"
	serviceDescription = "Receives Android app activity events and emails a daily Excel report"
)

type program struct {
	cfg        *Config
	httpServer *http.Server
	cron       *cron.Cron
	logFile    *os.File
	mdns       *mdns.Server
}

func executableDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return filepath.Dir(exe), nil
	}
	return filepath.Dir(resolved), nil
}

func setupFileLog() (*os.File, error) {
	f, err := os.OpenFile("cathy.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	log.SetOutput(io.MultiWriter(os.Stdout, f))
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	return f, nil
}

func (p *program) Start(s service.Service) error {
	if err := p.setup(); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		if p.cron != nil {
			p.cron.Stop()
		}
		return fmt.Errorf("listen %s: %w", listenAddr, err)
	}
	if srv, err := advertise(ln.Addr().(*net.TCPAddr).Port); err != nil {
		log.Printf("mdns advertise failed: %v", err)
	} else {
		p.mdns = srv
	}
	go func() {
		log.Printf("Server listening on http://%s", listenAddr)
		if err := p.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("http server error: %v", err)
		}
	}()
	return nil
}

func (p *program) setup() error {
	logFile, err := setupFileLog()
	if err != nil {
		log.Printf("cannot open cathy.log: %v (continuing with stdout only)", err)
	} else {
		p.logFile = logFile
	}

	dir, err := ensureDataDir()
	if err != nil {
		return err
	}
	dataDir = dir

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	p.cfg = cfg

	if err := todayFileExists(); err != nil {
		log.Printf("todayFileExists err: %v", err)
	}

	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		return fmt.Errorf("load timezone Asia/Ho_Chi_Minh: %w", err)
	}

	cronb := cron.New(cron.WithLocation(loc))
	if _, err := cronb.AddFunc(sendTime, func() {
		now := time.Now().In(loc)
		log.Printf("Cron triggered at %s — sending daily report", now.Format("2006-01-02 15:04:05 MST"))

		if err := sendJobDaily(p.cfg); err != nil {
			log.Printf("sendJobDaily failed: %v", err)
		} else {
			log.Println("Email sent successfully")
		}
	}); err != nil {
		return fmt.Errorf("failed to schedule cron job: %w", err)
	}
	cronb.Start()
	p.cron = cronb

	mux := http.NewServeMux()
	mux.HandleFunc("/api/app-installed", handleAppInstalled)
	mux.HandleFunc("/api/app-opened-long", handleAppOpenedLong)
	mux.HandleFunc("/api/app-uninstalled", handleAppUninstall)
	mux.HandleFunc("/app-uninstalled", handleAppUninstall) // back-compat alias
	mux.HandleFunc("/api/game-session", handleGameSession)
	p.httpServer = &http.Server{Addr: listenAddr, Handler: mux}
	return nil
}

func handleGameSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	defer r.Body.Close()

	var s GameSession
	if err := json.Unmarshal(body, &s); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	start := time.UnixMilli(s.SessionStart).In(time.FixedZone("GMT+7", 7*3600))
	end := time.UnixMilli(s.SessionEnd).In(time.FixedZone("GMT+7", 7*3600))
	log.Printf("[GAME_SESSION] %s: %s → %s (%s)",
		s.AppName, start.Format("15:04:05"), end.Format("15:04:05"), durationOpend(s.Duration))

	if err := todayFileExists(); err != nil {
		log.Printf("todayFileExists err: %v", err)
	}
	if err := saveGameSession(s, start, end); err != nil {
		log.Printf("save game session err: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"success"}`)
}

func saveGameSession(s GameSession, start time.Time, end time.Time) interface{} {
	excelMutex.Lock()
	defer excelMutex.Unlock()

	f, err := excelize.OpenFile(activeFile)
	if err != nil {
		return err
	}
	defer f.Close()

	const sheet = "GameSessions"
	sheetIdx, _ := f.GetSheetIndex(sheet)
	if _, err := f.GetSheetIndex(sheet); err != nil || sheetIdx == -1 {
		f.NewSheet(sheet)
		f.SetSheetRow(sheet, "A1", &[]string{"App", "Package", "Start", "Stop", "Duration"})
	}

	rows, _ := f.GetRows(sheet)
	nextRow := len(rows) + 1
	cell, _ := excelize.CoordinatesToCellName(1, nextRow)
	row := []interface{}{
		s.AppName, s.Package,
		start.Format("15:04:05"), end.Format("15:04:05"),
		durationOpend(s.Duration),
	}
	f.SetSheetRow(sheet, cell, &row)
	return f.Save()
}

func (p *program) Stop(s service.Service) error {
	log.Println("service stopping")
	if p.mdns != nil {
		if err := p.mdns.Shutdown(); err != nil {
			log.Printf("mdns shutdown: %v", err)
		}
	}
	if p.cron != nil {
		cronCtx := p.cron.Stop()
		select {
		case <-cronCtx.Done():
		case <-time.After(8 * time.Second):
			log.Println("timed out waiting for cron jobs")
		}
	}
	if p.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := p.httpServer.Shutdown(ctx); err != nil {
			log.Printf("http shutdown: %v", err)
		}
	}
	log.Println("service stopped")
	if p.logFile != nil {
		_ = p.logFile.Close()
	}
	return nil
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `%s

Usage:
  cathy.exe              Run in the foreground (or as a Windows service when started by SCM)
  cathy.exe install      Install as a Windows service (run as Administrator)
  cathy.exe uninstall    Remove the Windows service
  cathy.exe start        Start the service
  cathy.exe stop         Stop the service
  cathy.exe restart      Restart the service
  cathy.exe status       Show service status

Put .env next to the executable. Logs are written to cathy.log in that same folder.
`, serviceDisplayName)
}

func main() {
	exeDir, err := executableDir()
	if err != nil {
		log.Fatalf("cannot resolve executable directory: %v", err)
	}
	if !service.Interactive() {
		if err := os.Chdir(exeDir); err != nil {
			log.Fatalf("cannot change working directory to %s: %v", exeDir, err)
		}
	} else if _, err := os.Stat(".env"); err != nil {
		if _, err := os.Stat(filepath.Join(exeDir, ".env")); err == nil {
			if err := os.Chdir(exeDir); err != nil {
				log.Fatalf("cannot change working directory to %s: %v", exeDir, err)
			}
		}
	}

	svcConfig := &service.Config{
		Name:             serviceName,
		DisplayName:      serviceDisplayName,
		Description:      serviceDescription,
		WorkingDirectory: exeDir,
		Option: service.KeyValue{
			"StartType":              "automatic",
			"OnFailure":              "restart",
			"OnFailureDelayDuration": "5s",
		},
	}

	prg := &program{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		log.Fatalf("cannot create service: %v", err)
	}

	if len(os.Args) > 1 {
		cmd := os.Args[1]
		switch cmd {
		case "install", "uninstall", "start", "stop", "restart":
			if err := service.Control(s, cmd); err != nil {
				log.Fatalf("%s failed: %v", cmd, err)
			}
			log.Printf("%s %s succeeded", serviceName, cmd)
			return
		case "status":
			st, err := s.Status()
			if err != nil {
				log.Fatalf("status failed: %v", err)
			}
			switch st {
			case service.StatusRunning:
				fmt.Println("running")
			case service.StatusStopped:
				fmt.Println("stopped")
			default:
				fmt.Println("unknown")
			}
			return
		case "-h", "-help", "--help", "help":
			printUsage()
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
			printUsage()
			os.Exit(2)
		}
	}

	if err := s.Run(); err != nil {
		log.Fatalf("service run failed: %v", err)
	}
}

func getTodayFileName() string {
	dateStr := time.Now().In(time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)).Format("2006-01-02")
	return fmt.Sprintf("%s/bin_%s.xlsx", dataDir, dateStr)
}

// todayFileExists makes sure activeFile points at TODAY's file, not just any
// file that happens to still be on disk. Fixes the bug where activeFile,
// once set, never rotated past the day it was created.
func todayFileExists() error {
	excelMutex.Lock()
	defer excelMutex.Unlock()

	todayFile := getTodayFileName()

	if activeFile == todayFile && fileExists(activeFile) {
		return nil // already on today's file
	}

	if fileExists(todayFile) {
		activeFile = todayFile
		log.Printf("Using existing daily file: %s", activeFile)
		return nil
	}

	f := excelize.NewFile()
	defer f.Close()

	if err := f.SetSheetName("Sheet1", sheetName); err != nil {
		return fmt.Errorf("failed to set sheet name: %w", err)
	}

	headers := []string{"Timestamp", "App Name", "Package", "Category", "Duration", "Event Type"}
	for colIdx, header := range headers {
		cell, err := excelize.CoordinatesToCellName(colIdx+1, 1)
		if err != nil {
			return fmt.Errorf("failed to get cell name for col %d, row 1: %w", colIdx+1, err)
		}
		if err := f.SetCellValue(sheetName, cell, header); err != nil {
			return fmt.Errorf("failed to set header %q at %s: %w", header, cell, err)
		}
	}

	style, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#D9E1F2"}, Pattern: 1},
	})
	if err != nil {
		return fmt.Errorf("failed to create header style: %w", err)
	}
	if err := f.SetRowStyle(sheetName, 1, 1, style); err != nil {
		return fmt.Errorf("failed to apply header style to row 1: %w", err)
	}

	for _, col := range []string{"A", "B", "C", "D", "E", "F"} {
		if err := f.SetColWidth(sheetName, col, col, 30); err != nil {
			return fmt.Errorf("failed to set column width %s: %w", col, err)
		}
	}

	if err := f.SaveAs(todayFile); err != nil {
		return fmt.Errorf("failed to save new file %s: %w", todayFile, err)
	}

	activeFile = todayFile
	log.Printf("Created new daily file: %s", activeFile)
	return nil
}

func handleAppUninstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	body, _ := io.ReadAll(r.Body)
	defer r.Body.Close()

	var event UninstallEvent
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	formatted := formatDuration(event.Timestamp)
	log.Printf("[UNINSTALL_REASON] %s → Reason: %s", formatted, event.Reason)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"success"}`)
}

func ensureDataDir() (string, error) {
	dir := "data"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("cannot create data directory: %w", err)
	}
	return dir, nil
}

func formatDuration(d string) string {
	loc := time.FixedZone("GMT+7", 7*60*60)
	t, err := time.Parse(time.RFC3339Nano, d)
	if err != nil {
		t = time.Now()
	}
	return t.In(loc).Format("2-1-2006 15:04:05 GMT+7")
}

// handleAppInstalled also receives uninstall-shaped payloads from the Kotlin
// side (PackageChangePayload: no duration field) — Duration/SessionStart
// just come through as 0, which is fine for the "installed"/"uninstalled" rows.
func handleAppInstalled(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Cannot read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var event AppOpenedLong
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	log.Printf("[INSTALLED] %s → App: %s (%s)",
		formatDuration(event.Timestamp), event.AppName, event.Package)

	if err := todayFileExists(); err != nil {
		log.Printf("todayFileExists err: %v", err)
	}
	if err := saveItToExcel(event, "installed"); err != nil {
		log.Printf("save it got -> %s", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"success","event_type":"installed"}`)
}

func durationOpend(x int64) string {
	durationSec := x / 1000
	durationMin := durationSec / 60
	durationHour := durationMin / 60

	var durationStr string
	if durationHour > 0 {
		remainMin := durationMin % 60
		durationStr = fmt.Sprintf("%dh %dm", durationHour, remainMin)
	} else {
		durationStr = fmt.Sprintf("%dm", durationMin)
	}
	return durationStr
}

func handleAppOpenedLong(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Cannot read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var event AppOpenedLong
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	log.Printf("[OPENED_LONG] %s → App: %s (%s), Duration: %d ms (~%s min)",
		formatDuration(event.Timestamp), event.AppName, event.Package, event.Duration, durationOpend(event.Duration))

	if err := todayFileExists(); err != nil {
		log.Printf("todayFileExists err: %v", err)
	}
	if err := saveItToExcel(event, "Opening long"); err != nil {
		log.Printf("save it got err %s", err.Error())
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"success","event_type":"opened_long"}`)
}

func sendJobDaily(cfg *Config) error {
	if err := todayFileExists(); err != nil {
		return fmt.Errorf("no file exists %s", err)
	}
	excelMutex.Lock()
	fileToSend := activeFile
	excelMutex.Unlock()

	if !fileExists(fileToSend) {
		log.Printf("No data file to send today (%s)", fileToSend)
		return fmt.Errorf("no file to send")
	}

	msg := mail.NewMessage()
	msg.SetHeader("From", cfg.FromMail)
	msg.SetHeader("To", cfg.ToMail)
	msg.SetHeader("Subject", fmt.Sprintf("%s - %s", subject, time.Now().Format("2006-01-02")))
	msg.SetBody("text/plain", "Daily activities attached.")
	msg.Attach(fileToSend)

	dialer := mail.NewDialer("smtp.gmail.com", 587, cfg.FromMail, cfg.AppPassword)
	if err := dialer.DialAndSend(msg); err != nil {
		return fmt.Errorf("cannot send email: %w", err)
	}

	log.Printf("Sent daily report: %s", fileToSend)
	return nil
}

var packageCategories = map[string]string{
	"com.twitter.android":             "Social",
	"com.facebook.katana":             "Facebook",
	"com.instagram.android":           "Instagram",
	"com.zhiliaoapp.musically":        "Tik Tok",
	"com.instagram.barcelona":         "Thread",
	"com.ss.android.ugc.aweme":        "Douyin",
	"com.ss.android.ugc.aweme.mobile": "Douyin",

	"com.supercell.clashofclans":              "Class of Clan",
	"com.mojang.minecraftpe":                  "Minecraft",
	"com.dts.freefireth":                      "Free Fire Game",
	"com.dts.freefiremax":                     "Free Fire Game",
	"com.riotgames.league.wildriftvn":         "LMHT: Toc Chien",
	"com.garena.game.kgvn":                    "Garena Lien Quan Mobile",
	"com.roblox.client":                       "Roblox Game",
	"com.roblox.client.vnggames":              "Roblox Viet Nam",
	"com.riotgames.league.teamfighttacticsvn": "Đấu Trường Chân Lý",
	"com.riotgames.league.teamfighttactics":   "Đấu Trường Chân Lý",

	"com.google.android.youtube": "Youtube",
	"com.netflix.mediaclient":    "Netflix",

	"com.microsoft.emmx":  "Browser",
	"com.android.chrome":  "Chrome",
	"org.mozilla.firefox": "Firefox",

	"com.shopee.vn":           "Shopee",
	"vn.tiki.app.tikiandroid": "Tik Tok shop",
}

func getCategory(packageName string) string {
	cacheMutex.RLock()
	if cat, ok := categoryCache[packageName]; ok {
		cacheMutex.RUnlock()
		return cat
	}
	cacheMutex.RUnlock()

	cat, ok := packageCategories[packageName]
	if !ok {
		cat = packageName //search by hands
	}
	cacheMutex.Lock()
	categoryCache[packageName] = cat
	cacheMutex.Unlock()
	return cat
}

func saveItToExcel(event AppOpenedLong, eventType string) error {
	excelMutex.Lock()
	defer excelMutex.Unlock()

	f, err := excelize.OpenFile(activeFile)
	if err != nil {
		return fmt.Errorf("open file %s failed: %w", activeFile, err)
	}
	defer f.Close()

	rows, err := f.GetRows(sheetName)
	if err != nil {
		return fmt.Errorf("GetRows failed on %q: %w", sheetName, err)
	}

	nextRow := max(len(rows)+1, 2)

	category := getCategory(event.Package)
	durStr := durationOpend(event.Duration)

	values := []string{
		event.Timestamp,
		event.AppName,
		event.Package,
		category,
		durStr,
		eventType,
	}

	for colIdx, value := range values {
		column := colIdx + 1
		cellName, err := excelize.CoordinatesToCellName(column, nextRow)
		if err != nil {
			return fmt.Errorf("invalid coordinates col=%d row=%d: %w", column, nextRow, err)
		}
		if err := f.SetCellValue(sheetName, cellName, value); err != nil {
			return fmt.Errorf("SetCellValue failed at %s (value=%v): %w", cellName, value, err)
		}
	}

	if err := f.Save(); err != nil {
		return fmt.Errorf("save file %s failed: %w", activeFile, err)
	}

	log.Printf("Saved event to %s row %d", activeFile, nextRow)
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
