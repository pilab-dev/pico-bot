package waftools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

type LogEntry struct {
	Timestamp time.Time
	Level     string
	Message   string
	Component string
	Error     string
	URL       string
	TraceID   string
	ClientIP  string
	Method    string
	Metric    string
}

// Regex for ISO timestamp format: 2026-04-12T17:59:03+02:00
var isoLogLineRegex = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[^\s]*)\s+(\w+)\s+(.*)`)

// Regex for Go http log format: 2026/04/12 17:59:06 http: TLS handshake error...
var goHttpLogRegex = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2}\s+\d{2}:\d{2}:\d{2})\s+http:\s+(.*)`)

// Key-value field regexes
var (
	errorRe    = regexp.MustCompile(`error="([^"]*)"`)
	urlRe      = regexp.MustCompile(`url=([^\s"]+)`)
	traceRe    = regexp.MustCompile(`trace_id=(\S+)`)
	clientIPRe = regexp.MustCompile(`client_ip=([^\s:]+)`)
	methodRe   = regexp.MustCompile(`method=(\w+)`)
	metricRe   = regexp.MustCompile(`metric=(\S+)`)
)

// ExtractActualLogMessage extracts the actual log message from a syslog line
// Format: "Apr 12 17:59:03 vm2.pilab.hu waf[723535]: ACTUAL_MESSAGE"
func ExtractActualLogMessage(line string) string {
	// Find the last ": " that precedes the actual log message
	// The pattern is: ... process[pid]: message
	idx := strings.LastIndex(line, ": ")
	if idx < 0 {
		return line
	}
	return line[idx+2:]
}

func ParseLogEntry(line string) (*LogEntry, error) {
	actualMsg := ExtractActualLogMessage(line)

	entry, err := tryParseISO(actualMsg)
	if err == nil {
		return entry, nil
	}

	entry, err = tryParseGoHTTP(actualMsg)
	if err == nil {
		return entry, nil
	}

	return nil, fmt.Errorf("invalid log format")
}

func tryParseISO(line string) (*LogEntry, error) {
	matches := isoLogLineRegex.FindStringSubmatch(line)
	if len(matches) < 4 {
		return nil, fmt.Errorf("no ISO match")
	}

	entry := &LogEntry{
		Level: matches[2],
	}

	if ts, err := time.Parse("2006-01-02T15:04:05-07:00", matches[1]); err == nil {
		entry.Timestamp = ts
	}

	rest := matches[3]
	entry.Message = extractMessage(rest)
	extractFields(rest, entry)

	return entry, nil
}

func tryParseGoHTTP(line string) (*LogEntry, error) {
	matches := goHttpLogRegex.FindStringSubmatch(line)
	if len(matches) < 3 {
		return nil, fmt.Errorf("no Go HTTP match")
	}

	entry := &LogEntry{
		Level: "ERR",
		Component: "http",
		Message: "TLS handshake error",
	}

	if ts, err := time.Parse("2006/01/02 15:04:05", matches[1]); err == nil {
		entry.Timestamp = ts
	}

	rest := matches[2]

	// Extract client IP from "TLS handshake error from IP:port: ..."
	if idx := strings.Index(rest, "from "); idx >= 0 {
		after := rest[idx+5:]
		end := strings.IndexAny(after, ": ")
		if end > 0 {
			entry.ClientIP = after[:end]
		}
	}
	entry.Error = rest

	return entry, nil
}

func parseISOLogEntry(line string) (*LogEntry, error) {
	matches := isoLogLineRegex.FindStringSubmatch(line)
	if len(matches) < 4 {
		return nil, fmt.Errorf("invalid ISO log format")
	}

	entry := &LogEntry{
		Level: matches[2],
	}

	if ts, err := time.Parse("2006-01-02T15:04:05-07:00", matches[1]); err == nil {
		entry.Timestamp = ts
	}

	rest := matches[3]

	// Check if message is in backticks (old format)
	if idx := strings.Index(rest, "`"); idx >= 0 {
		// Old format: COMPONENT `message` key=value
		beforeBacktick := strings.TrimSpace(rest[:idx])
		fields := strings.Fields(beforeBacktick)
		if len(fields) > 0 {
			entry.Component = fields[0]
		}
		// Extract message from backticks
		closeIdx := strings.Index(rest[idx+1:], "`")
		if closeIdx >= 0 {
			entry.Message = rest[idx+1 : idx+1+closeIdx]
		}
	} else {
		// New format: MESSAGE key=value (no backticks)
		entry.Message = extractMessage(rest)
	}

	// Extract fields from key=value pairs
	extractFields(rest, entry)

	return entry, nil
}

func parseGoHTTPLogEntry(line string) (*LogEntry, error) {
	matches := goHttpLogRegex.FindStringSubmatch(line)
	if len(matches) < 3 {
		return nil, fmt.Errorf("invalid Go http log format")
	}

	entry := &LogEntry{}

	if ts, err := time.Parse("2006/01/02 15:04:05", matches[1]); err == nil {
		entry.Timestamp = ts
	}

	rest := matches[2]

	// Extract client IP from "TLS handshake error from IP:port"
	if strings.Contains(rest, "TLS handshake error from ") {
		entry.Level = "ERR"
		entry.Component = "http"
		entry.Message = "TLS handshake error"

		idx := strings.Index(rest, "TLS handshake error from ")
		if idx >= 0 {
			after := rest[idx+len("TLS handshake error from "):]
			end := strings.IndexAny(after, ": ")
			if end > 0 {
				entry.ClientIP = after[:end]
			}
		}
		entry.Error = rest
	}

	return entry, nil
}

func extractMessage(rest string) string {
	// Find where key=value pairs start
	kvStart := -1
	for _, kv := range []string{"error=", "client_ip=", "url=", "trace_id=", "method=", "metric=", "component="} {
		if idx := strings.Index(rest, kv); idx > 0 {
			if kvStart == -1 || idx < kvStart {
				kvStart = idx
			}
		}
	}
	if kvStart > 0 {
		return strings.TrimSpace(rest[:kvStart])
	}
	return strings.TrimSpace(rest)
}

func extractFields(rest string, entry *LogEntry) {
	if m := errorRe.FindStringSubmatch(rest); len(m) > 1 {
		entry.Error = m[1]
	}
	if m := urlRe.FindStringSubmatch(rest); len(m) > 1 {
		entry.URL = m[1]
	}
	if m := traceRe.FindStringSubmatch(rest); len(m) > 1 {
		entry.TraceID = m[1]
	}
	if m := clientIPRe.FindStringSubmatch(rest); len(m) > 1 {
		entry.ClientIP = m[1]
	}
	if m := methodRe.FindStringSubmatch(rest); len(m) > 1 {
		entry.Method = m[1]
	}
	if m := metricRe.FindStringSubmatch(rest); len(m) > 1 {
		entry.Metric = m[1]
	}
	// Extract component if not already set
	if entry.Component == "" {
		if idx := strings.Index(rest, "component="); idx >= 0 {
			after := rest[idx+len("component="):]
			end := strings.IndexAny(after, " \n")
			if end > 0 {
				entry.Component = after[:end]
			} else {
				entry.Component = after
			}
		}
	}
}

func ParseLogFile(path string) ([]LogEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(data), "\n")
	var entries []LogEntry

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if entry, err := ParseLogEntry(line); err == nil {
			entries = append(entries, *entry)
		}
	}

	return entries, nil
}

type Threat struct {
	IP            string
	Count         int
	Country       string
	ISOCode       string
	Provider      string
	AbuseEmail    string
	AttackType    string
	ThreatCategory string
	Method        string
	URLs          []string
	MaliciousURLs []string
}

type ThreatDetail struct {
	Timestamp time.Time
	Method    string
	URL       string
	Error     string
	TraceID   string
}

type RipeResponse struct {
	Handle          string   `json:"handle"`
	StartAddress    string   `json:"startAddress"`
	EndAddress      string   `json:"endAddress"`
	IPVersion       string   `json:"ipVersion"`
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	Country         string   `json:"country"`
	ParentHandle    string   `json:"parentHandle"`
	Cidr0Cidrs      []Cidr   `json:"cidr0_cidrs"`
	Status          []string `json:"status"`
	Entities        []Entity `json:"entities"`
	Links           []Link   `json:"links"`
	Events          []Event  `json:"events"`
	Notices         []Notice `json:"notices"`
	RdapConformance []string `json:"rdapConformance"`
	Port43          string   `json:"port43"`
	ObjectClassName string   `json:"objectClassName"`
}

type Cidr struct {
	V4Prefix string `json:"v4prefix"`
	Length   int    `json:"length"`
}

type Entity struct {
	Handle          string        `json:"handle"`
	VcardArray      []interface{} `json:"vcardArray"`
	Roles           []string      `json:"roles"`
	Links           []Link        `json:"links"`
	ObjectClassName string        `json:"objectClassName"`
}

type EntityInfo struct {
	Name  string
	Email string
	Kind  string
}

func (e Entity) ParseVCard() EntityInfo {
	var info EntityInfo
	if len(e.VcardArray) < 2 {
		return info
	}
	arr, ok := e.VcardArray[1].([]any)
	if !ok {
		return info
	}
	for _, item := range arr {
		fields, ok := item.([]any)
		if !ok || len(fields) < 2 {
			continue
		}
		key, _ := fields[0].(string)
		switch key {
		case "fn":
			if len(fields) >= 4 {
				if text, ok := fields[3].(string); ok {
					info.Name = text
				}
			}
		case "kind":
			if len(fields) >= 4 {
				if text, ok := fields[3].(string); ok {
					info.Kind = text
				}
			}
		case "email":
			if len(fields) >= 4 {
				if text, ok := fields[3].(string); ok {
					info.Email = text
				}
			}
		}
	}
	return info
}

func (r RipeResponse) GetAbuseEmail() string {
	for _, e := range r.Entities {
		info := e.ParseVCard()
		if info.Email != "" {
			for _, role := range e.Roles {
				if role == "abuse" {
					return info.Email
				}
			}
		}
	}
	return ""
}

func (r RipeResponse) GetOrgName() string {
	for _, e := range r.Entities {
		info := e.ParseVCard()
		if info.Kind == "org" && info.Name != "" {
			return info.Name
		}
	}
	return ""
}

type Link struct {
	Value string `json:"value"`
	Rel   string `json:"rel"`
	Href  string `json:"href"`
	Type  string `json:"type,omitempty"`
}

type Event struct {
	EventAction string `json:"eventAction"`
	EventDate   string `json:"eventDate"`
}

type Notice struct {
	Title       string   `json:"title"`
	Description []string `json:"description"`
	Links       []Link   `json:"links,omitempty"`
}

func LookupWHOIS(ctx context.Context, ip string, baseURL string, client *http.Client) (provider string, _ string, err error) {
	if client == nil {
		client = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = "https://rdap.db.ripe.net"
	}
	url := fmt.Sprintf("%s/ip/%s", baseURL, ip)

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "pico-bot/1.0")
	req.Header.Set("Accept", "application/rdap+json, application/json, */*")

	resp, err := client.Do(req)
	if err != nil {
		return "RDAP error", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Sprintf("Status:%d", resp.StatusCode), "", nil
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "Parse error", "", err
	}

	if network, ok := data["network"].(map[string]interface{}); ok {
		if name, ok := network["name"].(string); ok && name != "" {
			provider = name
		}
		if handle, ok := network["handle"].(string); ok && handle != "" && provider == "" {
			provider = handle
		}
		if handle, ok := network["handle"].(string); ok && handle != "" && provider != "" {
			provider = fmt.Sprintf("%s (%s)", provider, handle)
		}
		if provider == "" {
			if startAddr, ok := network["startAddress"].(string); ok {
				provider = startAddr
			}
		}
	}

	if provider == "" {
		provider = "Unknown"
	}

	return provider, "", nil
}

func LookupWHOISParsed(ctx context.Context, ip string, baseURL string, client *http.Client) (*RipeResponse, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = "https://rdap.db.ripe.net"
	}
	url := fmt.Sprintf("%s/ip/%s", baseURL, ip)

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "pico-bot/1.0")
	req.Header.Set("Accept", "application/rdap+json, application/json, */*")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("RDAP error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status: %d", resp.StatusCode)
	}

	var ripe RipeResponse
	if err := json.NewDecoder(resp.Body).Decode(&ripe); err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	return &ripe, nil
}

func LookupWHOISBatch(ctx context.Context, ips []string, baseURL string, client *http.Client) ([]*RipeResponse, []error) {
	if client == nil {
		client = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = "https://rdap.db.ripe.net"
	}

	results := make([]*RipeResponse, len(ips))
	errs := make([]error, len(ips))
	var wg sync.WaitGroup
	var mu sync.Mutex
	semaphore := make(chan struct{}, 10) // Limit to 10 concurrent lookups

	for i, ip := range ips {
		wg.Add(1)
		go func(idx int, ipAddr string) {
			defer wg.Done()
			semaphore <- struct{}{}        // Acquire
			defer func() { <-semaphore }() // Release

			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			resp, err := LookupWHOISParsed(ctx, ipAddr, baseURL, client)
			mu.Lock()
			results[idx] = resp
			errs[idx] = err
			mu.Unlock()
		}(i, ip)
	}

	wg.Wait()
	return results, errs
}
