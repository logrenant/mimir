package brain

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Storage handles reading and writing Nodes to the filesystem.
type Storage struct {
	mu      sync.RWMutex
	dirPath string
}

// NewStorage initializes a new Storage engine for the brain nodes.
// It will create the directory if it doesn't exist.
func NewStorage(dirPath string) (*Storage, error) {
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create brain directory: %w", err)
	}
	return &Storage{
		dirPath: dirPath,
	}, nil
}

// Save writes a node to the filesystem.
func (s *Storage) Save(node *Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	md, err := node.FormatMarkdown()
	if err != nil {
		return err
	}

	filePath := filepath.Join(s.dirPath, fmt.Sprintf("%s.md", node.ID))
	if err := os.WriteFile(filePath, []byte(md), 0644); err != nil {
		return fmt.Errorf("failed to write node %s: %w", node.ID, err)
	}

	return nil
}

// Load reads a single node by ID.
func (s *Storage) Load(id string) (*Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filePath := filepath.Join(s.dirPath, fmt.Sprintf("%s.md", id))
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read node %s: %w", id, err)
	}

	node, err := ParseMarkdown(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse node %s: %w", id, err)
	}

	return node, nil
}

// ListAll returns all nodes currently in the brain.
func (s *Storage) ListAll() ([]*Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var nodes []*Node
	entries, err := os.ReadDir(s.dirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read brain directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".md")
		
		// Unlocked inner read since we already hold the read lock
		filePath := filepath.Join(s.dirPath, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue // skip unreadable files
		}

		node, err := ParseMarkdown(data)
		if err == nil {
			// For robustness, ensure ID matches filename
			node.ID = id
			nodes = append(nodes, node)
		}
	}

	return nodes, nil
}

// GenerateID produces a stable unique ID based on a source string and timestamp.
func GenerateID(source string) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s_%d", source, time.Now().UnixNano())))
	return fmt.Sprintf("node-%x", hash[:8])
}
