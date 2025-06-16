package main

import (
	"log"
	"os"
	"testing"

	"github.com/joho/godotenv"
)

func TestMain(m *testing.M) {
	// Load environment variables from .env file
	if err := godotenv.Load(); err != nil {
		log.Fatalf("Error loading .env file: %v", err)
	}

	// Load configuration from environment variables
	loadConfig()

	// Run the tests
	exitCode := m.Run()

	// Exit with the appropriate code
	os.Exit(exitCode)
}

func TestSendSlackMessage(t *testing.T) {
	slackChannelID = "C058MADPRG9" // Test channel ID

	postToSlack("Test message from unit test", slackChannelID)
	if err := postToSlack("Test message from unit test", slackChannelID); err != nil {
		t.Errorf("Failed to send message to Slack: %v", err)
	}
}
