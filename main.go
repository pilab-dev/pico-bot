package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
	"github.com/google/go-github/v59/github"
	"github.com/joho/godotenv"
	"github.com/slack-go/slack"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
)

// --- CONFIGURATION ---
// These are read from environment variables for security.
var (
	slackBotToken  string
	slackChannelID string
	githubToken    string
	githubOrg      string
	geminiAPIKey   string
)

// loadConfig loads all necessary configuration from environment variables.
// It will panic if any of the required variables are not set.
func loadConfig() {
	slackBotToken = os.Getenv("SLACK_BOT_TOKEN")
	slackChannelID = os.Getenv("SLACK_CHANNEL_ID")
	githubToken = os.Getenv("GITHUB_TOKEN")
	githubOrg = os.Getenv("GITHUB_ORG")
	geminiAPIKey = os.Getenv("GEMINI_API_KEY")

	if slackBotToken == "" || slackChannelID == "" || githubToken == "" || githubOrg == "" || geminiAPIKey == "" {
		log.Fatal("Error: One or more required environment variables are not set.\n" +
			"Please set SLACK_BOT_TOKEN, SLACK_CHANNEL_ID, GITHUB_TOKEN, GITHUB_ORG, and GEMINI_API_KEY.")
	}
}

func main() {
	godotenv.Load()

	loadConfig()

	// Create a context for our API calls
	ctx := context.Background()

	githubOrgs := []string{
		"pilab-dev",
		"pilab-cloud",
	}

	commitLog := ""
	regularPRs := ""
	dependabotPRs := ""

	log.Println("Step 1: Fetching recent activity from GitHub organizations")
	// 1. Fetch GitHub Activities
	for _, org := range githubOrgs {
		fmt.Println("  Fetching activity for", org)
		commitActivity, err := fetchOrgActivity(ctx, org)
		if err != nil {
			log.Fatalf("Failed to fetch GitHub activity: %v", err)
		}
		if commitActivity != "" {
			commitLog += fmt.Sprintf("\nOrganization: %s\n\n%s", org, commitActivity)
		}

		// Fetch pull requests
		regularPrActivity, dependabotPrActivity, err := fetchOrgPullRequests(ctx, org)
		if err != nil {
			log.Fatalf("Failed to fetch GitHub pull requests: %v", err)
		}
		if regularPrActivity != "" {
			regularPRs += fmt.Sprintf("\nOrganization: %s\n\n%s", org, regularPrActivity)
		}
		if dependabotPrActivity != "" {
			dependabotPRs += fmt.Sprintf("\nOrganization: %s\n\n%s", org, dependabotPrActivity)
		}
	}

	summary := ""
	if commitLog == "" && regularPRs == "" && dependabotPRs == "" {
		log.Println("No new activity in the last 24 hours.")
		summary = "No new commits or open pull requests in the last 24 hours. A good day to plan and refactor! 🤔"
	} else {
		// 2. Summarize with Gemini
		log.Println("Step 2: Summarizing activity with Gemini...")
		var promptBuilder strings.Builder
		promptBuilder.WriteString("You are a helpful project manager bot. For a daily team update, summarize the following GitHub activity. ")
		promptBuilder.WriteString("Organize the summary by repository. Use markdown lists and bold text to make it readable. ")
		promptBuilder.WriteString("Only use Slack-supported markdown: *bold*, _italic_, ~strikethrough~, lists (- item), blockquotes (>), and inline code (`code`). ")
		promptBuilder.WriteString("Do not use headings (#), tables, or HTML.\n\n")

		if commitLog != "" {
			promptBuilder.WriteString("First, summarize the recent commits under a '*Recent Commits*' heading.\n")
		}
		if regularPRs != "" {
			promptBuilder.WriteString("Next, list all open pull requests under a '*Pull Requests*' heading.\n")
		}
		if dependabotPRs != "" {
			promptBuilder.WriteString("Finally, list all open Dependabot pull requests under a '*Dependabot Updates*' heading.\n")
		}

		promptBuilder.WriteString("\nHere is the raw data:\n")

		if commitLog != "" {
			promptBuilder.WriteString("\n--- Commit Log ---\n")
			promptBuilder.WriteString(commitLog)
		}
		if regularPRs != "" {
			promptBuilder.WriteString("\n--- Open Pull Requests ---\n")
			promptBuilder.WriteString(regularPRs)
		}
		if dependabotPRs != "" {
			promptBuilder.WriteString("\n--- Dependabot Pull Requests ---\n")
			promptBuilder.WriteString(dependabotPRs)
		}

		prompt := promptBuilder.String()

		os.WriteFile("prompt.txt", []byte(prompt), 0o644)

		var err error
		summary, err = getGeminiResponse(ctx, prompt)
		if err != nil {
			log.Fatalf("Failed to get summary from Gemini: %v", err)
		}
	}

	// 3. Get Tip of the Day from Gemini
	log.Println("Step 3: Getting a Tip of the Day from Gemini...")
	tipPrompt := "Give me one useful, short tip of the day for a programmer or DevOps engineer. Just the tip, no extra intro."
	tip, err := getGeminiResponse(ctx, tipPrompt)
	if err != nil {
		// Don't fail the whole process if we can't get a tip
		log.Printf("Could not fetch tip of the day: %v. Proceeding without it.", err)
		tip = "Could not fetch a tip today, but keep learning and building!"
	}

	os.WriteFile("summary.txt", []byte(summary), 0o644)
	os.WriteFile("tip.txt", []byte(tip), 0o644)

	log.Println("Summary and tip saved to summary.txt and tip.txt")

	// 4. Build and Post Slack Message
	log.Println("Step 4: Building and posting the message to Slack channel:", slackChannelID)
	err = postToSlack(summary, tip)
	if err != nil {
		log.Fatalf("Failed to post message to Slack: %v", err)
	}

	log.Println("✅ Successfully posted daily summary to Slack!")
}

// fetchOrgActivity retrieves commits from all repositories in a GitHub organization from the last 24 hours.
func fetchOrgActivity(ctx context.Context, org string) (string, error) {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: githubToken})
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	repos, _, err := client.Repositories.ListByOrg(ctx, org, &github.RepositoryListByOrgOptions{Type: "all"})
	if err != nil {
		return "", fmt.Errorf("listing repositories for org %s: %w", org, err)
	}

	var activityBuilder strings.Builder
	since := time.Now().Add(-24 * time.Hour)

	for _, repo := range repos {
		commits, _, err := client.Repositories.ListCommits(ctx, org, *repo.Name, &github.CommitsListOptions{Since: since})
		if err != nil {
			log.Printf("Could not fetch commits for repo %s: %v. Skipping.", *repo.Name, err)
			continue
		}

		if len(commits) > 0 {
			for _, commit := range commits {
				// Format: "Repo: [repo-name], Author: [author-name], Message: [commit-message]"
				line := fmt.Sprintf("Repo: %s, Author: %s, Message: %s\n",
					*repo.Name,
					commit.GetCommit().GetAuthor().GetName(),
					commit.GetCommit().GetMessage(),
				)

				activityBuilder.WriteString(line)
			}
		}
	}

	return activityBuilder.String(), nil
}

// fetchOrgPullRequests retrieves open pull requests from all repositories in a GitHub organization,
// separating them into regular and Dependabot PRs.
func fetchOrgPullRequests(ctx context.Context, org string) (string, string, error) {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: githubToken})
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	repos, _, err := client.Repositories.ListByOrg(ctx, org, &github.RepositoryListByOrgOptions{Type: "all"})
	if err != nil {
		return "", "", fmt.Errorf("listing repositories for org %s: %w", org, err)
	}

	var regularPrBuilder strings.Builder
	var dependabotPrBuilder strings.Builder

	for _, repo := range repos {
		pulls, _, err := client.PullRequests.List(ctx, org, *repo.Name, &github.PullRequestListOptions{State: "open"})
		if err != nil {
			log.Printf("Could not fetch pull requests for repo %s: %v. Skipping.", *repo.Name, err)
			continue
		}

		if len(pulls) > 0 {
			for _, pr := range pulls {
				author := pr.GetUser().GetLogin()
				line := fmt.Sprintf("Repo: %s, Author: %s, Title: %s\n",
					*repo.Name,
					author,
					pr.GetTitle(),
				)

				if strings.Contains(author, "dependabot") {
					dependabotPrBuilder.WriteString(line)
				} else {
					regularPrBuilder.WriteString(line)
				}
			}
		}
	}

	return regularPrBuilder.String(), dependabotPrBuilder.String(), nil
}

// getGeminiResponse sends a prompt to the Gemini API and returns the text response.
func getGeminiResponse(ctx context.Context, prompt string) (string, error) {
	client, err := genai.NewClient(ctx, option.WithAPIKey(geminiAPIKey))
	if err != nil {
		return "", err
	}
	defer client.Close()

	model := client.GenerativeModel("gemini-2.0-flash-lite")
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return "", err
	}

	// Extract text from the response
	var responseText strings.Builder
	for _, cand := range resp.Candidates {
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				if txt, ok := part.(genai.Text); ok {
					responseText.WriteString(string(txt))
				}
			}
		}
	}

	return responseText.String(), nil
}

// postToSlack constructs a message using Slack's Block Kit and posts it to the configured channel.
func postToSlack(summary, tip string) error {
	client := slack.New(slackBotToken)

	// --- Block Kit Message Construction ---
	headerText := slack.NewTextBlockObject(slack.PlainTextType, "🚀 Daily GitHub Digest 🚀", true, false)
	headerBlock := slack.NewHeaderBlock(headerText)

	summaryText := slack.NewTextBlockObject(slack.MarkdownType, summary, false, false)
	summarySection := slack.NewSectionBlock(summaryText, nil, nil)

	dividerBlock := slack.NewDividerBlock()

	tipHeaderText := slack.NewTextBlockObject(slack.MarkdownType, "💡 *Tip of the Day*", false, false)
	tipHeaderSection := slack.NewSectionBlock(tipHeaderText, nil, nil)

	tipText := slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf(">%s", tip), false, false)
	tipSection := slack.NewSectionBlock(tipText, nil, nil)

	footerText := slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("Report generated on %s", time.Now().Format("Jan 02, 2006")), false, false)
	footerContext := slack.NewContextBlock("", footerText)

	// Post the message to the channel
	_, _, err := client.PostMessage(
		slackChannelID,
		slack.MsgOptionBlocks(
			headerBlock,
			summarySection,
			dividerBlock,
			tipHeaderSection,
			tipSection,
			dividerBlock,
			footerContext,
		),
		// slack.MsgOptionAttachments(
		// 	slack.Attachment{
		// 		Color:       "#36a64f",
		// 		Title:       "maci",
		// 		ServiceName: "GitHub Digest",
		// 		Footer:      "Powered by Gemini and Slack",
		// 		FooterIcon:  "https://platform.slack-edge.com/img/default_application_icon.png",
		// 	},
		// ),
		// Provide a plain-text fallback for notifications
		slack.MsgOptionText(fmt.Sprintf("Daily GitHub Digest: %s", summary), false),
	)

	return err
}
