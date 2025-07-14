package utils

import (
	"bytes"
	"encoding/json"
	"gamerpal/internal/config"
	"io"
	"net/http"
)

type ModelsClient struct {
	config *config.Config
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ModelsRequest struct {
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	TopP        float64   `json:"top_p"`
	Model       string    `json:"model"`
}

type ModelsResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

func NewModelsClient(cfg *config.Config) *ModelsClient {
	return &ModelsClient{
		config: cfg,
	}
}

// ModelsRequest sends a request to github models with some predefined settings.
func (m *ModelsClient) ModelsRequest(systemPrompt, userPrompt string, model string) string {
	modelsURL := "https://models.github.ai/inference/chat/completions"
	modelsToken := m.config.GetGitHubModelsToken()

	// create request payload
	reqPayload := ModelsRequest{
		Messages: []Message{
			{
				Role:    "system",
				Content: systemPrompt,
			},
			{
				Role:    "user",
				Content: userPrompt,
			},
		},
		Temperature: 1,
		TopP:        1,
		Model:       model,
	}

	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		return ""
	}

	client := &http.Client{}

	req, err := http.NewRequest("POST", modelsURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return ""
	}

	// set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+modelsToken)

	response, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return ""
	}

	var modelsResp ModelsResponse
	if err := json.Unmarshal(body, &modelsResp); err != nil {
		return ""
	}

	if len(modelsResp.Choices) > 0 {
		return modelsResp.Choices[0].Message.Content
	}

	return ""
}
