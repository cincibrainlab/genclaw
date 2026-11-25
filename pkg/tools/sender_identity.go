package tools

import "context"

// SenderIdentity describes the unverified sender supplied by an execution's
// caller. It must not be used as proof of authentication or authorization.
type SenderIdentity struct {
	Channel string
	RawID   string
}

func (id SenderIdentity) QualifiedID() string {
	if id.Channel == "" || id.RawID == "" {
		return ""
	}
	return id.Channel + ":" + id.RawID
}

type senderIdentityKey struct{}
type syntheticSenderKey struct{}

// WithSyntheticSender marks an execution that has no human sender.
func WithSyntheticSender(ctx context.Context) context.Context {
	return context.WithValue(ctx, syntheticSenderKey{}, true)
}

// WithoutSyntheticSender starts a new inbound execution independently of its parent.
func WithoutSyntheticSender(ctx context.Context) context.Context {
	return context.WithValue(ctx, syntheticSenderKey{}, false)
}

// IsSyntheticSender reports whether the execution was marked synthetic.
func IsSyntheticSender(ctx context.Context) bool {
	synthetic, _ := ctx.Value(syntheticSenderKey{}).(bool)
	return synthetic
}

// WithSenderIdentity replaces any inherited identity, including when either
// component is missing, so unrelated executions cannot inherit a sender.
func WithSenderIdentity(ctx context.Context, channel, rawID string) context.Context {
	return context.WithValue(ctx, senderIdentityKey{}, SenderIdentity{Channel: channel, RawID: rawID})
}

func SenderIdentityFromContext(ctx context.Context) (SenderIdentity, bool) {
	id, ok := ctx.Value(senderIdentityKey{}).(SenderIdentity)
	if !ok || id.Channel == "" || id.RawID == "" {
		return SenderIdentity{}, false
	}
	return id, true
}
