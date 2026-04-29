package cmd

import (
	"testing"
	"time"
)

func TestFetchGitHubOrgWork(t *testing.T) {
	t.Skip("Skipping to avoid API calls - use cached github_work.json")

	today := time.Now().Format("2006-01-02")
	t.Logf("Fetching PRs created >= %s", today)

	orgs := []string{"pilab-dev", "pilab-cloud"}

	output, err := fetchGitHubOrgWork(orgs)
	if err != nil {
		t.Fatalf("fetchGitHubOrgWork failed: %v", err)
	}

	if output == "" {
		t.Fatal("output is empty")
	}

	_ = output
}
