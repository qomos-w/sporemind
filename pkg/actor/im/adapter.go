package im

import (
	"context"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Provider is the transport abstraction every IM platform adapter implements
// (telegram first; feishu / wecom / dingtalk follow). Implementations must be
// stateless apart from per-call local state: one registry entry serves every
// account, and account credentials arrive as call arguments.
type Provider interface {
	// ID returns the canonical provider key used by ImAccount.Provider
	// (e.g. "telegram").
	ID() string

	// Name returns the human-readable provider name for status surfaces.
	Name() string

	// Probe verifies account connectivity (token validity, API reachability)
	// without starting the receive loop.
	Probe(ctx context.Context, account gen.ImAccount) error

	// Run starts the inbound receive loop for one account, forwarding received
	// messages to inbound until ctx is cancelled. It blocks and returns the
	// terminal error when the loop exits.
	Run(ctx context.Context, account gen.ImAccount, inbound chan<- InboundMessage) error

	// Send pushes one outbound text message into a chat.
	Send(ctx context.Context, account gen.ImAccount, chatID string, text string) error
}

// InboundMessage is one received IM message normalized across providers.
type InboundMessage struct {
	// AccountID identifies the receiving bot account (ImAccount.ID).
	AccountID string
	// ChatID is the provider chat identifier the message came from.
	ChatID string
	// FromUser is the provider user id of the sender; the account's
	// AllowUsers list gates on this value.
	FromUser string
	// Text is the normalized message body.
	Text string
	// ContextToken optionally carries a provider-side continuation context
	// (e.g. a reply-to marker) that the agent turn can echo back.
	ContextToken string
}

// providerRegistry maps canonical provider ids to factories. Entries are
// installed by adapter files' init() (telegram.go lands in a follow-up card)
// and only read afterwards, so no locking is required.
var providerRegistry = map[string]func() Provider{}

// RegisterProvider installs a provider factory under its canonical id. It is
// meant for adapter init() functions only.
func RegisterProvider(id string, factory func() Provider) {
	providerRegistry[id] = factory
}

// LookupProvider instantiates the provider registered under id.
func LookupProvider(id string) (Provider, bool) {
	factory, ok := providerRegistry[id]
	if !ok {
		return nil, false
	}
	return factory(), true
}
