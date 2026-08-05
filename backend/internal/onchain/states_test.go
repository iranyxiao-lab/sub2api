package onchain

import "testing"

func TestIntentTransitions(t *testing.T) {
	t.Parallel()
	if !CanTransitionIntent(IntentPending, IntentPartiallyPaid) {
		t.Fatal("pending intent must allow partial payment")
	}
	if CanTransitionIntent(IntentSettled, IntentSettling) {
		t.Fatal("settled intent must not be fulfilled twice")
	}
	if !CanTransitionIntent(IntentSettled, IntentReviewRequired) {
		t.Fatal("post-settlement deposits must be reviewable")
	}
}

func TestStateMachineTerminalAndRecoveryRules(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		allowed bool
	}{
		{"deposit review can resume credit", CanTransitionDeposit(DepositReviewRequired, DepositCreditPending)},
		{"credited deposit cannot be credited twice", CanTransitionDeposit(DepositCredited, DepositCreditPending)},
		{"credited deposit can enter reconciliation review", CanTransitionDeposit(DepositCredited, DepositReviewRequired)},
		{"hash conflict cannot self-heal", CanTransitionCursor(CursorHashConflict, CursorHealthy)},
		{"failed transfer can be rebuilt", CanTransitionTransfer(TransferFailed, TransferPrepared)},
		{"finalized transfer is terminal", CanTransitionTransfer(TransferFinalized, TransferPrepared)},
		{"nonce conflict requires reconciliation", CanTransitionNonce(NonceConflict, NonceReconciling)},
		{"replacement can observe original success", CanTransitionTransaction(TransactionReplaced, TransactionFinalized)},
		{"finalized transaction is terminal", CanTransitionTransaction(TransactionFinalized, TransactionPending)},
		{"approved review can resolve", CanTransitionReview(ReviewApproved, ReviewResolved)},
	}
	want := []bool{true, false, true, false, true, false, true, true, false, true}
	for i, test := range tests {
		if test.allowed != want[i] {
			t.Fatalf("%s: allowed = %v, want %v", test.name, test.allowed, want[i])
		}
	}
}
