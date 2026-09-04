package service

import (
	"context"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveActor(t *testing.T) {
	t.Parallel()

	t.Run("verified subject is authoritative, declared demoted to a note", func(t *testing.T) {
		ctx := WithSubject(context.Background(), "auth0|alice")
		by, actor, err := resolveActor(ctx, "someone-else@acme.com")
		require.NoError(t, err)
		assert.Equal(t, "auth0|alice", by, "By is the verified subject, not the self-declared name")
		require.NotNil(t, actor)
		assert.Equal(t, models.ActorSourceToken, actor.Source)
		assert.Equal(t, "auth0|alice", actor.Subject)
		assert.Equal(t, "someone-else@acme.com", actor.Declared, "self-declared name preserved as a note")
	})

	t.Run("no subject falls back to the declared name, tagged unverified", func(t *testing.T) {
		by, actor, err := resolveActor(context.Background(), "ops@acme.com")
		require.NoError(t, err)
		assert.Equal(t, "ops@acme.com", by)
		require.NotNil(t, actor)
		assert.Equal(t, models.ActorSourceDeclared, actor.Source)
		assert.Empty(t, actor.Subject)
		assert.Equal(t, "ops@acme.com", actor.Declared)
	})

	t.Run("unauthenticated and unattributed is rejected", func(t *testing.T) {
		_, _, err := resolveActor(context.Background(), "   ")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not authenticated")
	})
}

// seedOpenAlert puts a single OPEN alert into the fake store and returns its id.
func seedOpenAlert(f *fakeV1Store) uuid.UUID {
	id := uuid.New()
	f.alerts[id] = &models.Alert{
		ID:              id,
		RuleID:          uuid.New(),
		Fingerprint:     "asset:USD/2",
		Status:          models.AlertOpen,
		Severity:        models.SeverityHigh,
		ContractVersion: models.ContractVersionV1,
	}
	return id
}

func TestAckAlert_BindsActor(t *testing.T) {
	t.Parallel()
	svc, store := newValidatingService(t, validatingLedger{})

	t.Run("authenticated caller binds the verified subject", func(t *testing.T) {
		id := seedOpenAlert(store)
		ctx := WithSubject(context.Background(), "auth0|bob")
		got, err := svc.AckAlert(ctx, id, &AckAlertRequest{By: "typed-name", Note: "looking"})
		require.NoError(t, err)
		require.NotNil(t, got.Ack.Actor)
		assert.Equal(t, models.ActorSourceToken, got.Ack.Actor.Source)
		assert.Equal(t, "auth0|bob", got.Ack.Actor.Subject)
		assert.Equal(t, "auth0|bob", got.Ack.By)
	})

	t.Run("unauthenticated caller falls back to the declared name", func(t *testing.T) {
		id := seedOpenAlert(store)
		got, err := svc.AckAlert(context.Background(), id, &AckAlertRequest{By: "ops@acme.com"})
		require.NoError(t, err)
		require.NotNil(t, got.Ack.Actor)
		assert.Equal(t, models.ActorSourceDeclared, got.Ack.Actor.Source)
		assert.Equal(t, "ops@acme.com", got.Ack.By)
	})

	t.Run("unauthenticated and unattributed is rejected", func(t *testing.T) {
		id := seedOpenAlert(store)
		_, err := svc.AckAlert(context.Background(), id, &AckAlertRequest{})
		require.Error(t, err)
	})
}

func TestResolveAndAccept_BindActor(t *testing.T) {
	t.Parallel()
	svc, store := newValidatingService(t, validatingLedger{})

	t.Run("resolve binds the verified subject onto the resolution", func(t *testing.T) {
		id := seedOpenAlert(store)
		ctx := WithSubject(context.Background(), "auth0|carol")
		got, err := svc.ResolveAlert(ctx, id, &ResolveAlertRequest{By: "note-only", Note: "rebooked"})
		require.NoError(t, err)
		require.NotNil(t, got.Resolution.Actor)
		assert.Equal(t, models.ActorSourceToken, got.Resolution.Actor.Source)
		assert.Equal(t, "auth0|carol", got.Resolution.By)
	})

	t.Run("accept binds the verified subject onto the resolution", func(t *testing.T) {
		id := seedOpenAlert(store)
		ctx := WithSubject(context.Background(), "auth0|dave")
		got, err := svc.AcceptAlert(ctx, id, &AcceptAlertRequest{Note: "known timing diff"})
		require.NoError(t, err)
		require.NotNil(t, got.Resolution.Actor)
		assert.Equal(t, models.ActorSourceToken, got.Resolution.Actor.Source)
		assert.Equal(t, "auth0|dave", got.Resolution.By)
	})
}

func TestSnoozeAndUnsnooze_BindActor(t *testing.T) {
	t.Parallel()
	svc, store := newValidatingService(t, validatingLedger{})
	future := time.Now().Add(time.Hour)

	t.Run("snooze binds the verified subject onto the snooze", func(t *testing.T) {
		id := seedOpenAlert(store)
		ctx := WithSubject(context.Background(), "auth0|erin")
		got, err := svc.SnoozeAlert(ctx, id, &SnoozeAlertRequest{Until: future, By: "typed-name", Note: "muting"})
		require.NoError(t, err)
		require.NotNil(t, got.Snooze.Actor)
		assert.Equal(t, models.ActorSourceToken, got.Snooze.Actor.Source)
		assert.Equal(t, "auth0|erin", got.Snooze.Actor.Subject)
		assert.Equal(t, "auth0|erin", got.Snooze.By)
	})

	t.Run("snooze falls back to the declared name when unauthenticated", func(t *testing.T) {
		id := seedOpenAlert(store)
		got, err := svc.SnoozeAlert(context.Background(), id, &SnoozeAlertRequest{Until: future, By: "ops@acme.com"})
		require.NoError(t, err)
		require.NotNil(t, got.Snooze.Actor)
		assert.Equal(t, models.ActorSourceDeclared, got.Snooze.Actor.Source)
		assert.Equal(t, "ops@acme.com", got.Snooze.By)
	})

	// The token path no longer needs a declared 'by' — previously snooze/unsnooze
	// rejected an empty 'by' outright, so an authenticated caller had to type a name.
	t.Run("unsnooze works from a verified token alone", func(t *testing.T) {
		id := seedOpenAlert(store)
		ctx := WithSubject(context.Background(), "auth0|frank")
		_, err := svc.SnoozeAlert(ctx, id, &SnoozeAlertRequest{Until: future})
		require.NoError(t, err)
		got, err := svc.UnsnoozeAlert(ctx, id, &UnsnoozeAlertRequest{})
		require.NoError(t, err)
		require.Nil(t, got.Snooze)
	})

	t.Run("unsnooze rejects an unauthenticated, unattributed caller", func(t *testing.T) {
		id := seedOpenAlert(store)
		_, err := svc.UnsnoozeAlert(context.Background(), id, &UnsnoozeAlertRequest{})
		require.Error(t, err)
	})
}
