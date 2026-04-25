// Package a2a defines the A2AAPI view-scoped accessor for agent-to-agent
// signed-card exchanges (a2a-signed-cards-trust mission).
package a2a

import "context"

// Card is a reference-only A2A card metadata record.
type Card struct {
	ID       string `json:"id"`
	Issuer   string `json:"issuer"`
	Subject  string `json:"subject"`
	IssuedAt string `json:"issuedAt"`
}

// A2AAPI is the view-scoped accessor for A2A card listing and streams.
type A2AAPI interface {
	ListCards(ctx context.Context) ([]Card, error)
	StartStream(ctx context.Context) (subscriptionID string, err error)
	StopStream(ctx context.Context, subscriptionID string) error
}
