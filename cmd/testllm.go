package cmd

import (
	"fmt"
	"log"
	"time"

	"github.com/astrica1/gollama"
	"github.com/spf13/cobra"
)

var testLLMCmd = &cobra.Command{
	Use:   "testllm",
	Short: "Test local LLM connection",
	Long:  `Tests connection to LM Studio or Ollama. Use OLLAMA_BASE_URL env var to override.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		initConfig()

		log.Printf("Testing LLM at %s...", ollamaURL)
		log.Printf("Model: %s", ollamaModel)

		client, err := gollama.NewClient(ollamaURL)
		if err != nil {
			return fmt.Errorf("failed to create client: %w", err)
		}

		ctx2, cancel := contextWithTimeout(30 * time.Second)
		defer cancel()

		models, err := client.List(ctx2)
		if err != nil {
			return fmt.Errorf("failed to list models: %w", err)
		}

		if models == nil || len(models.Models) == 0 {
			return fmt.Errorf("no models found")
		}

		log.Printf("Found %d models:", len(models.Models))
		for _, m := range models.Models {
			fmt.Printf("  - %s\n", m.Name)
		}

		testModel := ollamaModel
		found := false
		for _, m := range models.Models {
			if m.Name == testModel {
				found = true
				break
			}
		}
		if !found {
			testModel = models.Models[0].Name
			log.Printf("Model '%s' not found, using: %s", ollamaModel, testModel)
		}

		log.Printf("\nTesting generation with model: %s...", testModel)
		resp, err := client.Generate(ctx2, &gollama.GenerateRequest{
			Model:  testModel,
			Prompt: "Say 'Hello from pico-bot!' in exactly 3 words.",
		})
		if err != nil {
			return fmt.Errorf("generation failed: %w", err)
		}

		log.Printf("\n✅ LLM Response: %s", resp.Response)
		return nil
	},
}
