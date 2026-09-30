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

	"github.com/joho/godotenv"
	"github.com/kardianos/service"
	"github.com/robfig/cron/v3"
	"github.com/xuri/excelize/v2"
	"gopkg.in/mail.v2"
)

// AppEvent Common structure for both endpoints (flexible)
type AppEvent struct {
	Timestamp string `json:"timestamp"`          // ISO format, e.g. "2026-02-28T17:45:00+07:00"
	AppName   string `json:"appName"`            // human-readable name
	Package   string `json:"package,omitempty"`  // optional package name
	Duration  int64  `json:"duration,omitempty"` // only for opened-long (milliseconds)
}

var (
	subject       = "Bin Daily activities"
	excelMutex    sync.Mutex
	categoryCache = map[string]string{}
	cacheMutex    sync.RWMutex
	// cron schedule for 19:00 daily (no leading space)
	sendTime     = "0 19 * * *"
	activeFile   string
	liveTemplate = "bin_daily.xlsx"
	sheetName    = "Events"
	dataDir      string
	listenAddr   = ":8080"
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
		return nil, fmt.Errorf("APP_PASSWORD, TO_MAIL, and FROM_MAIL must be set in .env.example (next to the executable) or the process environment")
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
	if _, err := cronb.AddFunc("@every 5m", func() {
		now := time.Now().In(loc)
		log.Printf("Cron triggered at %s — should send daily report", now.Format("2006-01-02 15:04:05 MST"))

		fileToSend := activeFile
		if fileToSend == "" {
			log.Println("ERROR: activeFile is empty!")
			return
		}
		if !fileExists(fileToSend) {
			log.Printf("ERROR: file not found: %s", fileToSend)
			return
		}

		log.Printf("Attempting to send: %s", fileToSend)
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
	mux.HandleFunc("/app-uninstalled", handleAppUnistall)
	p.httpServer = &http.Server{Addr: listenAddr, Handler: mux}
	return nil
}

func (p *program) Stop(s service.Service) error {
	log.Println("service stopping")
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

Put .env.example next to the executable. Logs are written to cathy.log in that same folder.
`, serviceDisplayName)
}

func main() {
	exeDir, err := executableDir()
	if err != nil {
		log.Fatalf("cannot resolve executable directory: %v", err)
	}
	// Windows services start in C:\Windows\System32. `go run` puts the binary
	// in a temp dir, so only force the exe directory when SCM is launching us
	// or when .env.example actually sits next to the binary.
	if !service.Interactive() {
		if err := os.Chdir(exeDir); err != nil {
			log.Fatalf("cannot change working directory to %s: %v", exeDir, err)
		}
	} else if _, err := os.Stat(".env.example"); err != nil {
		if _, err := os.Stat(filepath.Join(exeDir, ".env.example")); err == nil {
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

type UninstallEvent struct {
	Reason    string `json:"reason"`
	Timestamp string `json:"timestamp"`
}

func getTodayFileName() string {
	dateStr := time.Now().In(time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)).Format("2006-01-02")
	return fmt.Sprintf("%s/bin_%s.xlsx", dataDir, dateStr)
}

func todayFileExists() error {
	excelMutex.Lock()
	defer excelMutex.Unlock()

	if activeFile != "" && fileExists(activeFile) {
		return nil // already good
	}

	todayFile := getTodayFileName()

	// If today's file already exists → just use it
	if fileExists(todayFile) {
		activeFile = todayFile
		log.Printf("Using existing daily file: %s", activeFile)
		return nil
	}

	// Create new file
	f := excelize.NewFile()
	defer f.Close()

	// Rename default sheet
	if err := f.SetSheetName("Sheet1", sheetName); err != nil {
		return fmt.Errorf("failed to set sheet name: %w", err)
	}

	// Write headers - row 1, columns A to F
	headers := []string{
		"Timestamp",
		"App Name",
		"Package",
		"Category",
		"Duration",
		"Event Type",
	}

	for colIdx, header := range headers {
		// column number starts at 1 (A=1, B=2, ...)
		cell, err := excelize.CoordinatesToCellName(colIdx+1, 1)
		if err != nil {
			return fmt.Errorf("failed to get cell name for col %d, row 1: %w", colIdx+1, err)
		}

		if err := f.SetCellValue(sheetName, cell, header); err != nil {
			return fmt.Errorf("failed to set header %q at %s: %w", header, cell, err)
		}
	}

	// Header style (apply once for row 1)
	style, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{
			Type:    "pattern",
			Color:   []string{"#D9E1F2"},
			Pattern: 1,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create header style: %w", err)
	}

	if err := f.SetRowStyle(sheetName, 1, 1, style); err != nil {
		return fmt.Errorf("failed to apply header style to row 1: %w", err)
	}

	// Column widths
	for _, col := range []string{"A", "B", "C", "D", "E", "F"} {
		if err := f.SetColWidth(sheetName, col, col, 30); err != nil {
			return fmt.Errorf("failed to set column width %s: %w", col, err)
		}
	}

	// Save
	if err := f.SaveAs(todayFile); err != nil {
		return fmt.Errorf("failed to save new file %s: %w", todayFile, err)
	}

	activeFile = todayFile
	log.Printf("Created new daily file: %s", activeFile)

	return nil
}

func handleAppUnistall(w http.ResponseWriter, r *http.Request) {
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

// clearExcelData removes all data rows from the spreadsheet but keeps the header
func clearExcelData() error {
	excelMutex.Lock()
	defer excelMutex.Unlock()
	if _, err := os.Stat(liveTemplate); os.IsNotExist(err) {
		return fmt.Errorf("livetemplate  does not exist, create it %s", liveTemplate)
	}
	return nil
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

// Handler for new app install
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

	var event AppEvent
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	log.Printf("[INSTALLED] %s → App: %s (%s)",
		formatDuration(event.Timestamp), event.AppName, event.Package)
	err = saveItToExcel(event, "installed")
	if err != nil {
		log.Printf("save it got -> %s", err)
	}
	// TODO: Save to database, send notification, etc.

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

// Handler for long-opened app
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

	var event AppEvent
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	log.Printf("[OPENED_LONG] %s → App: %s (%s), Duration: %d ms (~%s min)",
		formatDuration(event.Timestamp), event.AppName, event.Package, event.Duration, durationOpend(event.Duration))

	// TODO: Save to database, send notification, etc.
	err = saveItToExcel(event, "Opening long")
	if err != nil {
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
		return fmt.Errorf("no file to send ") // or send empty email — your choice
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
	// Social
	"com.twitter.android":      "Social",
	"com.facebook.katana":      "Social",
	"com.instagram.android":    "Social",
	"com.zhiliaoapp.musically": "Social", // TikTok
	"com.instagram.barcelona":  "thread",

	// Games
	"com.supercell.clashofclans":              "Game",
	"com.mojang.minecraftpe":                  "Game",
	"com.dts.freefireth":                      "Game",
	"com.dts.freefiremax":                     "Game",
	"com.riotgames.league.wildriftvn":         "Game",
	"com.garena.game.kgvn":                    "Game",
	"com.roblox.client":                       "Game",
	"com.roblox.client.vnggames":              "Game",
	"com.riotgames.league.teamfighttacticsvn": "Game",
	"com.riotgames.league.teamfighttactics":   "Game",
	// Video
	"com.google.android.youtube": "Video",
	"com.netflix.mediaclient":    "Video",

	// Browser
	"com.microsoft.emmx":  "Browser",
	"com.android.chrome":  "Browser",
	"org.mozilla.firefox": "Browser",

	// Shopping
	"com.shopee.vn":           "Shopping",
	"vn.tiki.app.tikiandroid": "Shopping",
}

func getCategory(packageName string) string {
	if cat, ok := packageCategories[packageName]; ok {
		return cat
	}
	return "other"
}

func initFile() error {
	if _, err := os.Stat(liveTemplate); os.IsNotExist(err) {
		f := excelize.NewFile()
		err := f.SetSheetName("Sheet1", sheetName)
		if err != nil {
			return fmt.Errorf("cannnot create sheet: %v", err)
		}
		headers := []string{"Timestamp", "App Name", "Package", "Category", "Duration", "Event Type"}
		for i, h := range headers {
			cell, _ := excelize.CoordinatesToCellName(i+1, 1)
			err := f.SetCellValue(sheetName, cell, h)
			if err != nil {
				return fmt.Errorf("cannnot create cell: %v", err)
			}
		}

		// Style headers bold
		style, _ := f.NewStyle(&excelize.Style{
			Font: &excelize.Font{Bold: true},
			Fill: excelize.Fill{
				Type:    "pattern",
				Color:   []string{"#D9E1F2"},
				Pattern: 1,
			},
		})
		err = f.SetRowStyle(sheetName, 1, 1, style)
		if err != nil {
			return fmt.Errorf("cannnot create row style: %v", err)
		}
		mapSheet := map[string]string{
			"A": "A",
			"B": "B",
			"C": "C",
			"D": "D",
			"E": "E",
			"F": "F",
		}

		for k, v := range mapSheet {
			if err = f.SetColWidth(sheetName, k, v, 30); err != nil {
				return fmt.Errorf("cannnot create col width: %v", err)
			}
		}
		if err := f.SaveAs(liveTemplate); err != nil {
			return fmt.Errorf("Failed to create Excel file: %v", err)
		}

		return nil
	}
	return nil
}

func saveItToExcel(event AppEvent, eventType string) error {
	excelMutex.Lock()
	defer excelMutex.Unlock()

	f, err := excelize.OpenFile(activeFile)
	if err != nil {
		return fmt.Errorf("open file %s failed: %w", activeFile, err)
	}
	defer f.Close()

	// Make sure sheet exists

	// Get number of rows safely
	rows, err := f.GetRows(sheetName)
	if err != nil {
		return fmt.Errorf("GetRows failed on %q: %w", sheetName, err)
	}

	nextRow := len(rows) + 1
	if nextRow < 2 {
		nextRow = 2 // force start from row 2 (after header)
	}

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

	// Column A=1, B=2, ..., F=6
	if len(values) > 6 {
		return fmt.Errorf("too many values (%d) - max 6 columns supported", len(values))
	}

	for colIdx, value := range values {
		column := colIdx + 1 // 1-based
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
