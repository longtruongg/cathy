package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/joho/godotenv"
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
	dataDir      = ensureDataDir()
)

type Config struct {
	AppPassword, ToMail, FromMail string
}

func loadConfig() (*Config, error) {
	var cfg Config
	if err := godotenv.Load(); err != nil {
		return nil, fmt.Errorf("Error loading .env file")
	}
	cfg.AppPassword = os.Getenv("APP_PASSWORD")
	cfg.ToMail = os.Getenv("TO_MAIL")
	cfg.FromMail = os.Getenv("FROM_MAIL")
	return &cfg, nil
}
func main() {
	loc, _ := time.LoadLocation("Asia/Ho_Chi_Minh")
	cronb := cron.New(cron.WithLocation(loc))
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("cannot load config from .env")
	}

	err = todayFileExists()
	if err != nil {
		log.Printf("todayFileExists err: %v", err)
	}
	// register the daily job and check for scheduling errors
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

		log.Printf("Attempting to send: %s  (size: ? bytes)", fileToSend)
		if err := sendJobDaily(cfg); err != nil {
			log.Printf("sendJobDaily failed: %v", err)
		} else {
			log.Println("Email sent successfully")
		}
	}); err != nil {
		log.Fatalf("failed to schedule cron job: %v", err)
	}
	// start scheduler in background
	cronb.Start()
	//defer cronb.Stop()

	// Endpoint 1: New app installed
	http.HandleFunc("/api/app-installed", handleAppInstalled)

	// Endpoint 2: App opened long time
	http.HandleFunc("/api/app-opened-long", handleAppOpenedLong)
	// send when Bin unistall
	http.HandleFunc("/app-uninstalled", handleAppUnistall)
	addr := ":8080"
	log.Printf("Server listening on http://%s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
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
func ensureDataDir() string {
	dir := "data"
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Fatalf("Cannot create data directory: %v", err)
	}
	return dir
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

	// Games
	"com.supercell.clashofclans": "Game",
	"com.mojang.minecraftpe":     "Game",

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
		var mapSheet = map[string]string{
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
