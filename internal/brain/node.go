package brain

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Node represents a single piece of knowledge in the Mimir Nervous System.
type Node struct {
	ID           string    `yaml:"id"`
	Type         string    `yaml:"type"` // e.g. "github_repo", "chat_session", "research", "note"
	Source       string    `yaml:"source"`
	Tags         []string  `yaml:"tags"`
	RelatedNodes []string  `yaml:"related_nodes"`
	CreatedAt    time.Time `yaml:"created_at"`
	
	// These are not in YAML frontmatter, they represent the body
	Assessment   string    `yaml:"-"` // 1 paragraph Turkish summary
	Content      string    `yaml:"-"` // The raw/structural data
}

// FormatMarkdown serializes the node into a Zettelkasten-style Markdown file with YAML frontmatter.
func (n *Node) FormatMarkdown() (string, error) {
	var buf bytes.Buffer

	buf.WriteString("---\n")
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(n); err != nil {
		return "", fmt.Errorf("failed to encode node yaml: %w", err)
	}
	buf.WriteString("---\n\n")

	buf.WriteString("# Değerlendirme\n")
	buf.WriteString(strings.TrimSpace(n.Assessment) + "\n\n")

	buf.WriteString("# İçerik\n")
	buf.WriteString(strings.TrimSpace(n.Content) + "\n")

	return buf.String(), nil
}

// ParseMarkdown deserializes a Zettelkasten-style Markdown file into a Node.
func ParseMarkdown(data []byte) (*Node, error) {
	strData := string(data)
	if !strings.HasPrefix(strData, "---") {
		return nil, fmt.Errorf("invalid node format: missing yaml frontmatter")
	}

	parts := strings.SplitN(strData, "---", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid node format: malformed frontmatter")
	}

	yamlContent := parts[1]
	bodyContent := parts[2]

	var node Node
	if err := yaml.Unmarshal([]byte(yamlContent), &node); err != nil {
		return nil, fmt.Errorf("failed to parse yaml frontmatter: %w", err)
	}

	// Simple heuristic to split Assessment and Content
	bodyContent = strings.TrimSpace(bodyContent)
	assessmentPrefix := "# Değerlendirme"
	contentPrefix := "# İçerik"

	if strings.HasPrefix(bodyContent, assessmentPrefix) {
		contentIdx := strings.Index(bodyContent, contentPrefix)
		if contentIdx != -1 {
			node.Assessment = strings.TrimSpace(bodyContent[len(assessmentPrefix):contentIdx])
			node.Content = strings.TrimSpace(bodyContent[contentIdx+len(contentPrefix):])
		} else {
			node.Assessment = strings.TrimSpace(bodyContent[len(assessmentPrefix):])
		}
	} else {
		node.Content = bodyContent
	}

	return &node, nil
}
