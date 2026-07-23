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

// TestAlertStatus_WireLiterals pins the on-the-wire string for every alert
// status. The CHECK constraint in migration #6 hardcodes these values.
func TestAlertStatus_WireLiterals(t *testing.T) {
	require.Equal(t, AlertStatus("OPEN"), AlertOpen)
	require.Equal(t, AlertStatus("ACKNOWLEDGED"), AlertAcknowledged)
	require.Equal(t, AlertStatus("RESOLVED"), AlertResolved)
}

// TestAlertEventType_WireLiterals pins the event type discriminator.
func TestAlertEventType_WireLiterals(t *testing.T) {
	require.Equal(t, AlertEventType("fail"), AlertEventFail)
	require.Equal(t, AlertEventType("pass"), AlertEventPass)
	require.Equal(t, AlertEventType("ack"), AlertEventAck)
	require.Equal(t, AlertEventType("resolve"), AlertEventResolve)
	require.Equal(t, AlertEventType("accept"), AlertEventAccept)
}

// TestAlertEvent_IsReopen helper predicate: a 'fail' event landing on a
// resolved alert IS a reopen by convention.
func TestAlertEvent_IsReopen(t *testing.T) {
	resolved := AlertResolved
	open := AlertOpen
	reopen := &AlertEvent{Type: AlertEventFail, PrevStatus: &resolved, NewStatus: AlertOpen}
	require.True(t, reopen.IsReopen())

	initialOpen := &AlertEvent{Type: AlertEventFail, PrevStatus: nil, NewStatus: AlertOpen}
	require.False(t, initialOpen.IsReopen(), "first-ever fail is not a reopen")

	updateOnOpen := &AlertEvent{Type: AlertEventFail, PrevStatus: &open, NewStatus: AlertOpen}
	require.False(t, updateOnOpen.IsReopen(), "fail on already-open is not a reopen")
}
