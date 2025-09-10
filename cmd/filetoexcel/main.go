package main

import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

const (
	sheetName       = "Zscore"
	onesColumnName  = "ones"
	blockColumnName = "samples"
	timeColumnName  = "time"
)

// DataRow represents a single input row with category label and ones count,
// plus computed cumulative mean and z-score.
type DataRow struct {
	Category       string
	Ones           int
	CumulativeMean float64
	ZScore         float64
}

// findInterval extracts the sampling interval in seconds from the file path.
// It looks for a segment matching `_i(\d+)` and returns the number.
func findInterval(filePath string) (int, error) {
	re := regexp.MustCompile(`_i(\d+)`)
	m := re.FindStringSubmatch(filePath)
	if len(m) < 2 {
		return 0, fmt.Errorf("interval not found in file name: %s", filepath.Base(filePath))
	}
	val, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, err
	}
	return val, nil
}

// findBitCount extracts the block bit count from the file path.
// It looks for a segment matching `_s(\d+)_i` and returns the number of bits per block.
func findBitCount(filePath string) (int, error) {
	re := regexp.MustCompile(`_s(\d+)_i`)
	m := re.FindStringSubmatch(filePath)
	if len(m) < 2 {
		return 0, fmt.Errorf("bit count not found in file name: %s", filepath.Base(filePath))
	}
	val, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, err
	}
	return val, nil
}

// readBinFile reads a .bin file and returns rows of (block number label, ones count).
// The block size is specified in bits; the function reads blockSize/8 bytes per block.
func readBinFile(filePath string, blockSize int) ([]DataRow, error) {
	if blockSize%8 != 0 {
		return nil, errors.New("block size must be a multiple of 8 bits for .bin files")
	}
	bytesPerBlock := blockSize / 8
	if bytesPerBlock <= 0 {
		return nil, errors.New("invalid block size")
	}
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	rows := make([]DataRow, 0, 1024)
	buf := make([]byte, bytesPerBlock)
	block := 1
	for {
		n, err := reader.Read(buf)
		if n == 0 {
			break
		}
		// Allow partial block at EOF; error only if it's not EOF
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		// Count ones in the read bytes (truncate if partial read)
		count := 0
		for i := 0; i < n; i++ {
			count += bits.OnesCount8(buf[i])
		}
		rows = append(rows, DataRow{Category: strconv.Itoa(block), Ones: count})
		block++
		if n < bytesPerBlock {
			break
		}
	}
	return rows, nil
}

// readCSVFile reads a .csv file with two columns: timestamp and ones count.
// It returns rows using the timestamp formatted as HH:MM:SS for the category label.
func readCSVFile(filePath string) ([]DataRow, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	// Expect no header; Python version used header=None
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	rows := make([]DataRow, 0, len(records))
	for _, rec := range records {
		if len(rec) < 2 {
			continue
		}
		label := formatTimeLabel(strings.TrimSpace(rec[0]))
		onesStr := strings.TrimSpace(rec[1])
		ones, err := strconv.Atoi(onesStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ones value '%s': %w", onesStr, err)
		}
		rows = append(rows, DataRow{Category: label, Ones: ones})
	}
	return rows, nil
}

// formatTimeLabel attempts to parse various timestamp formats and returns HH:MM:SS.
// If parsing fails, it returns the original string.
func formatTimeLabel(s string) string {
	formats := []string{
		// Common formats that pandas may accept
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006/01/02 15:04:05",
		"15:04:05",
		"15:04",
	}
	for _, layout := range formats {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("15:04:05")
		}
	}
	return s
}

// calculateZTest computes cumulative mean of ones and the z-score per row.
// expected_mean = 0.5 * block_size
// expected_std_dev = sqrt(block_size * 0.25)
// z_i = (cum_mean_i - expected_mean) / (expected_std_dev / sqrt(i+1))
func calculateZTest(rows []DataRow, blockSize int) []DataRow {
	expectedMean := 0.5 * float64(blockSize)
	expectedStdDev := math.Sqrt(float64(blockSize) * 0.25)
	if expectedStdDev == 0 {
		return rows
	}
	sum := 0
	for i := range rows {
		sum += rows[i].Ones
		cumMean := float64(sum) / float64(i+1)
		z := (cumMean - expectedMean) / (expectedStdDev / math.Sqrt(float64(i+1)))
		rows[i].CumulativeMean = cumMean
		rows[i].ZScore = z
	}
	return rows
}

// writeToExcel writes the rows to an Excel file with a line chart of the z-score.
// The first column header depends on input type: either "samples" or "time".
// The file is written next to the input path with a .xlsx extension.
func writeToExcel(rows []DataRow, filePath string, blockSize int, intervalSec int, firstColumnHeader string) error {
	if len(rows) == 0 {
		return errors.New("no data to write")
	}
	fileToSave := strings.TrimSuffix(filePath, filepath.Ext(filePath)) + ".xlsx"
	f := excelize.NewFile()
	defer f.Close()

	// Ensure we have a clean sheet named Zscore
	defaultSheet := f.GetSheetName(0)
	if defaultSheet != sheetName {
		f.NewSheet(sheetName)
		f.DeleteSheet(defaultSheet)
	}

	// Headers
	_ = f.SetCellStr(sheetName, "A1", firstColumnHeader)
	_ = f.SetCellStr(sheetName, "B1", onesColumnName)
	_ = f.SetCellStr(sheetName, "C1", "cumulative_mean")
	_ = f.SetCellStr(sheetName, "D1", "z_test")

	// Data rows
	for i, r := range rows {
		rowIdx := i + 2
		_ = f.SetCellStr(sheetName, fmt.Sprintf("A%d", rowIdx), r.Category)
		_ = f.SetCellInt(sheetName, fmt.Sprintf("B%d", rowIdx), r.Ones)
		_ = f.SetCellFloat(sheetName, fmt.Sprintf("C%d", rowIdx), r.CumulativeMean, 6, 64)
		_ = f.SetCellFloat(sheetName, fmt.Sprintf("D%d", rowIdx), r.ZScore, 6, 64)
	}

	// Build chart using struct API
	endRow := len(rows) + 1
	catRange := fmt.Sprintf("%s!$A$2:$A$%d", sheetName, endRow)
	valRange := fmt.Sprintf("%s!$D$2:$D$%d", sheetName, endRow)
	chart := &excelize.Chart{
		Type: excelize.Line,
		Series: []excelize.ChartSeries{
			{
				Name:       fmt.Sprintf("%s!$D$1", sheetName),
				Categories: catRange,
				Values:     valRange,
			},
		},
		Title:  []excelize.RichTextRun{{Text: filepath.Base(filePath)}},
		Legend: excelize.ChartLegend{Position: "none"},
		XAxis:  excelize.ChartAxis{Title: []excelize.RichTextRun{{Text: fmt.Sprintf("Number of Samples - one sample every %d second(s)", intervalSec)}}},
		YAxis:  excelize.ChartAxis{Title: []excelize.RichTextRun{{Text: fmt.Sprintf("Z-score - Sample Size =  %d bits)", blockSize)}}, MajorGridLines: true},
	}
	if err := f.AddChart(sheetName, "F2", chart); err != nil {
		return err
	}

	return f.SaveAs(fileToSave)
}

// run performs the end-to-end workflow: parse inputs, read data, compute, and export.
func run(filePath string) error {
	interval, err := findInterval(filePath)
	if err != nil {
		return err
	}
	blockSize, err := findBitCount(filePath)
	if err != nil {
		return err
	}

	var rows []DataRow
	firstHeader := blockColumnName
	if strings.HasSuffix(strings.ToLower(filePath), ".bin") {
		rows, err = readBinFile(filePath, blockSize)
		firstHeader = blockColumnName
	} else if strings.HasSuffix(strings.ToLower(filePath), ".csv") {
		rows, err = readCSVFile(filePath)
		firstHeader = timeColumnName
	} else {
		return fmt.Errorf("unsupported file type: %s", filepath.Ext(filePath))
	}
	if err != nil {
		return err
	}

	rows = calculateZTest(rows, blockSize)
	return writeToExcel(rows, filePath, blockSize, interval, firstHeader)
}

// main is the entry-point CLI that mirrors file_to_excel.py behavior.
// Usage: filetoexcel <path-to-.bin-or-.csv>
func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: filetoexcel <path-to-.bin-or-.csv>")
		os.Exit(2)
	}
	filePath := os.Args[1]
	if err := run(filePath); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
