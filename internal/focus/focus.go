package focus

import (
	"context"
	"fmt"
	"sync"

	"waydict/internal/apperr"
)

type Policy string

const (
	CancelOnFocusChange Policy = "cancel_on_focus_change"
	WarnAndType         Policy = "warn_and_type"
	TypeCurrent         Policy = "type_current"
)

type Target struct {
	Backend        string
	StableID       string
	AppID          string
	AppName        string
	PID            int
	SecureField    bool
	DegradedReason string
	Token          uint64 `json:"-"`
}

type Provider interface {
	Backend() string
	Available(context.Context) error
	Current(context.Context) (Target, error)
	Same(context.Context, Target) (current Target, same bool, err error)
	Release(Target)
}

// Metadata keeps Linux v1 status compatible without widening Target.
type Metadata struct {
	FocusedID   int64
	FocusedName string
	Class       string
	Workspace   string
	Output      string
}

type MetadataProvider interface {
	Metadata(Target) Metadata
}

type Change struct {
	From Target
	To   Target
}

func (c *Change) Error() string {
	if c == nil {
		return "focus changed"
	}
	return fmt.Sprintf("focus changed from %s to %s", diagnosticID(c.From), diagnosticID(c.To))
}

type Guard struct {
	mu          sync.Mutex
	provider    Provider
	policy      Policy
	captured    *Target
	expectedPID int
}

func NewGuard(provider Provider, policy Policy) *Guard {
	return &Guard{provider: provider, policy: policy}
}

func (g *Guard) CaptureStart(ctx context.Context, expectedPID int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.resetLocked()
	g.expectedPID = expectedPID
	if g.policy == TypeCurrent {
		return nil
	}
	if g.provider == nil {
		return apperr.New(apperr.CodeFocusUnavailable, "capture focus", fmt.Errorf("focus provider is unavailable"))
	}
	target, err := g.provider.Current(ctx)
	if err != nil {
		return providerError(apperr.CodeFocusUnavailable, "capture focus", err)
	}
	if target.SecureField {
		g.provider.Release(target)
		return secureFieldError("capture focus")
	}
	if err := validateOwnedTarget(target); err != nil {
		g.provider.Release(target)
		return err
	}
	if expectedPID != 0 && target.PID != expectedPID {
		g.provider.Release(target)
		return expectedPIDError(expectedPID, target.PID)
	}
	g.captured = &target
	return nil
}

func (g *Guard) ResolveForInjection(ctx context.Context) (Target, *Change, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.provider == nil {
		g.resetLocked()
		return Target{}, nil, apperr.New(apperr.CodeFocusUnavailable, "resolve focus", fmt.Errorf("focus provider is unavailable"))
	}
	if g.policy == TypeCurrent {
		return g.resolveCurrentLocked(ctx)
	}
	return g.resolveCapturedLocked(ctx)
}

func (g *Guard) StartedMetadata() *Target {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.captured == nil {
		return nil
	}
	target := *g.captured
	target.Token = 0
	return &target
}

func (g *Guard) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.resetLocked()
}

func (g *Guard) resolveCurrentLocked(ctx context.Context) (Target, *Change, error) {
	target, err := g.provider.Current(ctx)
	if err != nil {
		g.resetLocked()
		return Target{}, nil, providerError(apperr.CodeFocusUnavailable, "capture current focus", err)
	}
	if target.SecureField {
		g.provider.Release(target)
		g.resetLocked()
		return Target{}, nil, secureFieldError("capture current focus")
	}
	if err := validateOwnedTarget(target); err != nil {
		g.provider.Release(target)
		g.resetLocked()
		return Target{}, nil, err
	}
	if g.expectedPID != 0 && target.PID != g.expectedPID {
		g.provider.Release(target)
		g.resetLocked()
		return Target{}, nil, expectedPIDError(g.expectedPID, target.PID)
	}
	current, same, err := g.provider.Same(ctx, target)
	if err != nil {
		g.provider.Release(target)
		g.resetLocked()
		return Target{}, nil, providerError(apperr.CodeFocusUnavailable, "validate current focus", err)
	}
	if current.SecureField {
		g.provider.Release(current)
		g.provider.Release(target)
		g.resetLocked()
		return Target{}, nil, secureFieldError("validate current focus")
	}
	if err := validateOwnedTarget(current); err != nil {
		g.provider.Release(current)
		g.provider.Release(target)
		g.resetLocked()
		return Target{}, nil, err
	}
	if !same {
		change := newChange(target, current)
		g.provider.Release(current)
		g.provider.Release(target)
		g.resetLocked()
		return Target{}, nil, apperr.New(apperr.CodeFocusChanged, "validate current focus", change)
	}
	g.provider.Release(current)
	g.captured = nil
	g.expectedPID = 0
	return target, nil, nil
}

func (g *Guard) resolveCapturedLocked(ctx context.Context) (Target, *Change, error) {
	if g.captured == nil {
		g.resetLocked()
		return Target{}, nil, apperr.New(apperr.CodeFocusUnavailable, "resolve focus", fmt.Errorf("session focus was not captured"))
	}
	start := *g.captured
	if start.SecureField {
		g.resetLocked()
		return Target{}, nil, secureFieldError("resolve focus")
	}
	current, same, err := g.provider.Same(ctx, start)
	if err != nil {
		g.resetLocked()
		return Target{}, nil, providerError(apperr.CodeFocusUnavailable, "compare focus", err)
	}
	if current.SecureField {
		g.provider.Release(current)
		g.resetLocked()
		return Target{}, nil, secureFieldError("compare focus")
	}
	if err := validateOwnedTarget(current); err != nil {
		g.provider.Release(current)
		g.resetLocked()
		return Target{}, nil, err
	}
	if same {
		g.provider.Release(current)
		g.captured = nil
		g.expectedPID = 0
		return start, nil, nil
	}
	change := newChange(start, current)
	if g.policy == WarnAndType {
		g.provider.Release(start)
		g.captured = nil
		g.expectedPID = 0
		return current, change, nil
	}
	g.provider.Release(current)
	g.resetLocked()
	return Target{}, nil, apperr.New(apperr.CodeFocusChanged, "compare focus", change)
}

func (g *Guard) resetLocked() {
	if g.captured != nil && g.provider != nil {
		g.provider.Release(*g.captured)
	}
	g.captured = nil
	g.expectedPID = 0
}

// ValidateTarget checks a caller-owned pinned target without taking ownership of it.
func ValidateTarget(ctx context.Context, provider Provider, target Target) error {
	if provider == nil {
		return apperr.New(apperr.CodeFocusUnavailable, "validate injection target", fmt.Errorf("focus provider is unavailable"))
	}
	if target.SecureField {
		return secureFieldError("validate injection target")
	}
	if err := validateBorrowedTarget(target); err != nil {
		return err
	}
	current, same, err := provider.Same(ctx, target)
	if err != nil {
		return providerError(apperr.CodeFocusUnavailable, "validate injection target", err)
	}
	defer provider.Release(current)
	if current.SecureField {
		return secureFieldError("validate injection target")
	}
	if err := validateOwnedTarget(current); err != nil {
		return err
	}
	if !same {
		return apperr.New(apperr.CodeFocusChanged, "validate injection target", newChange(target, current))
	}
	return nil
}

func validateOwnedTarget(target Target) error {
	return validateBorrowedTarget(target)
}

func validateBorrowedTarget(target Target) error {
	if target.DegradedReason == "" && (target.StableID == "" || target.Token == 0) {
		return apperr.New(apperr.CodeFocusUnavailable, "validate focus target", fmt.Errorf("focus provider returned an unusable target"))
	}
	return nil
}

func providerError(code, operation string, err error) error {
	if err == nil {
		return nil
	}
	if apperr.Code(err) != apperr.CodeInternalError {
		return err
	}
	return apperr.New(code, operation, err)
}

func secureFieldError(operation string) error {
	return apperr.New(apperr.CodeSecureField, operation, fmt.Errorf("target is a secure text field"))
}

func expectedPIDError(expected, actual int) error {
	return apperr.New(apperr.CodeFocusChanged, "capture focus", fmt.Errorf("expected PID %d, got %d", expected, actual))
}

func diagnosticID(target Target) string {
	if target.StableID != "" {
		return target.StableID
	}
	if target.AppID != "" {
		return target.AppID
	}
	if target.PID != 0 {
		return fmt.Sprintf("pid:%d", target.PID)
	}
	if target.Backend != "" {
		return target.Backend + ":unknown"
	}
	return "unknown"
}

func newChange(from, to Target) *Change {
	from.Token = 0
	to.Token = 0
	return &Change{From: from, To: to}
}
