package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alex-ilgayev/secfeed/pkg/config"
	"github.com/alex-ilgayev/secfeed/pkg/llm/ollama"
	"github.com/alex-ilgayev/secfeed/pkg/llm/openai"
	"github.com/alex-ilgayev/secfeed/pkg/types"
	log "github.com/sirupsen/logrus"
)

const (
	embeddingsMaxTextLength = 8000
	llmInputMaxTextLength   = 40000

	llmMaxCompletionTokens = 2000
)

type LLMClientType string

// Implementing the pflag.Value interface for LLMClientType
func (l *LLMClientType) String() string {
	return string(*l)
}

// Implementing the pflag.Value interface for LLMClientType
func (l *LLMClientType) Set(value string) error {
	*l = LLMClientType(value)
	return nil
}

// Implementing the pflag.Value interface for LLMClientType
func (l *LLMClientType) Type() string {
	return "llmClientType"
}

const (
	OpenAI LLMClientType = "openai"
	Ollama LLMClientType = "ollama"
)

type LLMClient interface {
	ChatCompletion(ctx context.Context, model string, systemMsg, userMsg string, temperature float32, maxTokens int, jsonFormat bool) (string, error)
}

// Client is a generic interface that wraps OpenAI at the moment.
type Client struct {
	client         LLMClient
	modelFiltering string
	modelSummary   string
}

func NewClient(ctx context.Context, llmType LLMClientType, modelFiltering, modelSummary string) (*Client, error) {
	var llmClient LLMClient
	var err error

	switch llmType {
	case OpenAI:
		llmClient, err = openai.NewClient()
		if err != nil {
			return nil, fmt.Errorf("failed to create OpenAI client: %w", err)
		}
	case Ollama:
		llmClient, err = ollama.NewClient(ctx, []string{modelFiltering, modelSummary})
		if err != nil {
			return nil, fmt.Errorf("failed to create Ollama client: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported LLM client: %s", llmClient)
	}

	return &Client{
		client:         llmClient,
		modelFiltering: modelFiltering,
		modelSummary:   modelSummary,
	}, nil
}

// Not used at the moment.
func (c *Client) ExtractCategories(ctx context.Context, article types.Article) ([]string, error) {
	systemPrompt := `Extract key categories and topics from this article. Return a JSON array of strings without markdown format with no explanation.`
	userPrompt := fmt.Sprintf("Title: %s\nContent: %s\nCategories: %v", article.Title, article.Content, article.Categories)

	if len(userPrompt)+len(systemPrompt) > llmInputMaxTextLength {
		return nil, fmt.Errorf("input text for category extraction is too long (%d)", len(userPrompt)+len(systemPrompt))
	}

	resp, err := c.client.ChatCompletion(
		ctx,
		c.modelFiltering,
		systemPrompt,
		userPrompt,
		0.2, // Low temperature for more deterministic results
		llmMaxCompletionTokens,
		true,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to extract categories: %w", err)
	}

	var categories []string
	err = json.Unmarshal([]byte(resp), &categories)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal categories: %w", err)
	}

	log.WithFields(log.Fields{"categories": categories}).Debug("Extracted categories")

	return categories, nil
}

func (c *Client) Summarize(ctx context.Context, article types.Article) (string, error) {
	systemPrompt := `You are an AI assistant specialized in summarizing articles. Your task is to generate concise, accurate, and clear summaries of the content. When given a article, follow these guidelines:

1. Accuracy and Fidelity: Extract and convey the key points, methodologies, results, and conclusions as presented in the original text without introducing new interpretations.
2. Clarity and Brevity: Create summaries that are succinct and understandable even for complex topics. Use plain language and avoid unnecessary jargon.
3. Structure: Organize the summary logically. Consider using bullet points or short paragraphs to highlight:
   - The main objective or problem addressed.
   - The methodology or approach taken.
   - Key findings and results.
   - Conclusions or implications.
4. Neutrality: Maintain an objective tone. Do not include personal opinions or commentary.
5. Adaptability: Adjust the level of detail based on the article’s complexity and length. For highly technical or detailed articles, ensure the summary captures essential data without oversimplification.
6. Uncertainty: If certain parts of the article are ambiguous or contain conflicting information, note these uncertainties clearly in the summary.

Your goal is to help readers quickly grasp the essence of the articles while preserving the integrity of the original content.`
	userPrompt := fmt.Sprintf("Title: %s\nContent: %s\nCategories: %v", article.Title, article.Content, article.Categories)

	if len(systemPrompt)+len(userPrompt) > llmInputMaxTextLength {
		return "", fmt.Errorf("input text for summarization is too long (%d)", len(systemPrompt)+len(userPrompt))
	}

	resp, err := c.client.ChatCompletion(
		ctx,
		c.modelSummary,
		systemPrompt,
		userPrompt,
		0.5,
		llmMaxCompletionTokens,
		false,
	)
	if err != nil {
		return "", fmt.Errorf("failed to summarize article: %w", err)
	}

	return resp, nil
}

func (c *Client) CategoryMatching(ctx context.Context, categoriesToMatch []config.Category, article types.Article) ([]types.CategoryRelevance, error) {
	systemPrompt := `You have a list of categories to evaluate. 
For each category, determine how relevant the user's article is to that category. 

Scoring:
- A relevance score on a scale of 0 to 10, where 0 means “no connection” and 10 means “highly relevant.”
- Provide a short explanation for the assigned score.

Output must be valid JSON without markdown formatting. Return an array of objects, where each object has:
{
	"category": "<category name>",
	"relevance": <integer from 0 to 10>,
	"explanation": "<brief explanation>"
}

Categories:
`
	for i, cat := range categoriesToMatch {
		systemPrompt += fmt.Sprintf("%d. %s: %s\n", i+1, cat.Name, cat.Description)
	}

	userPrompt := fmt.Sprintf("Title: %s\nDescription: %s\nLink: %s\nContent: %s\n", article.Title, article.Description, article.Link, article.Content)
	userPrompt = "Below is the article to evaluate:\n\n" + userPrompt

	if len(systemPrompt)+len(userPrompt) > llmInputMaxTextLength {
		return nil, fmt.Errorf("input text for category matching is too long (%d)", len(systemPrompt)+len(userPrompt))
	}

	resp, err := c.client.ChatCompletion(
		ctx,
		c.modelFiltering,
		systemPrompt,
		userPrompt,
		0, // Low temperature for more deterministic results
		llmMaxCompletionTokens,
		true,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to match categories: %w", err)
	}

	var relevance []types.CategoryRelevance
	err = json.Unmarshal([]byte(resp), &relevance)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal relevance scores: %w", err)
	}

	return relevance, nil
}

// var (
// 	embeddingsChunkSize = 1000
// 	embeddingsOverlap   = 200
// )

// // callEmbeddingAPI is a helper that sends texts to the OpenAI API without checking length.
// func (c *Client) callEmbeddingAPI(ctx context.Context, texts []string) ([][]float32, error) {
// 	req := openai.EmbeddingRequest{
// 		Model: embeddingModel,
// 		Input: texts,
// 	}

// 	resp, err := c.client.CreateEmbeddings(ctx, req)
// 	if err != nil {
// 		return nil, err
// 	}

// 	c.tokenUsed[string(embeddingModel)].add(tokenUsed{
// 		prompt: resp.Usage.PromptTokens,
// 	})
// 	log.WithFields(log.Fields{"model": resp.Model, "tokens": resp.Usage.TotalTokens, "total_cost": c.totalCost()}).Debug("OpenAI API CreateEmbeddings call")

// 	if len(resp.Data) != len(texts) {
// 		return nil, fmt.Errorf("number of embeddings returned does not match number of texts")
// 	}

// 	embeddings := make([][]float32, len(texts))
// 	for i, embedding := range resp.Data {
// 		embeddings[i] = embedding.Embedding
// 	}

// 	return embeddings, nil
// }

// // Embedding computes embeddings for each text in the input slice.
// // If a text exceeds embeddingsMaxTextLength, it will be split into smaller chunks,
// // embeddings for each chunk will be computed, and then averaged.
// func (c *Client) Embedding(ctx context.Context, texts []string) ([][]float32, error) {
// 	results := make([][]float32, len(texts))
// 	for i, text := range texts {
// 		// If the text is within the maximum allowed length, process directly.
// 		if len(text) <= embeddingsMaxTextLength {
// 			embs, err := c.callEmbeddingAPI(ctx, []string{text})
// 			if err != nil {
// 				return nil, err
// 			}
// 			results[i] = embs[0]
// 		} else {
// 			// For texts that are too long, split into chunks.
// 			chunks := chunkText(text, embeddingsChunkSize, embeddingsOverlap)
// 			chunkEmbeddings, err := c.callEmbeddingAPI(ctx, chunks)
// 			if err != nil {
// 				return nil, err
// 			}

// 			// Average the embeddings of the chunks.
// 			avgEmbedding, err := averageEmbeddings(chunkEmbeddings)
// 			if err != nil {
// 				return nil, err
// 			}
// 			results[i] = avgEmbedding
// 		}
// 	}

// 	return results, nil
// }

// func (c *Client) Embedding(ctx context.Context, texts []string) ([][]float32, error) {
// 	for _, text := range texts {
// 		if len(text) > embeddingsMaxTextLength {
// 			return nil, fmt.Errorf("text is too long (%d)", len(text))
// 		}
// 	}

// 	req := openai.EmbeddingRequest{
// 		Model: embeddingModel,
// 		Input: texts,
// 	}

// 	resp, err := c.client.CreateEmbeddings(ctx, req)
// 	if err != nil {
// 		return nil, err
// 	}

// 	c.tokenUsed[string(embeddingModel)].add(tokenUsed{
// 		prompt: resp.Usage.PromptTokens,
// 	})
// 	log.WithFields(log.Fields{"model": resp.Model, "tokens": resp.Usage.TotalTokens, "total_cost": c.totalCost()}).Debug("OpenAI API CreateEmbeddings call")

// 	if len(resp.Data) != len(texts) {
// 		return nil, fmt.Errorf("number of embeddings returned does not match number of texts")
// 	}

// 	embeddings := make([][]float32, len(texts))
// 	for i, embedding := range resp.Data {
// 		embeddings[i] = embedding.Embedding
// 	}

// 	return embeddings, nil
// }

// // chunkText splits a text into chunks of at most chunkSize characters with a given overlap.
// func chunkText(text string, chunkSize, overlap int) []string {
// 	var chunks []string
// 	runes := []rune(text)
// 	n := len(runes)
// 	start := 0
// 	for start < n {
// 		end := start + chunkSize
// 		if end > n {
// 			end = n
// 		}
// 		chunks = append(chunks, string(runes[start:end]))
// 		// Move forward by chunkSize-overlap to allow overlapping.
// 		start += (chunkSize - overlap)
// 	}
// 	return chunks
// }

// // averageEmbeddings calculates the element-wise average of the provided embeddings.
// // All embeddings must have the same dimension.
// func averageEmbeddings(embeddings [][]float32) ([]float32, error) {
// 	if len(embeddings) == 0 {
// 		return nil, errors.New("no embeddings provided")
// 	}

// 	dim := len(embeddings[0])
// 	avg := make([]float32, dim)
// 	count := float32(len(embeddings))

// 	for _, emb := range embeddings {
// 		if len(emb) != dim {
// 			return nil, errors.New("embeddings have inconsistent dimensions")
// 		}
// 		for i, value := range emb {
// 			avg[i] += value
// 		}
// 	}

// 	for i := range avg {
// 		avg[i] /= count
// 	}

// 	return avg, nil
// }

// // weightedAverageEmbeddings calculates the element-wise weighted average of embeddings.
// // The weights slice should have the same length as embeddings and its values don't have to sum to 1.
// func weightedAverageEmbeddings(embeddings [][]float32, weights []float32) ([]float32, error) {
// 	if len(embeddings) == 0 {
// 		return nil, errors.New("no embeddings provided")
// 	}
// 	if len(embeddings) != len(weights) {
// 		return nil, errors.New("number of weights must match number of embeddings")
// 	}

// 	dim := len(embeddings[0])
// 	weightedAvg := make([]float32, dim)
// 	var totalWeight float32

// 	// Normalize weights and aggregate
// 	for idx, emb := range embeddings {
// 		if len(emb) != dim {
// 			return nil, errors.New("embeddings have inconsistent dimensions")
// 		}
// 		totalWeight += weights[idx]
// 		for i, value := range emb {
// 			weightedAvg[i] += value * weights[idx]
// 		}
// 	}

// 	// Normalize by the total weight
// 	if totalWeight == 0 {
// 		return nil, errors.New("total weight is zero")
// 	}

// 	for i := range weightedAvg {
// 		weightedAvg[i] /= totalWeight
// 	}

// 	return weightedAvg, nil
// }
