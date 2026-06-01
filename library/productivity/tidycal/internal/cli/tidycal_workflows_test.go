// Copyright 2026 Cathryn Lavery and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"testing"
	"time"
)

func TestResolveWorkflowWindowTreatsRelativeToAsInclusiveDate(t *testing.T) {
	loc := time.UTC

	window, err := resolveWorkflowWindow("", "today", "+1d", loc)
	if err != nil {
		t.Fatalf("resolveWorkflowWindow: %v", err)
	}

	if got := window.To.Sub(window.From); got != 48*time.Hour {
		t.Fatalf("window duration = %s, want 48h for today through +1d", got)
	}
}

func TestFilterBookingsInWindowSkipsUnparseableStartsAt(t *testing.T) {
	loc := time.UTC
	window := tidycalWindow{
		From: time.Date(2026, 6, 1, 0, 0, 0, 0, loc),
		To:   time.Date(2026, 6, 2, 0, 0, 0, 0, loc),
	}
	bookings := []workflowBooking{
		{ID: "bad", StartsAt: ""},
		{ID: "outside", StartsAt: "2026-06-03T10:00:00Z"},
		{ID: "inside", StartsAt: "2026-06-01T10:00:00Z"},
	}

	got := filterBookingsInWindow(bookings, window, loc, true)
	if len(got) != 1 || got[0].ID != "inside" {
		t.Fatalf("filtered bookings = %+v, want only inside booking", got)
	}
}

func TestBuildFollowupsKeepsCancelledReasonAheadOfIntakeAnswer(t *testing.T) {
	got := buildFollowups([]workflowBooking{
		{
			ID:          "cancelled",
			CancelledAt: "2026-06-01T10:00:00Z",
			Questions:   []bookingQuestion{{Question: "Anything else?", Answer: "Please follow up"}},
		},
	})

	if len(got) != 1 {
		t.Fatalf("followups len = %d, want 1", len(got))
	}
	if got[0].SuggestedReason != "cancelled_booking" {
		t.Fatalf("SuggestedReason = %q, want cancelled_booking", got[0].SuggestedReason)
	}
}
