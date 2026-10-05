package main

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/xuri/excelize/v2"
)

func MuxHandler() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/app-installed", handleAppInstalled)
	mux.HandleFunc("/api/app-opened-long", handleAppOpenedLong)
	mux.HandleFunc("/api/app-uninstalled", handleAppUninstall)
	mux.HandleFunc("/app-uninstalled", handleAppUninstall) // back-compat alias
	mux.HandleFunc("/api/game-session", handleGameSession)
	return mux
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

func saveGameSession(s GameSession, start time.Time, end time.Time) error {
	excelMutex.Lock()
	defer excelMutex.Unlock()

	f, err := excelize.OpenFile(activeFile)
	if err != nil {
		return err
	}
	defer f.Close()

	const sheet = "GameSessions"
	sheetIdx, err := f.GetSheetIndex(sheet)
	if err != nil || sheetIdx == -1 {
		f.NewSheet(sheet)
		_ = f.SetSheetRow(sheet, "A1", &[]string{"App", "Package", "Start", "Stop", "Duration"})
	}

	rows, _ := f.GetRows(sheet)
	nextRow := len(rows) + 1
	cell, _ := excelize.CoordinatesToCellName(1, nextRow)
	row := []interface{}{
		s.AppName, s.Package,
		start.Format("15:04:05"), end.Format("15:04:05"),
		durationOpend(s.Duration),
	}
	_ = f.SetSheetRow(sheet, cell, &row)
	return f.Save()
}

func handleAppUninstall(w http.ResponseWriter, r *http.Request) {
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

	// Android sends PackageChangePayload (same shape as install).
	var event PackageChange
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	log.Printf("[UNINSTALLED] %s → App: %s (%s)",
		formatDuration(event.Timestamp), event.AppName, event.Package)

	if err := todayFileExists(); err != nil {
		log.Printf("todayFileExists err: %v", err)
	}
	row := AppOpenedLong{
		Timestamp: event.Timestamp,
		AppName:   event.AppName,
		Package:   event.Package,
		DeviceID:  event.DeviceID,
	}
	if err := saveItToExcel(row, "uninstalled"); err != nil {
		log.Printf("save uninstall err: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"success","event_type":"uninstalled"}`)
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
