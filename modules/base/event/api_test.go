//go:build integration

package event

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/stretchr/testify/assert"
)

// TestCommit_EventNotFound tests that Commit handles non-existent events gracefully.
// This is the integration test for issue #339 fix.
// Run with: go test -tags=integration ./modules/base/event/...
// Requires: make env-test
func TestCommit_EventNotFound(t *testing.T) {
	_, ctx := testutil.NewTestServer()
	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	e := New(ctx)

	// Call Commit with a non-existent eventID
	// This should not panic - it should gracefully handle nil eventModel
	assert.NotPanics(t, func() {
		e.Commit(99999999) // Non-existent eventID
	})
}

// TestCommit_ValidEvent tests that Commit works correctly with a valid event.
// Run with: go test -tags=integration ./modules/base/event/...
// Requires: make env-test
func TestCommit_ValidEvent(t *testing.T) {
	_, ctx := testutil.NewTestServer()
	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	e := New(ctx)

	// Insert a test event using direct DB insert
	tx, err := ctx.DB().Begin()
	assert.NoError(t, err)

	eventID, err := e.db.InsertTx(&Model{
		Event: "test.event",
		Type:  1,
		Data:  "{}",
	}, tx)
	assert.NoError(t, err)
	err = tx.Commit()
	assert.NoError(t, err)

	// Call Commit with a valid eventID - should not panic
	assert.NotPanics(t, func() {
		e.Commit(eventID)
	})
}
