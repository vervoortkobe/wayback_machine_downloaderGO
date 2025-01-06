package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cheggaaa/pb/v3"
)

type WaybackDownloader struct {
	BaseURL              string
	Directory            string
	FromTimestamp        string
	ToTimestamp          string
	OnlyFilter           string
	ExcludeFilter        string
	AllTimestamps        bool
	All                  bool
	MaxPages             int
	ThreadsCount         int
	mu                   sync.Mutex
	downloadedTimestamps map[string]bool
}

type Snapshot struct {
	Timestamp string
	Original  string
}

func NewWaybackDownloader(baseURL string) *WaybackDownloader {
	return &WaybackDownloader{
		BaseURL:              baseURL,
		Directory:            "websites",
		MaxPages:             100,
		ThreadsCount:         1,
		downloadedTimestamps: make(map[string]bool),
	}
}

func (w *WaybackDownloader) getRawListFromAPI(urlStr string, pageIndex *int) ([]Snapshot, error) {
	apiURL := "https://web.archive.org/cdx/search/cdx"
	params := url.Values{}
	params.Add("url", urlStr+"/*")
	params.Add("fl", "timestamp,original")
	params.Add("collapse", "digest")
	params.Add("gzip", "false")
	params.Add("output", "json")

	if !w.All {
		params.Add("filter", "statuscode:200")
	}
	if w.FromTimestamp != "" {
		params.Add("from", w.FromTimestamp)
	}
	if w.ToTimestamp != "" {
		params.Add("to", w.ToTimestamp)
	}
	if pageIndex != nil {
		params.Add("page", fmt.Sprintf("%d", *pageIndex))
	}
	if w.OnlyFilter != "" {
		params.Add("filter", w.OnlyFilter)
	}
	if w.ExcludeFilter != "" {
		params.Add("exclude", w.ExcludeFilter)
	}

	fullURL := fmt.Sprintf("%s?%s", apiURL, params.Encode())

	// Debug: Print full URL
	fmt.Printf("Full URL: %s\n", fullURL)

	resp, err := http.Get(fullURL)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	// Debug: Print raw response body
	// fmt.Printf("Raw response body: %s\n", string(body))

	// Check if body is empty
	if len(body) == 0 {
		return nil, fmt.Errorf("received empty response body")
	}

	var rawData [][]string
	if err := json.Unmarshal(body, &rawData); err != nil {
		// More detailed error logging
		return nil, fmt.Errorf("failed to unmarshal JSON: %v\nResponse body: %s", err, string(body))
	}

	// Skip header row if present
	if len(rawData) > 0 && len(rawData[0]) > 0 && rawData[0][0] == "timestamp" {
		rawData = rawData[1:]
	}

	snapshots := make([]Snapshot, 0, len(rawData))
	for _, row := range rawData {
		if len(row) >= 2 {
			snapshots = append(snapshots, Snapshot{
				Timestamp: row[0],
				Original:  row[1],
			})
		}
	}

	return snapshots, nil
}

func (w *WaybackDownloader) downloadSnapshot(snapshot Snapshot, fileNum, totalFiles int) {
	if err := w.downloadFile(snapshot, fileNum, totalFiles); err != nil {
		fmt.Printf("Error downloading %s: %v\n", snapshot.Original, err)
	} else {
		fmt.Printf("Downloaded %s\n", snapshot.Original)
		if w.AllTimestamps {
			w.mu.Lock()
			w.downloadedTimestamps[snapshot.Timestamp] = true
			w.mu.Unlock()
		}
	}
}

func (w *WaybackDownloader) downloadFile(snapshot Snapshot, fileNum, totalFiles int) error {
	w.mu.Lock()
	if w.downloadedTimestamps[snapshot.Timestamp] {
		w.mu.Unlock()
		return nil
	}
	w.downloadedTimestamps[snapshot.Timestamp] = true
	w.mu.Unlock()

	fileURL := fmt.Sprintf("https://web.archive.org/web/%sid_/%s", snapshot.Timestamp, snapshot.Original)
	resp, err := http.Get(fileURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	parsedURL, err := url.Parse(snapshot.Original)
	if err != nil {
		return err
	}

	// Modify this part to handle static assets and paths more robustly
	relativePath := parsedURL.Path
	if relativePath == "" {
		relativePath = "index.html"
	} else if strings.HasSuffix(relativePath, "/") {
		relativePath += "index.html"
	} else {
		// Remove leading slash to create a proper relative path
		relativePath = strings.TrimPrefix(relativePath, "/")
	}

	// Use the full URL path to create a more accurate file structure
	fullPath := filepath.Join(w.Directory, parsedURL.Host, snapshot.Timestamp, relativePath)

	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	file, err := os.Create(fullPath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return err
	}

	w.mu.Lock()
	fmt.Printf("[%d/%d] Downloaded: %s -> %s\n", fileNum, totalFiles, snapshot.Original, fullPath)
	w.mu.Unlock()

	return nil
}

func main() {
	baseURL := flag.String("url", "", "URL to download from Wayback Machine")
	directory := flag.String("dir", "websites", "Directory to save files")
	fromTimestamp := flag.String("from", "", "From timestamp (YYYYMMDDHHMMSS)")
	toTimestamp := flag.String("to", "", "To timestamp (YYYYMMDDHHMMSS)")
	threads := flag.Int("threads", 1, "Number of concurrent downloads")
	flag.Parse()

	if *baseURL == "" {
		fmt.Println("Please provide a URL to download using -url flag")
		return
	}

	downloader := NewWaybackDownloader(*baseURL)
	downloader.Directory = *directory
	downloader.FromTimestamp = *fromTimestamp
	downloader.ToTimestamp = *toTimestamp
	downloader.ThreadsCount = *threads

	snapshots, err := downloader.getRawListFromAPI(downloader.BaseURL, nil)
	if err != nil {
		fmt.Printf("Error getting snapshots: %v\n", err)
		return
	}

	fmt.Printf("Found %d snapshots to download\n", len(snapshots))

	bar := pb.StartNew(len(snapshots))
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, downloader.ThreadsCount)

	for i, snapshot := range snapshots {
		wg.Add(1)
		semaphore <- struct{}{}
		go func(s Snapshot, fileNum int) {
			defer wg.Done()
			defer func() { <-semaphore }()
			if err := downloader.downloadFile(s, fileNum, len(snapshots)); err != nil {
				fmt.Printf("Error downloading %s: %v\n", s.Original, err)
			}
			bar.Increment()
		}(snapshot, i+1)
	}

	wg.Wait()
	bar.Finish()
	fmt.Println("Download completed!")
}
