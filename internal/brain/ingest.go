package brain

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/config"
)

type Core struct {
	storage    *Storage
	summarizer *LLMSummarizer
	cluster    *ClusterEngine
}

func NewCore(cfg config.Config, storageDir string) (*Core, error) {
	st, err := NewStorage(storageDir)
	if err != nil {
		return nil, err
	}
	
	sm := NewLLMSummarizer(cfg)
	cl := NewClusterEngine(st)
	
	return &Core{
		storage:    st,
		summarizer: sm,
		cluster:    cl,
	}, nil
}

// IngestData takes raw string data, creates a Node, summarizes it, links it, and saves it.
func (c *Core) IngestData(ctx context.Context, source, dataType, content string) (*Node, error) {
	node := &Node{
		ID:        GenerateID(source),
		Type:      dataType,
		Source:    source,
		CreatedAt: time.Now(),
		Content:   content,
	}

	summary, tags, err := c.summarizer.SummarizeToTurkish(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("failed to summarize data: %w", err)
	}

	node.Assessment = summary
	node.Tags = tags

	if err := c.storage.Save(node); err != nil {
		return nil, err
	}

	if err := c.cluster.LinkNode(ctx, node); err != nil {
		// Log error but don't fail the ingestion
		_ = err
	}

	return node, nil
}

// IngestGitHubRepo fetches the README or repo details using GitHub API and ingests it.
func (c *Core) IngestGitHubRepo(ctx context.Context, repo string) (*Node, error) {
	// Simple fetch of README to understand the repo
	// In a real system, you might fetch tree structure and multiple files.
	
	repo = strings.TrimPrefix(repo, "https://github.com/")
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/readme", repo)
	
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	
	// Add Github Token if available
	token := os.Getenv("GITHUB_TOKEN")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/vnd.github.v3.raw")
	
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github api request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api returned status: %d", resp.StatusCode)
	}
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	
	content := fmt.Sprintf("Repository: %s\n\nREADME:\n%s", repo, string(body))
	
	// Delegate to standard IngestData
	return c.IngestData(ctx, repo, "github_repo", content)
}

// QueryNodes returns nodes that match a query (e.g. tag search or ID lookup)
func (c *Core) QueryNodes(ctx context.Context, query string) ([]*Node, error) {
	all, err := c.storage.ListAll()
	if err != nil {
		return nil, err
	}
	
	query = strings.ToLower(query)
	var results []*Node
	
	for _, n := range all {
		if strings.Contains(strings.ToLower(n.ID), query) {
			results = append(results, n)
			continue
		}
		
		for _, t := range n.Tags {
			if strings.Contains(strings.ToLower(t), query) {
				results = append(results, n)
				break
			}
		}
	}
	
	return results, nil
}
