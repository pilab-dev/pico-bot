package cmd

import (
	"context"
	"log"
	"os"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

var (
	cfg           *viper.Viper
	ctx           context.Context
	slackBotToken string
	slackChannel  string
	githubToken   string
	githubOrg     string
	githubOrgs    string
	geminiKey     string
	ollamaURL     string
	ollamaModel   string
)

func initConfig() {
	_ = godotenv.Load()

	cfg = viper.New()
	cfg.SetConfigName(".env")
	cfg.SetConfigType("env")
	cfg.AddConfigPath(".")
	cfg.AddConfigPath("$HOME/.config/pico-bot")
	_ = cfg.ReadInConfig()

	cfg.SetDefault("OLLAMA_BASE_URL", "http://localhost:11434")
	cfg.SetDefault("OLLAMA_MODEL", "llama3")
	cfg.SetDefault("REPORT_TOP_N", 5)
	cfg.SetDefault("REPORT_TODAY_ONLY", true)

	slackBotToken = getEnv("SLACK_BOT_TOKEN", "")
	slackChannel = getEnv("SLACK_CHANNEL_ID", "")
	githubToken = getEnv("GITHUB_TOKEN", "")
	githubOrg = getEnv("GITHUB_ORG", "pilab-dev")
	githubOrgs = getEnv("GITHUB_ORGS", "pilab-dev,pilab-cloud")
	geminiKey = getEnv("GEMINI_API_KEY", "")
	ollamaURL = getEnv("OLLAMA_BASE_URL", "http://localhost:11434")
	ollamaModel = getEnv("OLLAMA_MODEL", "llama3")

	if slackBotToken == "" {
		log.Fatal("SLACK_BOT_TOKEN is required")
	}
	if slackChannel == "" {
		log.Fatal("SLACK_CHANNEL_ID is required")
	}

	ctx = context.Background()
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	if cfg != nil {
		if v := cfg.GetString(key); v != "" {
			return v
		}
	}
	return fallback
}
