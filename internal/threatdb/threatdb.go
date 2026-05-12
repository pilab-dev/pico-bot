package threatdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	baseURL string
	cache   *threatDBCache
	once    sync.Once
	client  *http.Client
)

type ThreatType struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Subtypes    []string `json:"subtypes"`
}

type Mappings struct {
	MetricToType map[string]string `json:"metric_to_type"`
}

type threatDBCache struct {
	mu               sync.RWMutex
	threatTypes      []ThreatType
	mappings         Mappings
	maliciousDomains map[string]bool
	maliciousIPs     map[string]bool
	fetchedAt        time.Time
}

func init() {
	cache = &threatDBCache{
		maliciousDomains: make(map[string]bool),
		maliciousIPs:     make(map[string]bool),
	}
	client = &http.Client{Timeout: 10 * time.Second}
}

// Init configures the base URL for the threat DB repo (raw content URL)
func Init(baseURL string) {
	once.Do(func() {
		baseURL = strings.TrimSuffix(baseURL, "/")
	})
}

// Refresh fetches all threat DB data from the remote repo
func Refresh(ctx context.Context) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	// Fetch threat types
	typesData, err := fetchRaw(ctx, "threat-types.json")
	if err != nil {
		return fmt.Errorf("threat types: %w", err)
	}
	var types []ThreatType
	if err := json.Unmarshal(typesData, &types); err != nil {
		return fmt.Errorf("parse threat types: %w", err)
	}

	// Fetch mappings
	mappingsData, err := fetchRaw(ctx, "mappings.json")
	if err != nil {
		return fmt.Errorf("mappings: %w", err)
	}
	var mappings Mappings
	if err := json.Unmarshal(mappingsData, &mappings); err != nil {
		return fmt.Errorf("parse mappings: %w", err)
	}

	// Fetch malicious domains
	domainsData, err := fetchRaw(ctx, "malicious-domains.txt")
	if err != nil {
		return fmt.Errorf("malicious domains: %w", err)
	}
	domainMap := make(map[string]bool)
	for _, line := range strings.Split(string(domainsData), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		domainMap[line] = true
	}

	// Fetch malicious IPs
	ipsData, err := fetchRaw(ctx, "malicious-ips.txt")
	if err != nil {
		return fmt.Errorf("malicious IPs: %w", err)
	}
	ipMap := make(map[string]bool)
	for _, line := range strings.Split(string(ipsData), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ipMap[line] = true
	}

	cache.threatTypes = types
	cache.mappings = mappings
	cache.maliciousDomains = domainMap
	cache.maliciousIPs = ipMap
	cache.fetchedAt = time.Now()

	return nil
}

func fetchRaw(ctx context.Context, path string) ([]byte, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("threat DB base URL not configured")
	}
	url := fmt.Sprintf("%s/%s", baseURL, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, path)
	}
	return io.ReadAll(resp.Body)
}

// GetThreatCategory maps a WAF log metric to a broader threat category
func GetThreatCategory(metric string) string {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	if cat, ok := cache.mappings.MetricToType[metric]; ok {
		return cat
	}
	return metric // fallback to raw metric if no mapping
}

// IsMaliciousDomain checks if a domain is in the malicious list
func IsMaliciousDomain(domain string) bool {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	_, ok := cache.maliciousDomains[domain]
	return ok
}

// IsMaliciousIP checks if an IP is in the malicious list
func IsMaliciousIP(ip string) bool {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	_, ok := cache.maliciousIPs[ip]
	return ok
}

// GetThreatTypes returns all threat type definitions
func GetThreatTypes() []ThreatType {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.threatTypes
}
