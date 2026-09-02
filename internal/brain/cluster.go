package brain

import (
	"context"
)

// ClusterEngine assigns related_nodes based on tag overlap and potentially semantic similarity.
type ClusterEngine struct {
	storage *Storage
}

// NewClusterEngine creates a new clustering engine.
func NewClusterEngine(storage *Storage) *ClusterEngine {
	return &ClusterEngine{
		storage: storage,
	}
}

// LinkNode finds related nodes for the given node and updates the links bidirectionally.
func (c *ClusterEngine) LinkNode(ctx context.Context, target *Node) error {
	allNodes, err := c.storage.ListAll()
	if err != nil {
		return err
	}

	targetTagSet := make(map[string]bool)
	for _, t := range target.Tags {
		targetTagSet[t] = true
	}

	var newRelated []string

	for _, n := range allNodes {
		if n.ID == target.ID {
			continue
		}

		// Simple Jaccard-like check: if they share at least 1 tag, link them.
		// For a more advanced setup, this could use embeddings or an LLM call.
		shared := 0
		for _, t := range n.Tags {
			if targetTagSet[t] {
				shared++
			}
		}

		if shared > 0 {
			newRelated = append(newRelated, n.ID)
			
			// Update the other node too
			alreadyLinked := false
			for _, rn := range n.RelatedNodes {
				if rn == target.ID {
					alreadyLinked = true
					break
				}
			}
			if !alreadyLinked {
				n.RelatedNodes = append(n.RelatedNodes, target.ID)
				_ = c.storage.Save(n) // Ignoring error to continue linking
			}
		}
	}

	target.RelatedNodes = append(target.RelatedNodes, newRelated...)
	
	// Deduplicate
	dedup := make(map[string]bool)
	var finalRelated []string
	for _, id := range target.RelatedNodes {
		if !dedup[id] && id != target.ID {
			dedup[id] = true
			finalRelated = append(finalRelated, id)
		}
	}
	target.RelatedNodes = finalRelated

	return c.storage.Save(target)
}
