package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/astrica1/gollama"
	"github.com/slack-go/slack"
	"github.com/spf13/cobra"
)

var gitsummaryCmd = &cobra.Command{
	Use:   "gitsummary",
	Short: "Fetch GitHub activity and summarize using local LLM",
	RunE: func(cmd *cobra.Command, args []string) error {
		initConfig()

		log.Println("Step 1: Fetching GitHub PR activity...")
		orgs := strings.Split(githubOrgs, ",")
		activity, err := fetchGitHubOrgWork(orgs)
		if err != nil {
			return fmt.Errorf("failed to fetch: %w", err)
		}

		if activity == "" || activity == "[]" {
			return postToSlackSimple("No PRs today. Time to code! 💻")
		}

		log.Println("Step 2: Summarizing with local LLM...")
		summary, err := summarizeWithLLM(activity)
		if err != nil {
			log.Printf("LLM failed: %v", err)
			summary = formatGitHubActivity(activity)
		}

		log.Println("Step 3: Posting to Slack...")
		return postToSlackSimple(summary)
	},
}

type prEntry struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	Repo      string `json:"repo"`
	State     string `json:"state"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	Author    string `json:"author"`
}

type ghPR struct {
	URL       string     `json:"url"`
	Title     string     `json:"title"`
	Repo      repoInfo   `json:"repository"`
	State     string     `json:"state"`
	CreatedAt string     `json:"createdAt"`
	UpdatedAt string     `json:"updatedAt"`
	Author    authorInfo `json:"author"`
}

type repoInfo struct {
	NameWithOwner string `json:"nameWithOwner"`
}

type authorInfo struct {
	Login string `json:"login"`
}

// fetchGitHubOrgWork fetches all PRs from multiple orgs using gh CLI.
// Fetches: created today, updated today (includes merged/closed)
func fetchGitHubOrgWork(orgs []string) (string, error) {
	seen := make(map[string]bool)
	var results []prEntry
	today := time.Now().Format("2006-01-02")

	for _, org := range orgs {
		args := []string{"search", "prs", "--owner", org, "--state", "open", "--created", ">=" + today, "-L", "50", "--json", "title,repository,state,url,createdAt,updatedAt,author"}
		cmd := exec.Command("gh", args...)
		out, err := cmd.Output()
		if err != nil {
			log.Printf("gh search created failed for org %s: %v", org, err)
		} else {
			parseAndDedup(string(out), seen, &results)
		}

		args = []string{"search", "prs", "--owner", org, "--updated", ">=" + today, "-L", "50", "--json", "title,repository,state,url,createdAt,updatedAt,author"}
		cmd = exec.Command("gh", args...)
		out, err = cmd.Output()
		if err != nil {
			log.Printf("gh search updated failed for org %s: %v", org, err)
		} else {
			parseAndDedup(string(out), seen, &results)
		}
	}

	data, err := json.Marshal(results)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseAndDedup(jsonStr string, seen map[string]bool, results *[]prEntry) {
	var prs []ghPR
	if err := json.Unmarshal([]byte(jsonStr), &prs); err != nil {
		return
	}
	for _, pr := range prs {
		if seen[pr.URL] {
			continue
		}
		seen[pr.URL] = true
		author := "unknown"
		if pr.Author.Login != "" {
			author = pr.Author.Login
		}
		repo := "unknown/repo"
		if pr.Repo.NameWithOwner != "" {
			repo = pr.Repo.NameWithOwner
		}
		*results = append(*results, prEntry{
			URL:       pr.URL,
			Title:     pr.Title,
			Repo:      repo,
			State:     pr.State,
			CreatedAt: pr.CreatedAt,
			UpdatedAt: pr.UpdatedAt,
			Author:    author,
		})
	}
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func summarizeWithLLM(activity string) (string, error) {
	client := &http.Client{Timeout: 120 * time.Second}

	resp, err := client.Get(ollamaURL + "/v1/models")
	if err == nil && resp.StatusCode == 200 {
		return summarizeWithOpenAIAPI(client, activity)
	}

	gollamaClient, err := gollama.NewClient(ollamaURL)
	if err != nil {
		return "", err
	}

	ctx2, cancel := contextWithTimeout(120 * time.Second)
	defer cancel()

	models, err := gollamaClient.List(ctx2)
	if err != nil || models == nil || len(models.Models) == 0 {
		return "", fmt.Errorf("no models")
	}

	modelName := ollamaModel
	found := false
	for _, m := range models.Models {
		if m.Name == modelName {
			found = true
			break
		}
	}
	if !found {
		modelName = models.Models[0].Name
	}

	prompt := fmt.Sprintf(`You are a DevOps assistant. Summarize this GitHub activity for a team update.
Use markdown with *bold* for repo names. Keep it brief.

Here are the PRs:
%s`, activity)

	resp2, err := gollamaClient.Generate(ctx2, &gollama.GenerateRequest{
		Model:  modelName,
		Prompt: prompt,
	})
	if err != nil || resp2 == nil {
		return "", err
	}

	return resp2.Response, nil
}

func summarizeWithOpenAIAPI(client *http.Client, activity string) (string, error) {
	type Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	type ChatRequest struct {
		Model       string    `json:"model"`
		Messages    []Message `json:"messages"`
		MaxTokens   int       `json:"max_tokens"`
		Temperature float64   `json:"temperature"`
	}

	type Choice struct {
		Message Message `json:"message"`
		Text    string  `json:"text"`
	}

	type ChatResponse struct {
		Choices []Choice `json:"choices"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	prompt := fmt.Sprintf(`You are a DevOps assistant. Summarize this GitHub activity for a team update.
Use markdown with *bold* for repo names. Keep it brief.

Here are the PRs:
%s`, activity)

	reqBody := ChatRequest{
		Model: ollamaModel,
		Messages: []Message{
			{Role: "system", Content: "You are a helpful DevOps assistant."},
			{Role: "user", Content: prompt},
		},
		MaxTokens:   500,
		Temperature: 0.7,
	}

	body, _ := json.Marshal(reqBody)
	ctx, cancel := contextWithTimeout(120 * time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", ollamaURL+"/v1/chat/completions", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer dummy")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", err
	}

	if chatResp.Error != nil {
		return "", errors.New(chatResp.Error.Message)
	}

	if len(chatResp.Choices) > 0 {
		msg := chatResp.Choices[0].Message
		if msg.Content != "" {
			return msg.Content, nil
		}
		if chatResp.Choices[0].Text != "" {
			return chatResp.Choices[0].Text, nil
		}
	}

	return "", fmt.Errorf("no response")
}

func formatGitHubActivity(activity string) string {
	var prs []prEntry
	if err := json.Unmarshal([]byte(activity), &prs); err != nil {
		return "*Unable to parse PR data*"
	}
	if len(prs) == 0 {
		return "*No PRs*"
	}

	var b strings.Builder
	b.WriteString("*Recent PRs*\n")
	for i, pr := range prs {
		if i >= 10 {
			break
		}
		b.WriteString(fmt.Sprintf("- [%s] %s (%s)\n", pr.Repo, pr.Title, pr.Author))
	}
	if len(prs) > 10 {
		b.WriteString(fmt.Sprintf("\n_...and %d more_", len(prs)-10))
	}
	return b.String()
}

func postToSlackSimple(activity string) error {
	client := slack.New(slackBotToken)

	headerBlock := slack.NewHeaderBlock(slack.NewTextBlockObject(slack.PlainTextType, "🚀 GitHub PR Activity", true, false))

	lines := strings.Split(strings.TrimSpace(activity), "\n")

	commitCount := 0
	repoStats := make(map[string]int)
	for _, line := range lines {
		if strings.HasPrefix(line, "- ") {
			commitCount++
			if idx := strings.Index(line, "["); idx >= 0 {
				rest := line[idx+1:]
				if end := strings.Index(rest, "]"); end > 0 {
					repo := strings.TrimSpace(rest[:end])
					repoStats[repo]++
				}
			}
		}
	}

	var repoSummary []string
	for repo, count := range repoStats {
		repoSummary = append(repoSummary, fmt.Sprintf("%s (%d)", repo, count))
	}
	summaryText := fmt.Sprintf("*%d PRs* today", commitCount)
	if len(repoSummary) > 0 {
		summaryText += "\n\n*Top Repos:*\n"
		for i, rs := range repoSummary {
			if i >= 5 {
				break
			}
			summaryText += fmt.Sprintf("• %s\n", rs)
		}
		if len(repoSummary) > 5 {
			summaryText += fmt.Sprintf("_...and %d more_", len(repoSummary)-5)
		}
	}

	statsSection := slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, summaryText, false, false), nil, nil)

	divider := slack.NewDividerBlock()

	headerRow := slack.NewSectionBlock(nil, []*slack.TextBlockObject{
		slack.NewTextBlockObject(slack.MarkdownType, "*Repo*", false, false),
		slack.NewTextBlockObject(slack.MarkdownType, "*Author*", false, false),
		slack.NewTextBlockObject(slack.MarkdownType, "*Title*", false, false),
	}, nil)

	var blocks []slack.Block
	blocks = append(blocks, headerBlock, statsSection, divider, headerRow)

	for i, line := range lines {
		if i >= 15 {
			break
		}
		if !strings.HasPrefix(line, "- ") {
			continue
		}

		parts := strings.SplitN(line[2:], "]", 2)
		if len(parts) < 2 {
			continue
		}
		repo := strings.TrimSpace(parts[0])
		rest := parts[1]
		titleAuthor := strings.SplitN(rest, "(", 2)
		title := strings.TrimSpace(titleAuthor[0])
		author := ""
		if len(titleAuthor) > 1 {
			author = strings.TrimSpace(strings.TrimSuffix(titleAuthor[1], ")"))
		}

		if len(title) > 40 {
			title = title[:37] + "..."
		}

		row := slack.NewSectionBlock(nil, []*slack.TextBlockObject{
			slack.NewTextBlockObject(slack.MarkdownType, repo, false, false),
			slack.NewTextBlockObject(slack.MarkdownType, author, false, false),
			slack.NewTextBlockObject(slack.MarkdownType, "_"+title+"_", false, false),
		}, nil)
		blocks = append(blocks, row)
	}

	footerText := slack.NewTextBlockObject(slack.MarkdownType,
		fmt.Sprintf("_Generated by pico-bot at %s_", time.Now().Format("15:04 MST")), false, false)
	footerContext := slack.NewContextBlock("", footerText)
	blocks = append(blocks, divider, footerContext)

	_, _, err := client.PostMessage(slackChannel,
		slack.MsgOptionBlocks(blocks...),
		slack.MsgOptionText(fmt.Sprintf("GitHub PR Activity: %d PRs", commitCount), false))

	return err
}
