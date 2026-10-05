package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/xuri/excelize/v2"
)

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
