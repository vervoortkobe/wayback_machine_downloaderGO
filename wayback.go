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
)

type WaybackDownloader struct {
	BaseURL       string
	Directory     string
	FromTimestamp string
	ToTimestamp   string
	OnlyFilter    string
	ExcludeFilter string
	AllTimestamps bool
	All           bool
	MaxPages      int
	ThreadsCount  int
	mu            sync.Mutex
}

type Snapshot struct {
	Timestamp string
	Original  string
}

func NewWaybackDownloader(baseURL string) *WaybackDownloader {
	return &WaybackDownloader{
		BaseURL:      baseURL,
		Directory:    "websites",
		MaxPages:     100,
		ThreadsCount: 1,
	}
}

func (w *WaybackDownloader) getRawListFromAPI(urlStr string, pageIndex *int) ([]Snapshot, error) {
	apiURL := "https://web.archive.org/cdx/search/cdx"
	
	params := url.Values{}
	params.Add("url", urlStr)
	params.Add("output", "json")
	params.Add("fl", "timestamp,original")
	params.Add("collapse", "digest")
	params.Add("gzip", "false")
	
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

	fullURL := fmt.Sprintf("%s?%s", apiURL, params.Encode())
	
	resp, err := http.Get(fullURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rawData [][]string
	if err := json.Unmarshal(body, &rawData); err != nil {
		return nil, err
	}

	// Skip header row if present
	if len(rawData) > 0 && len(rawData[0]) > 0 && rawData[0][0] == "timestamp" {
		rawData = rawData[1:]
	}

	snapshots := make([]Snapshot, len(rawData))
	for i, row := range rawData {
		if len(row) >= 2 {
			snapshots[i] = Snapshot{
				Timestamp: row[0],
				Original:  row[1],
			}
		}
	}

	return snapshots, nil
}

func (w *WaybackDownloader) downloadFile(snapshot Snapshot) error {
	fileURL := fmt.Sprintf("https://web.archive.org/web/%sid_/%s", snapshot.Timestamp, snapshot.Original)
	
	resp, err := http.Get(fileURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Create file path based on URL
	parsedURL, err := url.Parse(snapshot.Original)
	if err != nil {
		return err
	}

	relativePath := parsedURL.Path
	if relativePath == "" || strings.HasSuffix(relativePath, "/") {
		relativePath += "index.html"
	}

	fullPath := filepath.Join(w.Directory, parsedURL.Host, relativePath)
	
	// Create directory structure
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Create and write to file
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
	fmt.Printf("Downloaded: %s -> %s\n", snapshot.Original, fullPath)
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

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, downloader.ThreadsCount)

	for _, snapshot := range snapshots {
		wg.Add(1)
		semaphore <- struct{}{}
		
		go func(s Snapshot) {
			defer wg.Done()
			defer func() { <-semaphore }()
			
			if err := downloader.downloadFile(s); err != nil {
				fmt.Printf("Error downloading %s: %v\n", s.Original, err)
			}
		}(snapshot)
	}

	wg.Wait()
	fmt.Println("Download completed!")
}