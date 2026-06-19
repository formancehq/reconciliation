package models

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResolutionKind_WireLiterals pins the on-the-wire string for every
// resolution kind. The OpenAPI enum, docs, and storage docstrings all assume
// these exact values — drift between the constant and the API contract
// silently breaks client deserialization.
func TestResolutionKind_WireLiterals(t *testing.T) {
	require.Equal(t, ResolutionKind("auto"), ResolutionAuto)
	require.Equal(t, ResolutionKind("fixed_by_booking"), ResolutionFixedByBooking)
	require.Equal(t, ResolutionKind("accepted_by_business"), ResolutionAcceptedByBusiness)
}
