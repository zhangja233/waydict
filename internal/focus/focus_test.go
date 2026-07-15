package focus

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"waydict/internal/apperr"
)

type sameResult struct {
	target Target
	same   bool
	err    error
}

type fakeProvider struct {
	current []Target
	curErr  error
	same    []sameResult
	release map[uint64]int
}

func (f *fakeProvider) Backend() string                 { return "fake" }
func (f *fakeProvider) Available(context.Context) error { return nil }
func (f *fakeProvider) Current(context.Context) (Target, error) {
	if f.curErr != nil {
		return Target{}, f.curErr
	}
	target := f.current[0]
	f.current = f.current[1:]
	return target, nil
}
func (f *fakeProvider) Same(context.Context, Target) (Target, bool, error) {
	result := f.same[0]
	f.same = f.same[1:]
	if result.err != nil {
		return Target{}, false, result.err
	}
	return result.target, result.same, nil
}
func (f *fakeProvider) Release(target Target) {
	if f.release == nil {
		f.release = make(map[uint64]int)
	}
	f.release[target.Token]++
}

func TestGuardPolicyOwnershipPaths(t *testing.T) {
	providerErr := errors.New("provider failed")
	tests := []struct {
		name        string
		policy      Policy
		current     []Target
		same        []sameResult
		wantCode    string
		wantWarning bool
		wantTarget  uint64
		wantRelease map[uint64]int
	}{
		{"cancel same", CancelOnFocusChange, []Target{owned(1, "one")}, []sameResult{{owned(2, "one"), true, nil}}, "", false, 1, releases(1, 2)},
		{"cancel changed", CancelOnFocusChange, []Target{owned(1, "one")}, []sameResult{{owned(2, "two"), false, nil}}, apperr.CodeFocusChanged, false, 0, releases(1, 2)},
		{"cancel compare error", CancelOnFocusChange, []Target{owned(1, "one")}, []sameResult{{err: providerErr}}, apperr.CodeFocusUnavailable, false, 0, releases(1)},
		{"warn same", WarnAndType, []Target{owned(1, "one")}, []sameResult{{owned(2, "one"), true, nil}}, "", false, 1, releases(1, 2)},
		{"warn changed", WarnAndType, []Target{owned(1, "one")}, []sameResult{{owned(2, "two"), false, nil}}, "", true, 2, releases(1, 2)},
		{"warn compare error", WarnAndType, []Target{owned(1, "one")}, []sameResult{{err: providerErr}}, apperr.CodeFocusUnavailable, false, 0, releases(1)},
		{"current same", TypeCurrent, []Target{owned(1, "one")}, []sameResult{{owned(2, "one"), true, nil}}, "", false, 1, releases(1, 2)},
		{"current changed", TypeCurrent, []Target{owned(1, "one")}, []sameResult{{owned(2, "two"), false, nil}}, apperr.CodeFocusChanged, false, 0, releases(1, 2)},
		{"current compare error", TypeCurrent, []Target{owned(1, "one")}, []sameResult{{err: providerErr}}, apperr.CodeFocusUnavailable, false, 0, releases(1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &fakeProvider{current: append([]Target(nil), tt.current...), same: append([]sameResult(nil), tt.same...)}
			guard := NewGuard(provider, tt.policy)
			if err := guard.CaptureStart(context.Background(), 0); err != nil {
				t.Fatalf("CaptureStart: %v", err)
			}
			target, warning, err := guard.ResolveForInjection(context.Background())
			if got := apperr.Code(err); got != tt.wantCode {
				t.Fatalf("error code = %q, want %q (%v)", got, tt.wantCode, err)
			}
			if (warning != nil) != tt.wantWarning {
				t.Fatalf("warning = %#v", warning)
			}
			if target.Token != tt.wantTarget {
				t.Fatalf("target token = %d, want %d", target.Token, tt.wantTarget)
			}
			if err == nil {
				provider.Release(target)
			}
			guard.Reset()
			if !reflect.DeepEqual(provider.release, tt.wantRelease) {
				t.Fatalf("releases = %#v, want %#v", provider.release, tt.wantRelease)
			}
		})
	}
}

func TestGuardRejectsSecureAndInvalidTargets(t *testing.T) {
	tests := []struct {
		name        string
		policy      Policy
		capture     Target
		current     Target
		wantCode    string
		wantRelease map[uint64]int
	}{
		{"secure captured", CancelOnFocusChange, secure(1, "one"), Target{}, apperr.CodeSecureField, releases(1)},
		{"invalid captured", WarnAndType, Target{Backend: "fake", Token: 1}, Target{}, apperr.CodeFocusUnavailable, releases(1)},
		{"secure comparison", CancelOnFocusChange, owned(1, "one"), secure(2, "one"), apperr.CodeSecureField, releases(1, 2)},
		{"secure current", TypeCurrent, secure(1, "one"), Target{}, apperr.CodeSecureField, releases(1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &fakeProvider{current: []Target{tt.capture}}
			if tt.current.Token != 0 {
				provider.same = []sameResult{{target: tt.current, same: true}}
			}
			guard := NewGuard(provider, tt.policy)
			err := guard.CaptureStart(context.Background(), 0)
			if err == nil {
				_, _, err = guard.ResolveForInjection(context.Background())
			}
			if got := apperr.Code(err); got != tt.wantCode {
				t.Fatalf("error code = %q, want %q (%v)", got, tt.wantCode, err)
			}
			guard.Reset()
			if !reflect.DeepEqual(provider.release, tt.wantRelease) {
				t.Fatalf("releases = %#v, want %#v", provider.release, tt.wantRelease)
			}
		})
	}
}

func TestGuardExpectedPIDMismatch(t *testing.T) {
	for _, policy := range []Policy{CancelOnFocusChange, WarnAndType, TypeCurrent} {
		t.Run(string(policy), func(t *testing.T) {
			provider := &fakeProvider{current: []Target{withPID(owned(1, "one"), 42)}}
			guard := NewGuard(provider, policy)
			err := guard.CaptureStart(context.Background(), 7)
			if policy == TypeCurrent && err == nil {
				_, _, err = guard.ResolveForInjection(context.Background())
			}
			if got := apperr.Code(err); got != apperr.CodeFocusChanged {
				t.Fatalf("error code = %q (%v)", got, err)
			}
			if !reflect.DeepEqual(provider.release, releases(1)) {
				t.Fatalf("releases = %#v", provider.release)
			}
		})
	}
}

func TestGuardProviderFailureOwnership(t *testing.T) {
	providerErr := errors.New("unavailable")
	tests := []struct {
		name        string
		policy      Policy
		current     []Target
		currentErr  error
		same        []sameResult
		resolve     bool
		wantCode    string
		wantRelease map[uint64]int
	}{
		{"capture error", CancelOnFocusChange, nil, providerErr, nil, false, apperr.CodeFocusUnavailable, nil},
		{"invalid comparison target", CancelOnFocusChange, []Target{owned(1, "one")}, nil, []sameResult{{target: Target{Backend: "fake", Token: 2}, same: true}}, true, apperr.CodeFocusUnavailable, releases(1, 2)},
		{"current error", TypeCurrent, nil, providerErr, nil, true, apperr.CodeFocusUnavailable, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &fakeProvider{current: tt.current, curErr: tt.currentErr, same: tt.same}
			guard := NewGuard(provider, tt.policy)
			err := guard.CaptureStart(context.Background(), 0)
			if err == nil && tt.resolve {
				_, _, err = guard.ResolveForInjection(context.Background())
			}
			if got := apperr.Code(err); got != tt.wantCode {
				t.Fatalf("code = %q, want %q (%v)", got, tt.wantCode, err)
			}
			guard.Reset()
			if !reflect.DeepEqual(provider.release, tt.wantRelease) {
				t.Fatalf("releases = %#v, want %#v", provider.release, tt.wantRelease)
			}
		})
	}
}

func TestGuardCaptureReplacementAndResetReleaseOnce(t *testing.T) {
	provider := &fakeProvider{current: []Target{owned(1, "one"), owned(2, "two")}}
	guard := NewGuard(provider, CancelOnFocusChange)
	if err := guard.CaptureStart(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if err := guard.CaptureStart(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	guard.Reset()
	guard.Reset()
	if !reflect.DeepEqual(provider.release, releases(1, 2)) {
		t.Fatalf("releases = %#v", provider.release)
	}
}

func TestValidateTargetReleasesOnlyTransientOwnership(t *testing.T) {
	provider := &fakeProvider{same: []sameResult{{target: owned(2, "one"), same: true}}}
	pinned := owned(1, "one")
	if err := ValidateTarget(context.Background(), provider, pinned); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(provider.release, releases(2)) {
		t.Fatalf("releases before caller release = %#v", provider.release)
	}
	provider.Release(pinned)
	if !reflect.DeepEqual(provider.release, releases(1, 2)) {
		t.Fatalf("releases = %#v", provider.release)
	}
}

func TestGuardResetMetadataAndDegradedFallback(t *testing.T) {
	degraded := Target{Backend: "fake", DegradedReason: "PID-only fallback", PID: 9}
	provider := &fakeProvider{current: []Target{degraded}}
	guard := NewGuard(provider, CancelOnFocusChange)
	if err := guard.CaptureStart(context.Background(), 9); err != nil {
		t.Fatal(err)
	}
	metadata := guard.StartedMetadata()
	if metadata == nil || metadata.Token != 0 || metadata.DegradedReason == "" {
		t.Fatalf("metadata = %#v", metadata)
	}
	guard.Reset()
	if !reflect.DeepEqual(provider.release, releases(0)) {
		t.Fatalf("releases = %#v", provider.release)
	}
}

func TestTargetTokenNeverSerializes(t *testing.T) {
	data, err := json.Marshal(owned(99, "secret"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || containsToken(data) {
		t.Fatalf("serialized target leaks token: %s", data)
	}
}

func owned(token uint64, id string) Target {
	return Target{Backend: "fake", StableID: id, Token: token}
}

func secure(token uint64, id string) Target {
	target := owned(token, id)
	target.SecureField = true
	return target
}

func withPID(target Target, pid int) Target {
	target.PID = pid
	return target
}

func releases(tokens ...uint64) map[uint64]int {
	out := make(map[uint64]int, len(tokens))
	for _, token := range tokens {
		out[token]++
	}
	return out
}

func containsToken(data []byte) bool {
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return true
	}
	_, ok := fields["Token"]
	if ok {
		return true
	}
	_, ok = fields["token"]
	return ok
}
