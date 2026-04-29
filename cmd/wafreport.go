package cmd

import (
	"fmt"
	"log"
	"net/netip"
	"sort"
	"sync"
	"time"

	"go.pilab.hu/cloud/pico-bot/internal/waftools"

	"github.com/oschwald/maxminddb-golang/v2"
	"github.com/slack-go/slack"
	"github.com/spf13/cobra"
)

var wafreportCmd = &cobra.Command{
	Use:   "wafreport",
	Short: "Analyze WAF logs and report threats",
	RunE: func(cmd *cobra.Command, args []string) error {
		initConfig()

		log.Println("Step 1: Parsing WAF logs...")
		threats, err := parseWAFLogs()
		if err != nil {
			return fmt.Errorf("failed to parse: %w", err)
		}

		if len(threats) == 0 {
			return postToSlackWAF([]waftools.Threat{})
		}

		log.Println("Step 2: Enriching with GeoIP + WHOIS...")
		enriched, err := enrichThreats(threats)
		if err != nil {
			log.Printf("Enrichment warning: %v", err)
		}

		log.Println("Step 3: Posting to Slack...")
		return postToSlackWAF(enriched)
	},
}

func parseWAFLogs() ([]waftools.Threat, error) {
	logFile := getEnv("WAF_LOG_FILE", "journalctl_vm2_full.log")
	todayOnly := getEnv("REPORT_TODAY_ONLY", "true") == "true"

	entries, err := waftools.ParseLogFile(logFile)
	if err != nil {
		return nil, fmt.Errorf("failed to parse log file: %w", err)
	}

	today := time.Now().Format("Jan 02")
	threatMap := make(map[string]*waftools.Threat)

	for _, entry := range entries {
		if todayOnly && entry.Timestamp.Format("Jan 02") != today {
			continue
		}

		// Only process WAF rejection/error events with client IP
		if entry.ClientIP == "" {
			continue
		}

		if entry.Level != "WRN" && entry.Level != "ERR" {
			continue
		}

		ip := entry.ClientIP
		if _, err := netip.ParseAddr(ip); err != nil {
			continue
		}

		if _, exists := threatMap[ip]; !exists {
			threatMap[ip] = &waftools.Threat{
				IP:         ip,
				AttackType: entry.Metric,
				Method:     entry.Method,
				URLs:       []string{},
			}
		}
		threatMap[ip].Count++

		if entry.URL != "" && !contains(threatMap[ip].URLs, entry.URL) {
			threatMap[ip].URLs = append(threatMap[ip].URLs, entry.URL)
		}
	}

	topN := 10
	if cfg != nil {
		if v := cfg.GetInt("REPORT_TOP_N"); v != 0 {
			topN = v
		}
	}

	var threats []waftools.Threat
	for _, t := range threatMap {
		threats = append(threats, *t)
	}

	sort.Slice(threats, func(i, j int) bool {
		return threats[i].Count > threats[j].Count
	})

	if len(threats) > topN {
		threats = threats[:topN]
	}

	return threats, nil
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

type geoCache struct {
	mu   sync.Mutex
	data map[string]struct {
		ISOCode string
		Country string
	}
}

func (c *geoCache) get(ip string) (string, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.data[ip]; ok {
		return entry.ISOCode, entry.Country, true
	}
	return "", "", false
}

func (c *geoCache) set(ip, isoCode, country string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[ip] = struct {
		ISOCode string
		Country string
	}{isoCode, country}
}

type whoisCache struct {
	mu   sync.Mutex
	data map[string]struct {
		Provider string
		Email    string
	}
}

func (c *whoisCache) get(ip string) (string, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.data[ip]; ok {
		return entry.Provider, entry.Email, true
	}
	return "", "", false
}

func (c *whoisCache) set(ip, provider, email string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[ip] = struct {
		Provider string
		Email    string
	}{provider, email}
}

func enrichThreats(threats []waftools.Threat) ([]waftools.Threat, error) {
	geoDBPath := getEnv("GEOIP_DB_PATH", "/home/pilab.hu/cloud/pico-bot/GeoLite2-Country.mmdb")

	db, err := maxminddb.Open(geoDBPath)
	if err != nil {
		return threats, fmt.Errorf("GeoIP DB: %w", err)
	}
	defer db.Close()

	log.Printf("Enriching %d threats...", len(threats))

	cache := &geoCache{data: make(map[string]struct {
		ISOCode string
		Country string
	})}

	// GeoIP enrichment with caching
	for i, t := range threats {
		if isoCode, country, ok := cache.get(t.IP); ok {
			threats[i].ISOCode = isoCode
			threats[i].Country = country
			continue
		}

		ip, err := netip.ParseAddr(t.IP)
		if err != nil {
			continue
		}

		var rec struct {
			Country struct {
				ISOCode string            `maxminddb:"iso_code"`
				Names   map[string]string `maxminddb:"names"`
			} `maxminddb:"country"`
		}

		if err := db.Lookup(ip).Decode(&rec); err == nil {
			isoCode := rec.Country.ISOCode
			country := rec.Country.Names["en"]
			threats[i].ISOCode = isoCode
			threats[i].Country = country
			cache.set(t.IP, isoCode, country)
		}
	}

	// WHOIS enrichment with caching (batch)
	whoisCache := &whoisCache{data: make(map[string]struct {
		Provider string
		Email    string
	})}

	// Check cache first
	missing := []int{}
	ips := []string{}
	for i, t := range threats {
		if provider, email, ok := whoisCache.get(t.IP); ok {
			threats[i].Provider = provider
			threats[i].AbuseEmail = email
		} else {
			missing = append(missing, i)
			ips = append(ips, t.IP)
		}
	}

	// Batch lookup for missing
	if len(ips) > 0 {
		ctx2, cancel := contextWithTimeout(30 * time.Second)
		defer cancel()

		responses, errs := waftools.LookupWHOISBatch(ctx2, ips, "", nil)
		for j, resp := range responses {
			idx := missing[j]
			if errs[j] != nil || resp == nil {
				threats[idx].Provider = "Unknown"
				threats[idx].AbuseEmail = ""
				continue
			}
			provider := resp.GetOrgName()
			if provider == "" {
				provider = resp.Name
			}
			if provider == "" {
				provider = resp.Handle
			}
			if provider == "" {
				provider = "Unknown"
			}
			email := resp.GetAbuseEmail()
			threats[idx].Provider = provider
			threats[idx].AbuseEmail = email
			whoisCache.set(ips[j], provider, email)
		}
	}

	return threats, nil
}

func flagForISOCode(code string) string {
	flags := map[string]string{
		"US": "🇺🇸", "CN": "🇨🇳", "DE": "🇩🇪", "GB": "🇬🇧", "FR": "🇫🇷",
		"JP": "🇯🇵", "AU": "🇦🇺", "CA": "🇨🇦", "IN": "🇮🇳", "BR": "🇧🇷",
		"KR": "🇰🇷", "NL": "🇳🇱", "SE": "🇸🇪", "CH": "🇨🇭", "SG": "🇸🇬",
		"HK": "🇭🇰", "TW": "🇹🇼", "RU": "🇷🇺", "UA": "🇺🇦", "PL": "🇵🇱",
		"IT": "🇮🇹", "ES": "🇪🇸", "MX": "🇲🇽", "AR": "🇦🇷", "ZA": "🇿🇦",
		"AE": "🇦🇪", "PK": "🇵🇰", "RO": "🇷🇴", "HU": "🇭🇺", "CZ": "🇨🇿",
	}
	if f, ok := flags[code]; ok {
		return f
	}
	return "🌍"
}

func makeRichCell(text string, bold bool) *slack.RichTextBlock {
	var style *slack.RichTextSectionTextStyle
	if bold {
		style = &slack.RichTextSectionTextStyle{Bold: true}
	}
	return slack.NewRichTextBlock("",
		slack.NewRichTextSection(
			slack.NewRichTextSectionTextElement(text, style),
		),
	)
}

func postToSlackWAF(threats []waftools.Threat) error {
	client := slack.New(slackBotToken)

	headerBlock := slack.NewHeaderBlock(slack.NewTextBlockObject(slack.PlainTextType, "🚨 WAF Threat Report", true, false))

	totalReqs := 0
	for _, t := range threats {
		totalReqs += t.Count
	}
	statsText := slack.NewTextBlockObject(slack.MarkdownType,
		fmt.Sprintf("*Date:* %s\n*Total:* %d threats | *Top %d attackers*",
			time.Now().Format("Jan 02, 2006"), totalReqs, len(threats)), false, false)
	statsSection := slack.NewSectionBlock(statsText, nil, nil)

	divider := slack.NewDividerBlock()

	tbl := slack.NewTableBlock("threats-table")
	tbl.AddRow(
		makeRichCell("#", true),
		makeRichCell("IP", true),
		makeRichCell("Flag", true),
		makeRichCell("Country", true),
		makeRichCell("Method", true),
		makeRichCell("Attack Type", true),
		makeRichCell("Provider", true),
		makeRichCell("Reqs", true),
	)

	for i, t := range threats {
		provider := t.Provider
		if provider == "" {
			provider = "-"
		}
		method := t.Method
		if method == "" {
			method = "-"
		}
		attackType := t.AttackType
		if attackType == "" {
			attackType = "-"
		}

		tbl.AddRow(
			makeRichCell(fmt.Sprintf("%d", i+1), false),
			makeRichCell(t.IP, false),
			makeRichCell(flagForISOCode(t.ISOCode), false),
			makeRichCell(t.Country, false),
			makeRichCell(method, false),
			makeRichCell(attackType, false),
			makeRichCell(provider, false),
			makeRichCell(fmt.Sprintf("%d", t.Count), false),
		)
	}

	var blocks []slack.Block
	blocks = append(blocks, headerBlock, statsSection, divider, tbl)

	// Add URL details for top threats
	for i, t := range threats {
		if i >= 3 {
			break // Only show URLs for top 3
		}
		if len(t.URLs) > 0 {
			urlText := fmt.Sprintf("*%s* targeted URLs:\n", t.IP)
			for j, url := range t.URLs {
				if j >= 5 {
					urlText += "... and more\n"
					break
				}
				urlText += fmt.Sprintf("• %s\n", url)
			}
			urlSection := slack.NewSectionBlock(
				slack.NewTextBlockObject(slack.MarkdownType, urlText, false, false),
				nil, nil,
			)
			blocks = append(blocks, urlSection)
		}
	}

	footerText := slack.NewTextBlockObject(slack.MarkdownType,
		fmt.Sprintf("_Generated by pico-bot at %s_", time.Now().Format("15:04 MST")), false, false)
	footerContext := slack.NewContextBlock("", footerText)
	blocks = append(blocks, divider, footerContext)

	_, _, err := client.PostMessage(slackChannel,
		slack.MsgOptionBlocks(blocks...),
		slack.MsgOptionText(fmt.Sprintf("WAF Threat Report: %d attackers", len(threats)), false))

	return err
}
