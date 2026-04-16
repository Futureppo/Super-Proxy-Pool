package pools

import (
	"testing"
)

func TestInternalPort(t *testing.T) {
	if port := InternalPort(1); port != 30001 {
		t.Fatalf("expected 30001, got %d", port)
	}
	if port := InternalPort(42); port != 30042 {
		t.Fatalf("expected 30042, got %d", port)
	}
}
