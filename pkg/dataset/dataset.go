package dataset

import "github.com/neuralmagic/nyann_poker/pkg/client"

// Dataset provides requests for the load generator.
// NextConversation returns a slice of message lists — one per turn.
// Single-turn datasets return a slice of length 1.
type Dataset interface {
	// NextConversation returns the messages for each turn of a conversation.
	// Turn i includes all messages from turns 0..i (context carry-forward).
	NextConversation() [][]client.Message
}
