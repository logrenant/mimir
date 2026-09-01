package tools

import (
	"testing"

	"github.com/logrenant/goat-mcp/internal/mcp"
)

// task-13 DoD: every tool response the registry can emit must be recognisable by
// the SD-2/SD-7 choke-point (finalizeResponse) — either MetadataResponse or
// RefinedResponse. A plain struct fails closed, so a new tool that forgets the
// marker cannot silently bypass isolation. Add every tool response type here.
func TestEveryToolResponseIsGateable(t *testing.T) {
	cases := []struct {
		name string
		v    any
	}{
		{"web_search", webSearchResponse{}},
		{"fetch_page", fetchPageResponse{}},
		{"research", researchResponse{}},
		{"diagnostics", diagnosticsResponse{}},
		{"ecommerce_product_lookup", ecommerceLookupResponse{}},
		{"tiktok_profile_lookup", tikTokLookupResponse{}},
		{"gmaps_business_lookup", gmapsLookupResponse{}},
		{"instagram_profile_lookup", instagramLookupResponse{}},
		{"maps_search", mapsSearchResponse{}},
		{"project_context", projectContextResponse{}},
		{"context_recall", contextRecallResponse{}},
		{"context_remember", contextRememberResponse{}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, meta := c.v.(mcp.MetadataResponse)
			_, refined := c.v.(mcp.RefinedResponse)
			if !meta && !refined {
				t.Fatalf("%s response %T implements neither mcp.MetadataResponse nor mcp.RefinedResponse — it would fail the choke-point", c.name, c.v)
			}
		})
	}
}
