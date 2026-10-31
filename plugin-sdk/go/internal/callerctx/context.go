package callerctx

import "context"

// context.go shares request metadata between the SDK HTTP adapter and its
// in-process testing harness. The package is internal to the SDK module so
// ordinary plugin code receives the public Caller view without needing to
// construct grant-bearing contexts or depend on their storage representation.

// Identity contains the host envelope fields needed by caller-scoped capabilities.
type Identity struct {
	UserID   int64  // verified account identifier
	Username string // verified account username
	Role     string // verified account role
	Locale   string // negotiated request language
	Grant    string // host-signed authorization ticket
	ClientIP string // host-resolved request address
}

// identityKey prevents collisions with unrelated context values from application handlers.
type identityKey struct{}

// With stores a copied identity value in a derived request context.
func With(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, identity)
}

// From retrieves the identity supplied by the SDK adapter or test harness.
func From(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityKey{}).(Identity)
	return identity, ok
}
