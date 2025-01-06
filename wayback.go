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

	"golang.org/x/net/html"
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

	if len(body) == 0 {
		return nil, fmt.Errorf("received empty response body")
	}

	var rawData [][]string
	if err := json.Unmarshal(body, &rawData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %v\nResponse body: %s", err, string(body))
	}

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
	if err := w.downloadRecursively(snapshot, fileNum, totalFiles); err != nil {
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

	fmt.Printf("Downloading: %s\n", fileURL)

	resp, err := http.Get(fileURL)
	if err != nil {
		return fmt.Errorf("HTTP GET error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP status error: %s", resp.Status)
	}

	parsedURL, err := url.Parse(snapshot.Original)
	if err != nil {
		return fmt.Errorf("URL parsing error: %v", err)
	}

	relativePath := parsedURL.Path
	if relativePath == "" || relativePath == "/" {
		relativePath = "index.html"
	} else if !strings.Contains(filepath.Base(relativePath), ".") {
		relativePath = filepath.Join(relativePath, "index.html")
	}

	fullPath := filepath.Join(w.Directory, parsedURL.Host, snapshot.Timestamp, relativePath)

	contentType := resp.Header.Get("Content-Type")
	extension := getExtensionFromContentType(contentType)

	if extension != "" && !strings.HasSuffix(fullPath, extension) {
		fullPath = fullPath + extension
	}

	fmt.Printf("Saving to: %s\n", fullPath)

	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("directory creation error: %v", err)
	}

	file, err := os.Create(fullPath)
	if err != nil {
		return fmt.Errorf("file creation error: %v", err)
	}
	defer file.Close()

	written, err := io.Copy(file, resp.Body)
	if err != nil {
		return fmt.Errorf("file write error: %v", err)
	}

	fmt.Printf("File saved: %s (Size: %d bytes)\n", fullPath, written)

	return nil
}

func (w *WaybackDownloader) downloadRecursively(snapshot Snapshot, fileNum, totalFiles int) error {
	err := w.downloadFile(snapshot, fileNum, totalFiles)
	if err != nil {
		return err
	}

	parsedURL, _ := url.Parse(snapshot.Original)
	fullPath := filepath.Join(w.Directory, parsedURL.Host, snapshot.Timestamp, "index.html")

	if strings.HasSuffix(snapshot.Original, ".html") || strings.HasSuffix(fullPath, ".html") {
		file, err := os.Open(fullPath)
		if err != nil {
			return err
		}
		defer file.Close()

		doc, err := html.Parse(file)
		if err != nil {
			return err
		}

		var urls []string
		var f func(*html.Node)
		f = func(n *html.Node) {
			if n.Type == html.ElementNode {
				for _, a := range n.Attr {
					if (a.Key == "src" || a.Key == "href") && strings.HasPrefix(a.Val, "/") {
						urls = append(urls, a.Val)
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				f(c)
			}
		}
		f(doc)

		for _, u := range urls {
			newSnapshot := Snapshot{
				Timestamp: snapshot.Timestamp,
				Original:  w.BaseURL + u,
			}
			w.downloadFile(newSnapshot, fileNum, totalFiles)
		}
	}

	return nil
}

func getExtensionFromContentType(contentType string) string {
	switch contentType {
	case "text/html":
		return ".html"
	case "application/javascript":
		return ".js"
	case "text/css":
		return ".css"
	default:
		return ""
	}
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

	for i, snapshot := range snapshots {
		err := downloader.downloadFile(snapshot, i+1, len(snapshots))
		if err != nil {
			fmt.Printf("Error downloading %s: %v\n", snapshot.Original, err)
		}
	}

	fmt.Println("Download completed!")
}
